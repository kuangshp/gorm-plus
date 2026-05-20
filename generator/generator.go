package generator

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"gorm.io/driver/mysql"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// ═══════════════════════════════════════════════════════════
//  路径工具
// ═══════════════════════════════════════════════════════════

func findProjectRoot(startDir string) (string, error) {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到 go.mod，请确认项目结构正确")
		}
		dir = parent
	}
}

func resolveConfigPaths(cfg *Config) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取当前工作目录失败: %w", err)
	}
	projectRoot, err := findProjectRoot(cwd)
	if err != nil {
		return fmt.Errorf("查找项目根目录失败: %w", err)
	}
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(projectRoot, p)
	}
	cfg.OutPath = resolve(cfg.OutPath)
	cfg.ModelPkgPath = resolve(cfg.ModelPkgPath)
	cfg.RepoPath = resolve(cfg.RepoPath)
	cfg.ApiPath = resolve(cfg.ApiPath)
	cfg.VoPath = resolve(cfg.VoPath)
	cfg.DtoPath = resolve(cfg.DtoPath)
	cfg.MapperPath = resolve(cfg.MapperPath)
	return nil
}

// ═══════════════════════════════════════════════════════════
//  嵌入模板
// ═══════════════════════════════════════════════════════════

//go:embed template/api_template.txt
var embeddedApiTemplate string

//go:embed template/dto_template.txt
var embeddedDtoTemplate string

//go:embed template/base_api_template.txt
var embeddedBaseApiTemplate string

//go:embed template/repository_gen_template.txt
var embeddedRepoGenTemplate string

//go:embed template/repository_template.txt
var embeddedRepoTemplate string

//go:embed template/vo_template.txt
var embeddedVoTemplate string

//go:embed template/mapper_template.txt
var embeddedMapperTemplate string

var embeddedTemplates = map[string]string{
	"api_template.txt":            embeddedApiTemplate,
	"dto_template.txt":            embeddedDtoTemplate,
	"base_api_template.txt":       embeddedBaseApiTemplate,
	"repository_gen_template.txt": embeddedRepoGenTemplate,
	"repository_template.txt":     embeddedRepoTemplate,
	"vo_template.txt":             embeddedVoTemplate,
	"mapper_template.txt":         embeddedMapperTemplate,
}

// ═══════════════════════════════════════════════════════════
//  gen.go 历史表解析
// ═══════════════════════════════════════════════════════════

// parseTablesFromGenFile 从已有 gen.go 的 Use() 函数中提取所有历史表名。
//
// gorm-gen 实际生成的 gen.go 格式（注意：无 Do 后缀）：
//
//	func Use(db *gorm.DB, opts ...gen.DOOption) *Query {
//	    return &Query{
//	        AccountEntity: newAccountEntity(db, opts...),   // ← 匹配字段名
//	        SysUserEntity: newSysUserEntity(db, opts...),
//	    }
//	}
//
// 正则匹配 `XxxEntity: newXxx(` 中的字段名前缀，转换为数据库表名：
//
//	AccountEntity → Account → account
//	SysUserEntity → SysUser → sys_user
func parseTablesFromGenFile(outPath string) []string {
	genFile := filepath.Join(outPath, "gen.go")
	data, err := os.ReadFile(genFile)
	if err != nil {
		// gen.go 不存在（首次运行），返回空切片
		return nil
	}

	// 精确匹配：字段名 XxxEntity 后跟 `: new`
	// 从字段名提取 ModelName 前缀（去掉 Entity 后缀）
	re := regexp.MustCompile(`(\w+)Entity\s*:\s*new\w+\(`)
	matches := re.FindAllSubmatch(data, -1)

	seen := make(map[string]bool)
	var tables []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		modelPrefix := string(m[1]) // 如 "Account"、"SysUser"
		if modelPrefix == "" {
			continue
		}
		tbl := camelToSnake(modelPrefix) // SysUser → sys_user
		if !seen[tbl] {
			seen[tbl] = true
			tables = append(tables, tbl)
		}
	}
	return tables
}

// camelToSnake 驼峰转下划线：SysUser → sys_user，Account → account
func camelToSnake(s string) string {
	var result []rune
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prevIsLower := unicode.IsLower(runes[i-1])
				nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if prevIsLower || nextIsLower {
					result = append(result, '_')
				}
			}
			result = append(result, unicode.ToLower(r))
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// mergeTableNames 合并历史表和新表，去重保持顺序
func mergeTableNames(existing []string, newTables ...string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(existing)+len(newTables))
	for _, t := range existing {
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	for _, t := range newTables {
		if t != "" && !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}

// ═══════════════════════════════════════════════════════════
//  命名工具
// ═══════════════════════════════════════════════════════════

func getGoctlPath() string {
	cmd := exec.Command("which", "go")
	output, err := cmd.Output()
	if err != nil {
		return "goctl"
	}
	goPath := strings.TrimSpace(string(output))
	goDir := goPath[:strings.LastIndex(goPath, "/")]
	return goDir + "/goctl"
}

func Case2Camel(name string) string {
	name = strings.Replace(name, "_", " ", -1)
	name = strings.Title(name)
	acronyms := []string{"IP", "ID", "URL", "API", "IOS", "XML", "JSON", "JWT", "SQL", "ORM"}
	for _, acronym := range acronyms {
		name = strings.ReplaceAll(name, strings.Title(strings.ToLower(acronym)), acronym)
	}
	return strings.Replace(name, " ", "", -1)
}

func LowerCamelCase(name string) string {
	if len(name) > 0 && name[0] >= 'a' && name[0] <= 'z' {
		hasUpper := false
		for _, c := range name[1:] {
			if c >= 'A' && c <= 'Z' {
				hasUpper = true
				break
			}
		}
		if !hasUpper {
			return name
		}
	}
	if name == "ID" {
		return "id"
	}
	if len(name) >= 2 && strings.HasSuffix(name, "ID") && name[:2] != "id" {
		return LowerCamelCase(name[:len(name)-2]) + "Id"
	}
	name = Case2Camel(name)
	return strings.ToLower(name[:1]) + name[1:]
}

func lowerFirst(name string) string { return LowerCamelCase(name) }

// ═══════════════════════════════════════════════════════════
//  标准输入
// ═══════════════════════════════════════════════════════════

var inputLines []string
var inputIndex int

func readInput(prompt string) string {
	if inputIndex < len(inputLines) {
		line := inputLines[inputIndex]
		inputIndex++
		fmt.Println(line)
		return line
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(prompt)
	input, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(input)
}

func init() {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, _ := io.ReadAll(os.Stdin)
		if len(data) > 0 {
			inputLines = strings.Split(string(data), "\n")
			for i, line := range inputLines {
				inputLines[i] = strings.TrimSpace(line)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════
//  数据结构
// ═══════════════════════════════════════════════════════════

func getTableColumns(db *gorm.DB, tableName string) ([]ColumnInfo, error) {
	type Column struct {
		Field   string
		Type    string
		Null    string
		Key     string
		Default *string
		Extra   string
		Comment string
	}
	var columns []Column
	err := db.Raw(fmt.Sprintf("SHOW FULL COLUMNS FROM `%s`", tableName)).Scan(&columns).Error
	result := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		result[i] = ColumnInfo{
			Name:    col.Field,
			Type:    col.Type,
			CanNull: col.Null == "YES",
			IsKey:   col.Key == "PRI",
			Extra:   col.Extra,
			Comment: col.Comment,
		}
	}
	return result, err
}

type ColumnInfo struct {
	Name, Type, FieldName, FieldType string
	JsonTag, JsonTagOpt              string
	CanNull, IsKey                   bool
	Extra, Comment, Validate         string
	IsTimeType, IsAuditField         bool
	IsDecimalType                    bool
}

type ApiTemplateData struct {
	TableName, ModelName, EntityName, TableComment string
	Columns                                        []ColumnInfo
}

type VoTemplateData struct {
	TableName, ModelName, TableComment string
	Columns                            []ColumnInfo
}

type RepositoryTemplateData struct {
	ModelName, ModelNameLower         string
	EntityName, EntityNameLower       string
	Package, DaoPath, ModelPath       string
	ModelPkgName, RawsqlPkgPath       string
	Columns                           []ColumnInfo
	PrimaryKeyField, PrimaryKeyColumn string
	TableName                         string // 用于 SF / Cache 的 key 前缀（如 "sys_user.FindById"）
}

type MapperTemplateData struct {
	TableName, ModelName, ModelNameLower, TableComment string
	Package, ModelPkgPath, ModelPkgName                string
	DtoPkgPath, DtoPkgName, DtoStructName              string
	VoPkgPath, VoPkgName, VoStructName                 string
	SameDtoPkg, IsGoZero                               bool
	HasTimeField, HasDecimalField                      bool
	Columns                                            []ColumnInfo
}

// ═══════════════════════════════════════════════════════════
//  模板加载 & 渲染
// ═══════════════════════════════════════════════════════════

func loadTemplate(templatePath string) (*template.Template, error) {
	templateName := filepath.Base(templatePath)
	funcMap := template.FuncMap{"lowerFirst": lowerFirst}
	if fileContent, err := os.ReadFile(templatePath); err == nil {
		return template.New(templateName).Funcs(funcMap).Parse(string(fileContent))
	}
	embeddedContent, ok := embeddedTemplates[templateName]
	if !ok {
		return nil, fmt.Errorf("模板 %q 不存在且无内嵌版本", templatePath)
	}
	return template.New(templateName).Funcs(funcMap).Parse(embeddedContent)
}

func renderTemplate(tmplPath string, data any) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ═══════════════════════════════════════════════════════════
//  类型映射
// ═══════════════════════════════════════════════════════════

func getGoType(sqlType string) string {
	s := strings.ToLower(sqlType)
	switch {
	case strings.Contains(s, "varchar") || strings.Contains(s, "text") || strings.Contains(s, "char"):
		return "string"
	case strings.Contains(s, "int"):
		return "int64"
	case strings.Contains(s, "decimal") || strings.Contains(s, "float") || strings.Contains(s, "double"):
		return "float64"
	case strings.Contains(s, "datetime") || strings.Contains(s, "timestamp") || strings.Contains(s, "date"):
		return "int64"
	case strings.Contains(s, "json"):
		return "string"
	case strings.Contains(s, "bool"):
		return "bool"
	}
	return "string"
}

func getGoTypeForApiDto(sqlType string) string {
	s := strings.ToLower(sqlType)
	if strings.Contains(s, "decimal") || strings.Contains(s, "float") || strings.Contains(s, "double") {
		return "string"
	}
	return getGoType(sqlType)
}

func getGoTypeForVo(sqlType string) string { return getGoTypeForApiDto(sqlType) }

// ═══════════════════════════════════════════════════════════
//  路径工具
// ═══════════════════════════════════════════════════════════

func pathToPkg(path string) string {
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimSuffix(path, "/")
	if filepath.IsAbs(path) {
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
				path = rel
			}
		}
	}
	return path
}

func getLastPathSegment(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(path, "./"), "/")
	return parts[len(parts)-1]
}

func ensureDir(dir string) {
	if dir != "" {
		os.MkdirAll(dir, 0755)
	}
}

func writeFileIfNotExist(filePath, content, label string) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err = os.WriteFile(filePath, []byte(content), 0644); err != nil {
			fmt.Printf("写入 %s 失败: %v\n", label, err)
		} else {
			fmt.Printf("已生成: %s\n", filePath)
		}
	} else {
		fmt.Printf("已存在，跳过: %s\n", filePath)
	}
}

func writeFileAlways(filePath, content, label string) {
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		fmt.Printf("写入 %s 失败: %v\n", label, err)
	} else {
		fmt.Printf("已生成(覆盖): %s\n", filePath)
	}
}

// ═══════════════════════════════════════════════════════════
//  各类文件生成
// ═══════════════════════════════════════════════════════════

func getTableComment(db *gorm.DB, tableName string) string {
	var comment string
	db.Raw(fmt.Sprintf(
		"SELECT TABLE_COMMENT FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='%s'",
		tableName)).Scan(&comment)
	return comment
}

func generateValidateRule(col ColumnInfo) string {
	var rules []string
	if !col.CanNull {
		rules = append(rules, "required")
	}
	if col.IsKey {
		rules = append(rules, "uuid")
	}
	if col.FieldType == "string" && strings.Contains(col.Name, "email") {
		rules = append(rules, "email")
	}
	if col.FieldType == "string" && strings.Contains(col.Name, "mobile") {
		rules = append(rules, "mobile")
	}
	if strings.Contains(strings.ToLower(col.Comment), "1是") && strings.Contains(strings.ToLower(col.Comment), "2是") {
		re := regexp.MustCompile(`(\d+)是([^，,]+)[,，]?`)
		var vals []string
		for _, m := range re.FindAllStringSubmatch(col.Comment, -1) {
			if len(m) >= 3 {
				vals = append(vals, m[1])
			}
		}
		if len(vals) > 0 {
			rules = append(rules, "oneof="+strings.Join(vals, " "))
		}
	}
	if !col.CanNull && col.FieldType == "int64" &&
		(strings.Contains(col.Name, "status") || strings.Contains(col.Name, "type") || strings.Contains(col.Name, "is_")) {
		rules = append(rules, "gte=1")
	}
	return strings.Join(rules, ",")
}

func buildRepoData(columns []ColumnInfo, modelName, pkg, daoPath, modelPath, tableName string) RepositoryTemplateData {
	columnData := make([]ColumnInfo, len(columns))
	primaryKeyField, primaryKeyColumn := "ID", "id"
	for i, col := range columns {
		fn := Case2Camel(col.Name)
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: fn, FieldType: getGoType(col.Type),
			CanNull: col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
		}
		if col.IsKey {
			primaryKeyField = fn
			primaryKeyColumn = col.Name
		}
	}
	return RepositoryTemplateData{
		ModelName: modelName, ModelNameLower: LowerCamelCase(modelName),
		EntityName: modelName + "Entity", EntityNameLower: LowerCamelCase(modelName + "Entity"),
		Package: pkg, DaoPath: pkg + "/" + daoPath, ModelPath: pkg + "/" + modelPath,
		ModelPkgName: getLastPathSegment(modelPath), Columns: columnData,
		PrimaryKeyField: primaryKeyField, PrimaryKeyColumn: primaryKeyColumn,
		TableName: tableName,
	}
}

func generateRepositoryFile(columns []ColumnInfo, modelName, pkg, daoPath, modelPath, tmplPath, tableName string) (string, error) {
	return renderTemplate(tmplPath, buildRepoData(columns, modelName, pkg, daoPath, modelPath, tableName))
}

func generateRepositoryExtFile(columns []ColumnInfo, modelName, pkg, daoPath, modelPath, tmplPath, tableName string) (string, error) {
	return renderTemplate(tmplPath, buildRepoData(columns, modelName, pkg, daoPath, modelPath, tableName))
}

func generateApiFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		jsonTagOpt := ""
		if col.CanNull {
			jsonTagOpt = ",optional"
		}
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: Case2Camel(col.Name), FieldType: getGoTypeForApiDto(col.Type),
			JsonTag: LowerCamelCase(Case2Camel(col.Name)), JsonTagOpt: jsonTagOpt,
			CanNull: col.CanNull, IsKey: col.IsKey, Extra: col.Extra, Comment: col.Comment,
			Validate: generateValidateRule(ColumnInfo{
				CanNull: col.CanNull, IsKey: col.IsKey,
				FieldType: getGoTypeForApiDto(col.Type), Name: col.Name, Comment: col.Comment,
			}),
		}
	}
	tableComment := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}
	return renderTemplate(tmplPath, ApiTemplateData{
		TableName:    LowerCamelCase(Case2Camel(tableName)),
		ModelName:    modelName,
		EntityName:   Case2Camel(strings.ToUpper(tableName[:1]+tableName[1:])) + "Entity",
		TableComment: tableComment,
		Columns:      columnData,
	})
}

func generateVoFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: Case2Camel(col.Name), FieldType: getGoTypeForVo(col.Type),
			CanNull: col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
		}
	}
	tableComment := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}
	return renderTemplate(tmplPath, VoTemplateData{
		TableName: tableName, ModelName: modelName,
		TableComment: tableComment, Columns: columnData,
	})
}

func generateDtoFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: Case2Camel(col.Name), FieldType: getGoTypeForApiDto(col.Type),
			CanNull: col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
			Validate: generateValidateRule(col),
		}
	}
	tableComment := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}
	return renderTemplate(tmplPath, VoTemplateData{
		TableName: tableName, ModelName: modelName,
		TableComment: tableComment, Columns: columnData,
	})
}

func buildMapperData(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB,
	pkg, modelPkgPath, dtoPkgPath, voPkgPath string, isGoZero bool) MapperTemplateData {
	auditFields := map[string]bool{"created_by": true, "updated_by": true}
	columnData := make([]ColumnInfo, len(columns))
	hasTime, hasDecimal := false, false
	for i, col := range columns {
		s := strings.ToLower(col.Type)
		isTime := strings.Contains(s, "datetime") || strings.Contains(s, "timestamp") || strings.Contains(s, "date")
		isDecimal := strings.Contains(s, "decimal") || strings.Contains(s, "float") || strings.Contains(s, "double")
		if isTime {
			hasTime = true
		}
		if isDecimal {
			hasDecimal = true
		}
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: Case2Camel(col.Name), FieldType: getGoType(col.Type),
			CanNull: col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
			IsTimeType: isTime, IsAuditField: auditFields[col.Name], IsDecimalType: isDecimal,
		}
	}
	tableComment := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}
	dtoStructName, voStructName := "Create"+modelName+"DTO", modelName+"Vo"
	if isGoZero {
		dtoStructName, voStructName = "Create"+modelName+"Req", modelName+"Model"
	}
	return MapperTemplateData{
		TableName: tableName, ModelName: modelName, ModelNameLower: LowerCamelCase(modelName),
		TableComment: tableComment, Package: pkg,
		ModelPkgPath: modelPkgPath, ModelPkgName: getLastPathSegment(modelPkgPath),
		DtoPkgPath: dtoPkgPath, DtoPkgName: getLastPathSegment(dtoPkgPath), DtoStructName: dtoStructName,
		VoPkgPath: voPkgPath, VoPkgName: getLastPathSegment(voPkgPath), VoStructName: voStructName,
		SameDtoPkg: dtoPkgPath != "" && dtoPkgPath == voPkgPath, IsGoZero: isGoZero,
		HasTimeField: hasTime, HasDecimalField: hasDecimal, Columns: columnData,
	}
}

// ═══════════════════════════════════════════════════════════
//  单表文件生成入口
// ═══════════════════════════════════════════════════════════

func generateForTable(tbl string, cfg *Config, db *gorm.DB,
	repoGenTmplPath, repoTmplPath, apiTmplPath, voTmplPath, dtoTmplPath, mapperTmplPath string) {

	columns, err := getTableColumns(db, tbl)
	if err != nil {
		fmt.Printf("[%s] 获取表结构失败，跳过: %v\n", tbl, err)
		return
	}
	modelName := Case2Camel(strings.ToUpper(tbl[:1]) + tbl[1:])

	// Repository _gen.go（始终覆盖，与 model 保持同步）
	if cfg.RepoPath != "" {
		if content, err := generateRepositoryFile(columns, modelName, cfg.Package,
			pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoGenTmplPath, tbl); err != nil {
			fmt.Printf("[%s] 生成 repository_gen 失败: %v\n", tbl, err)
		} else {
			writeFileAlways(
				fmt.Sprintf("%s/%s_gen.go", cfg.RepoPath, strings.ToLower(modelName)),
				content, "repository_gen",
			)
		}

		// Repository .go（用户自定义，已存在则跳过）
		if content, err := generateRepositoryExtFile(columns, modelName, cfg.Package,
			pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoTmplPath, tbl); err != nil {
			fmt.Printf("[%s] 生成 repository 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%s.go", cfg.RepoPath, strings.ToLower(modelName)),
				content, "repository",
			)
		}
	}

	// API .api（go-zero 模式，已存在则跳过）
	if cfg.ApiPath != "" {
		baseApiFile := filepath.Join(cfg.ApiPath, "base.api")
		if _, err := os.Stat(baseApiFile); os.IsNotExist(err) {
			if content, ok := embeddedTemplates["base_api_template.txt"]; ok {
				if err := os.WriteFile(baseApiFile, []byte(content), 0644); err != nil {
					fmt.Printf("写入 base.api 失败: %v\n", err)
				} else {
					fmt.Printf("已生成: %s\n", baseApiFile)
				}
			}
		}
		if content, err := generateApiFile(tbl, columns, modelName, db, apiTmplPath); err != nil {
			fmt.Printf("[%s] 生成 api 失败: %v\n", tbl, err)
		} else {
			apiFile := fmt.Sprintf("%s/%s.api", cfg.ApiPath, LowerCamelCase(Case2Camel(tbl)))
			if _, err := os.Stat(apiFile); os.IsNotExist(err) {
				if err = os.WriteFile(apiFile, []byte(content), 0644); err != nil {
					fmt.Printf("[%s] 写入 api 失败: %v\n", tbl, err)
				} else {
					fmt.Printf("已生成: %s\n", apiFile)
					cmd := exec.Command(getGoctlPath(), "api", "go", "-api", apiFile,
						"--dir", filepath.Dir(cfg.ApiPath), "--style=goZero")
					if output, err := cmd.CombinedOutput(); err != nil {
						fmt.Printf("[%s] goctl 执行失败: %v\n%s\n", tbl, err, output)
					} else {
						fmt.Printf("[%s] go-zero 代码生成成功\n", tbl)
					}
				}
			} else {
				fmt.Printf("已存在，跳过: %s\n", apiFile)
			}
		}
	}

	// VO（非 go-zero 模式，已存在则跳过）
	if cfg.VoPath != "" && cfg.ApiPath == "" {
		if content, err := generateVoFile(tbl, columns, modelName, db, voTmplPath); err != nil {
			fmt.Printf("[%s] 生成 VO 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%sVo.go", cfg.VoPath, LowerCamelCase(Case2Camel(modelName))),
				content, "VO",
			)
		}
	}

	// DTO（非 go-zero 模式，已存在则跳过）
	if cfg.DtoPath != "" && cfg.ApiPath == "" {
		if content, err := generateDtoFile(tbl, columns, modelName, db, dtoTmplPath); err != nil {
			fmt.Printf("[%s] 生成 DTO 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%sDto.go", cfg.DtoPath, LowerCamelCase(Case2Camel(modelName))),
				content, "DTO",
			)
		}
	}

	// Mapper（已存在则跳过）
	if cfg.MapperPath != "" {
		dtoPkg, voPkg := "", ""
		if cfg.ApiPath == "" {
			dtoPkg = pathToPkg(cfg.DtoPath)
			voPkg = pathToPkg(cfg.VoPath)
		}
		data := buildMapperData(tbl, columns, modelName, db,
			cfg.Package, pathToPkg(cfg.ModelPkgPath), dtoPkg, voPkg, cfg.ApiPath != "")
		if content, err := renderTemplate(mapperTmplPath, data); err != nil {
			fmt.Printf("[%s] 生成 mapper 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%sMapper.go", cfg.MapperPath, LowerCamelCase(Case2Camel(tbl))),
				content, "mapper",
			)
		}
	}
}

// ═══════════════════════════════════════════════════════════
//  主入口
// ═══════════════════════════════════════════════════════════

func Generate(cfg *Config) error {
	if err := resolveConfigPaths(cfg); err != nil {
		return fmt.Errorf("解析路径失败: %w", err)
	}

	dsn := fmt.Sprintf("%s:%s@(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	db, err := gorm.Open(mysql.Open(dsn))
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}

	inputTable := strings.TrimSpace(readInput("请输入表名（直接回车同步所有表）: "))
	isSingleTable := inputTable != ""

	// 定位模板目录
	templateDir := os.Getenv("GENERATOR_TEMPLATE_DIR")
	if templateDir == "" {
		if exePath, err := os.Executable(); err == nil {
			checkDir := filepath.Dir(exePath)
			for i := 0; i < 10; i++ {
				testDir := filepath.Join(checkDir, "generator", "template")
				if _, statErr := os.Stat(testDir); statErr == nil {
					templateDir = testDir
					break
				}
				parent := filepath.Dir(checkDir)
				if parent == checkDir {
					break
				}
				checkDir = parent
			}
		}
	}
	if templateDir == "" {
		cwd, _ := os.Getwd()
		templateDir = filepath.Join(cwd, "generator", "template")
	}

	apiTmplPath := filepath.Join(templateDir, "api_template.txt")
	dtoTmplPath := filepath.Join(templateDir, "dto_template.txt")
	repoGenTmplPath := filepath.Join(templateDir, "repository_gen_template.txt")
	repoTmplPath := filepath.Join(templateDir, "repository_template.txt")
	voTmplPath := filepath.Join(templateDir, "vo_template.txt")
	mapperTmplPath := filepath.Join(templateDir, "mapper_template.txt")

	// gorm-gen 配置
	g := gen.NewGenerator(gen.Config{
		OutPath:           cfg.OutPath,
		ModelPkgPath:      cfg.ModelPkgPath,
		Mode:              gen.WithDefaultQuery | gen.WithoutContext | gen.WithQueryInterface,
		FieldNullable:     false,
		FieldCoverable:    false,
		FieldSignable:     false,
		FieldWithIndexTag: false,
		FieldWithTypeTag:  true,
	})
	g.UseDB(db)
	g.WithDataTypeMap(map[string]func(gorm.ColumnType) string{
		"int":       func(_ gorm.ColumnType) string { return "int64" },
		"int2":      func(_ gorm.ColumnType) string { return "int64" },
		"int4":      func(_ gorm.ColumnType) string { return "int64" },
		"mediumint": func(_ gorm.ColumnType) string { return "int64" },
		"smallint":  func(_ gorm.ColumnType) string { return "int64" },
		"integer":   func(_ gorm.ColumnType) string { return "int64" },
		"tinyint":   func(_ gorm.ColumnType) string { return "int64" },
		"bigint":    func(_ gorm.ColumnType) string { return "int64" },
		"json":      func(_ gorm.ColumnType) string { return "datatypes.JSON" },
		"decimal":   func(_ gorm.ColumnType) string { return "decimal.Decimal" },
	})
	g.WithModelNameStrategy(func(tableName string) string {
		return Case2Camel(strings.ToUpper(tableName[:1])+tableName[1:]) + "Entity"
	})

	fieldOpts := []gen.ModelOpt{
		gen.FieldJSONTagWithNS(func(col string) string {
			if col == "deleted_at" {
				return "-"
			}
			return LowerCamelCase(Case2Camel(col))
		}),
		gen.FieldGORMTag("updated_at", func(_ field.GormTag) field.GormTag {
			return map[string][]string{"column": {"updated_at"}, "comment": {"更新时间"}}
		}),
		gen.FieldGORMTag("created_at", func(_ field.GormTag) field.GormTag {
			return map[string][]string{"column": {"created_at"}, "comment": {"创建时间"}}
		}),
		gen.FieldType("deleted_at", "gorm.DeletedAt"),
	}

	// ══════════════════════════════════════════════════════
	// 【第一步】确定 Model 生成范围并执行
	//
	// 单表模式追加策略：
	//   ① parseTablesFromGenFile 读取 gen.go 里已有表（字段名正则：XxxEntity: newXxx(）
	//   ② 合并历史表 + 本次新输入表（去重）
	//   ③ 用完整表集合调用 g.Execute() 重新生成 gen.go
	//   → gen.go 始终包含所有历史表，不会丢失
	// ══════════════════════════════════════════════════════
	fmt.Println("\n【第一步】生成数据模型（Model / DAO）...")

	var modelTables []string // 最终传给 g.Execute() 的表集合

	if isSingleTable {
		// ① 解析历史表
		historyTables := parseTablesFromGenFile(cfg.OutPath)
		fmt.Printf("  gen.go 中已记录 %d 张表: %v\n", len(historyTables), historyTables)

		// ② 合并
		modelTables = mergeTableNames(historyTables, inputTable)
		fmt.Printf("  合并后共 %d 张表（含本次 %q）: %v\n", len(modelTables), inputTable, modelTables)
	} else {
		if err := db.Raw("SHOW TABLES").Scan(&modelTables).Error; err != nil {
			return fmt.Errorf("获取表列表失败: %w", err)
		}
		if len(modelTables) == 0 {
			return fmt.Errorf("数据库 %s 中没有找到任何表", cfg.Database)
		}
		fmt.Printf("  全量模式，共 %d 张表\n", len(modelTables))
	}

	// ③ 统一生成（含所有历史表 + 新表）
	allModels := make([]interface{}, 0, len(modelTables))
	for _, tbl := range modelTables {
		allModels = append(allModels, g.GenerateModel(tbl, fieldOpts...))
	}
	g.ApplyBasic(allModels...)
	g.Execute()
	fmt.Println("  数据模型生成完成。")

	// ══════════════════════════════════════════════════════
	// 【第二步】生成 Repo / API / VO / DTO / Mapper
	//   单表模式：只对本次输入的表生成（其余已有文件自动跳过）
	//   全量模式：对所有表生成
	// ══════════════════════════════════════════════════════
	ensureDir(cfg.RepoPath)
	ensureDir(cfg.ApiPath)
	ensureDir(cfg.VoPath)
	ensureDir(cfg.DtoPath)
	ensureDir(cfg.MapperPath)

	var step2Tables []string
	if isSingleTable {
		step2Tables = []string{inputTable}
	} else {
		step2Tables = modelTables
	}

	fmt.Printf("\n【第二步】生成 Repo / API / VO / DTO / Mapper（共 %d 张表）...\n", len(step2Tables))
	for _, tbl := range step2Tables {
		fmt.Printf("\n─── 表: %s ───\n", tbl)
		generateForTable(tbl, cfg, db,
			repoGenTmplPath, repoTmplPath,
			apiTmplPath, voTmplPath, dtoTmplPath, mapperTmplPath)
	}

	fmt.Println("\n全部生成完成！")
	return nil
}
