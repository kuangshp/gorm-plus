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

	"gorm.io/driver/mysql"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// 将模板文件嵌入二进制，无论在哪个目录执行都可以正常访问
//
//go:embed template/api_template.txt
var embeddedApiTemplate string

//go:embed template/dto_template.txt
var embeddedDtoTemplate string

//go:embed template/repository_gen_template.txt
var embeddedRepoGenTemplate string

//go:embed template/repository_template.txt
var embeddedRepoTemplate string

//go:embed template/vo_template.txt
var embeddedVoTemplate string

// embeddedTemplates 内嵌模板映射表，key 为模板文件名
var embeddedTemplates = map[string]string{
	"api_template.txt":            embeddedApiTemplate,
	"dto_template.txt":            embeddedDtoTemplate,
	"repository_gen_template.txt": embeddedRepoGenTemplate,
	"repository_template.txt":     embeddedRepoTemplate,
	"vo_template.txt":             embeddedVoTemplate,
}

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
	acronyms := []string{"IP", "ID", "URL", "API", "IOS", "API", "XML", "JSON", "JWT", "SQL", "ORM"}
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
		prefix := name[:len(name)-2]
		return LowerCamelCase(prefix) + "Id"
	}
	name = Case2Camel(name)
	return strings.ToLower(name[:1]) + name[1:]
}

func lowerFirst(name string) string {
	return LowerCamelCase(name)
}

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
			allInput := string(data)
			inputLines = strings.Split(allInput, "\n")
			for i, line := range inputLines {
				inputLines[i] = strings.TrimSpace(line)
			}
		}
	}
}

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
	Name       string
	Type       string
	FieldName  string
	FieldType  string
	JsonTag    string
	JsonTagOpt string
	CanNull    bool
	IsKey      bool
	Extra      string
	Comment    string
	Validate   string
}

type ApiTemplateData struct {
	TableName    string
	ModelName    string
	EntityName   string
	TableComment string
	Columns      []ColumnInfo
}

type VoTemplateData struct {
	TableName    string
	ModelName    string
	TableComment string
	Columns      []ColumnInfo
}

type RepositoryTemplateData struct {
	ModelName       string
	ModelNameLower  string
	EntityName      string
	EntityNameLower string
	Package         string
	DaoPath         string
	ModelPath       string
	ModelPkgName    string
	Columns         []ColumnInfo
}

func getTableComment(db *gorm.DB, tableName string) (string, error) {
	var comment string
	err := db.Raw(fmt.Sprintf("SELECT TABLE_COMMENT FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '%s'", tableName)).Scan(&comment).Error
	return comment, err
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
		enumRegex := regexp.MustCompile(`(\d+)是([^，,]+)[,，]?`)
		matches := enumRegex.FindAllStringSubmatch(col.Comment, -1)
		if len(matches) > 0 {
			values := make([]string, 0, len(matches))
			for _, match := range matches {
				if len(match) >= 3 {
					values = append(values, match[1])
				}
			}
			if len(values) > 0 {
				rules = append(rules, fmt.Sprintf("oneof=%s", strings.Join(values, " ")))
			}
		}
	}
	if !col.CanNull && col.FieldType == "int64" && (strings.Contains(col.Name, "status") || strings.Contains(col.Name, "type") || strings.Contains(col.Name, "is_")) {
		rules = append(rules, "gte=1")
	}
	return strings.Join(rules, ",")
}

func loadTemplate(templatePath string) (*template.Template, error) {
	templateName := filepath.Base(templatePath)
	funcMap := template.FuncMap{
		"lowerFirst": lowerFirst,
	}

	fileContent, err := os.ReadFile(templatePath)
	if err == nil {
		return template.New(templateName).Funcs(funcMap).Parse(string(fileContent))
	}

	embeddedContent, ok := embeddedTemplates[templateName]
	if !ok {
		return nil, fmt.Errorf("模板文件 %q 不存在，且没有对应的内嵌模板: %w", templatePath, err)
	}
	return template.New(templateName).Funcs(funcMap).Parse(embeddedContent)
}

func generateApiFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", fmt.Errorf("加载模板失败: %w", err)
	}

	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		jsonTagOpt := ""
		if col.CanNull {
			jsonTagOpt = ",optional"
		}
		columnData[i] = ColumnInfo{
			Name:       col.Name,
			Type:       col.Type,
			FieldName:  Case2Camel(col.Name),
			FieldType:  getGoType(col.Type),
			JsonTag:    LowerCamelCase(col.Name),
			JsonTagOpt: jsonTagOpt,
			CanNull:    col.CanNull,
			IsKey:      col.IsKey,
			Extra:      col.Extra,
			Comment:    col.Comment,
			Validate:   generateValidateRule(ColumnInfo{CanNull: col.CanNull, IsKey: col.IsKey, FieldType: getGoType(col.Type), Name: col.Name, Comment: col.Comment}),
		}
	}

	tableComment, _ := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}

	data := ApiTemplateData{
		TableName:    tableName,
		ModelName:    modelName,
		EntityName:   Case2Camel(strings.ToUpper(tableName[:1]+tableName[1:])) + "Entity",
		TableComment: tableComment,
		Columns:      columnData,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("渲染模板失败: %w", err)
	}

	return buf.String(), nil
}

func generateRepositoryFile(columns []ColumnInfo, modelName string, pkg string, daoPath string, modelPath string, tmplPath string) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", fmt.Errorf("加载模板失败: %w", err)
	}

	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name:      col.Name,
			Type:      col.Type,
			FieldName: Case2Camel(col.Name),
			FieldType: getGoType(col.Type),
			CanNull:   col.CanNull,
			IsKey:     col.IsKey,
			Comment:   col.Comment,
		}
	}

	data := RepositoryTemplateData{
		ModelName:       modelName,
		ModelNameLower:  LowerCamelCase(modelName),
		EntityName:      modelName + "Entity",
		EntityNameLower: LowerCamelCase(modelName + "Entity"),
		Package:         pkg,
		DaoPath:         pkg + "/" + daoPath,
		ModelPath:       pkg + "/" + modelPath,
		ModelPkgName:    getLastPathSegment(modelPath),
		Columns:         columnData,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("渲染模板失败: %w", err)
	}

	return buf.String(), nil
}

func generateRepositoryInterfaceFile(columns []ColumnInfo, modelName string, pkg string, daoPath string, modelPath string, tmplPath string) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", fmt.Errorf("加载接口模板失败: %w", err)
	}

	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name:      col.Name,
			Type:      col.Type,
			FieldName: Case2Camel(col.Name),
			FieldType: getGoType(col.Type),
			CanNull:   col.CanNull,
			IsKey:     col.IsKey,
			Comment:   col.Comment,
		}
	}

	data := RepositoryTemplateData{
		ModelName:       modelName,
		ModelNameLower:  LowerCamelCase(modelName),
		EntityName:      modelName + "Entity",
		EntityNameLower: LowerCamelCase(modelName + "Entity"),
		Package:         pkg,
		DaoPath:         pkg + "/" + daoPath,
		ModelPath:       pkg + "/" + modelPath,
		ModelPkgName:    getLastPathSegment(modelPath),
		Columns:         columnData,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("渲染接口模板失败: %w", err)
	}

	return buf.String(), nil
}

func generateRepositoryExtFile(columns []ColumnInfo, modelName string, pkg string, daoPath string, modelPath string, tmplPath string) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", fmt.Errorf("加载扩展模板失败: %w", err)
	}

	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name:      col.Name,
			Type:      col.Type,
			FieldName: Case2Camel(col.Name),
			FieldType: getGoType(col.Type),
			CanNull:   col.CanNull,
			IsKey:     col.IsKey,
			Comment:   col.Comment,
		}
	}

	data := RepositoryTemplateData{
		ModelName:       modelName,
		ModelNameLower:  LowerCamelCase(modelName),
		EntityName:      modelName + "Entity",
		EntityNameLower: LowerCamelCase(modelName + "Entity"),
		Package:         pkg,
		DaoPath:         pkg + "/" + daoPath,
		ModelPath:       pkg + "/" + modelPath,
		ModelPkgName:    getLastPathSegment(modelPath),
		Columns:         columnData,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("渲染扩展模板失败: %w", err)
	}

	return buf.String(), nil
}

func generateVoFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", fmt.Errorf("加载模板失败: %w", err)
	}

	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name:      col.Name,
			Type:      col.Type,
			FieldName: Case2Camel(col.Name),
			FieldType: getGoType(col.Type),
			CanNull:   col.CanNull,
			IsKey:     col.IsKey,
			Comment:   col.Comment,
		}
	}

	tableComment, _ := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}

	data := VoTemplateData{
		TableName:    tableName,
		ModelName:    modelName,
		TableComment: tableComment,
		Columns:      columnData,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("渲染模板失败: %w", err)
	}

	return buf.String(), nil
}

func generateDtoFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	tmpl, err := loadTemplate(tmplPath)
	if err != nil {
		return "", fmt.Errorf("加载模板失败: %w", err)
	}

	columnData := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		columnData[i] = ColumnInfo{
			Name:      col.Name,
			Type:      col.Type,
			FieldName: Case2Camel(col.Name),
			FieldType: getGoType(col.Type),
			CanNull:   col.CanNull,
			IsKey:     col.IsKey,
			Comment:   col.Comment,
			Validate:  generateValidateRule(col),
		}
	}

	tableComment, _ := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}

	data := VoTemplateData{
		TableName:    tableName,
		ModelName:    modelName,
		TableComment: tableComment,
		Columns:      columnData,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("渲染模板失败: %w", err)
	}

	return buf.String(), nil
}

func getGoType(sqlType string) string {
	sqlType = strings.ToLower(sqlType)
	if strings.Contains(sqlType, "varchar") || strings.Contains(sqlType, "text") || strings.Contains(sqlType, "char") {
		return "string"
	}
	if strings.Contains(sqlType, "int") {
		return "int64"
	}
	if strings.Contains(sqlType, "decimal") || strings.Contains(sqlType, "float") || strings.Contains(sqlType, "double") {
		return "float64"
	}
	if strings.Contains(sqlType, "datetime") || strings.Contains(sqlType, "timestamp") {
		return "int64"
	}
	if strings.Contains(sqlType, "date") {
		return "int64"
	}
	if strings.Contains(sqlType, "json") {
		return "string"
	}
	if strings.Contains(sqlType, "bool") {
		return "bool"
	}
	return "string"
}

// pathToPkg 将路径转换为包路径，如 "./query/dao" -> "query/dao"
func pathToPkg(path string) string {
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimSuffix(path, "/")
	return path
}

// getLastPathSegment 获取路径的最后一个段，如 "dal/model/entity" -> "entity"
func getLastPathSegment(path string) string {
	path = strings.TrimPrefix(path, "./")
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// ensureDir 确保目录存在，不存在则创建
func ensureDir(dir string) {
	if dir != "" {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			os.MkdirAll(dir, 0755)
		}
	}
}

// writeFileIfNotExist 文件不存在时写入，已存在时打印跳过信息
func writeFileIfNotExist(filePath string, content string, label string) {
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

// generateForTable 为单张表生成 Repo / API / VO / DTO 文件
func generateForTable(tbl string, cfg *Config, db *gorm.DB,
	repoGenTmplPath, repoTmplPath, apiTmplPath, voTmplPath, dtoTmplPath string) {

	columns, err := getTableColumns(db, tbl)
	if err != nil {
		fmt.Printf("[%s] 获取表结构失败，跳过: %v\n", tbl, err)
		return
	}
	modelName := Case2Camel(strings.ToUpper(tbl[:1]) + tbl[1:])

	// ── Repository _gen.go（已存在跳过）──────────────────────────
	if cfg.RepoPath != "" {
		content, err := generateRepositoryFile(columns, modelName, cfg.Package,
			pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoGenTmplPath)
		if err != nil {
			fmt.Printf("[%s] 生成 repository_gen 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%s_gen.go", cfg.RepoPath, strings.ToLower(modelName)),
				content, "repository_gen",
			)
		}

		// ── Repository .go（已存在跳过）──────────────────────────
		extContent, err := generateRepositoryExtFile(columns, modelName, cfg.Package,
			pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoTmplPath)
		if err != nil {
			fmt.Printf("[%s] 生成 repository 扩展失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%s.go", cfg.RepoPath, strings.ToLower(modelName)),
				extContent, "repository",
			)
		}
	}

	// ── API .api（已存在跳过）────────────────────────────────────
	if cfg.ApiPath != "" {
		apiContent, err := generateApiFile(tbl, columns, modelName, db, apiTmplPath)
		if err != nil {
			fmt.Printf("[%s] 生成 api 失败: %v\n", tbl, err)
		} else {
			apiFileName := fmt.Sprintf("%s/%s.api", cfg.ApiPath, tbl)
			if _, err := os.Stat(apiFileName); os.IsNotExist(err) {
				if err = os.WriteFile(apiFileName, []byte(apiContent), 0644); err != nil {
					fmt.Printf("[%s] 写入 api 文件失败: %v\n", tbl, err)
				} else {
					fmt.Printf("已生成: %s\n", apiFileName)
					// 调用 goctl 生成 go-zero 代码
					goctlPath := getGoctlPath()
					cmd := exec.Command(goctlPath, "api", "go", "-api", apiFileName,
						"--dir", filepath.Dir(cfg.ApiPath), "--style=goZero")
					cmd.Dir = "."
					if output, err := cmd.CombinedOutput(); err != nil {
						fmt.Printf("[%s] 执行 goctl 失败: %v\n%s\n", tbl, err, output)
					} else {
						fmt.Printf("[%s] go-zero 代码生成成功\n", tbl)
					}
				}
			} else {
				fmt.Printf("已存在，跳过: %s\n", apiFileName)
			}
		}
	}

	// ── VO（已存在跳过）──────────────────────────────────────────
	if cfg.VoPath != "" {
		voContent, err := generateVoFile(tbl, columns, modelName, db, voTmplPath)
		if err != nil {
			fmt.Printf("[%s] 生成 VO 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%sVo.go", cfg.VoPath, strings.ToLower(modelName)),
				voContent, "VO",
			)
		}
	}

	// ── DTO（已存在跳过）─────────────────────────────────────────
	if cfg.DtoPath != "" {
		dtoContent, err := generateDtoFile(tbl, columns, modelName, db, dtoTmplPath)
		if err != nil {
			fmt.Printf("[%s] 生成 DTO 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				fmt.Sprintf("%s/%sDto.go", cfg.DtoPath, strings.ToLower(modelName)),
				dtoContent, "DTO",
			)
		}
	}
}

// Generate 代码生成器主函数
func Generate(cfg *Config) error {
	dsn := fmt.Sprintf("%s:%s@(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)

	db, err := gorm.Open(mysql.Open(dsn))
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}

	tableName := readInput("请输入表名（直接回车生成所有表）: ")

	// 模板路径解析（优先文件系统，回退内嵌）
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
		if templateDir == "" {
			cwd, _ := os.Getwd()
			templateDir = filepath.Join(cwd, "generator", "template")
		}
	}
	apiTmplPath := filepath.Join(templateDir, "api_template.txt")
	dtoTmplPath := filepath.Join(templateDir, "dto_template.txt")
	repoGenTmplPath := filepath.Join(templateDir, "repository_gen_template.txt")
	repoTmplPath := filepath.Join(templateDir, "repository_template.txt")
	voTmplPath := filepath.Join(templateDir, "vo_template.txt")

	// 初始化 gorm-gen 生成器
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

	dataMap := map[string]func(detailType gorm.ColumnType) (dataType string){
		"int":       func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"int2":      func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"int4":      func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"mediumint": func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"smallint":  func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"integer":   func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"tinyint":   func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"bigint":    func(detailType gorm.ColumnType) (dataType string) { return "int64" },
		"json":      func(detailType gorm.ColumnType) (dataType string) { return "datatypes.JSON" },
		"decimal":   func(detailType gorm.ColumnType) (dataType string) { return "decimal.Decimal" },
	}
	g.WithDataTypeMap(dataMap)

	g.WithModelNameStrategy(func(tableName string) (modelName string) {
		return Case2Camel(strings.ToUpper(tableName[:1]) + tableName[1:] + "Entity")
	})

	jsonField := gen.FieldJSONTagWithNS(func(columnName string) (tagContent string) {
		if strings.Contains(`deleted_at`, columnName) {
			return "-"
		}
		return LowerCamelCase(columnName)
	})
	autoUpdateTimeField := gen.FieldGORMTag("updated_at", func(tag field.GormTag) field.GormTag {
		return map[string][]string{"column": {"updated_at"}, "comment": {"更新时间"}}
	})
	autoCreateTimeField := gen.FieldGORMTag("created_at", func(tag field.GormTag) field.GormTag {
		return map[string][]string{"column": {"created_at"}, "comment": {"创建时间"}}
	})
	softDeleteField := gen.FieldType("deleted_at", "gorm.DeletedAt")
	fieldOpts := []gen.ModelOpt{jsonField, autoCreateTimeField, autoUpdateTimeField, softDeleteField}

	// ── 确定要处理的表名列表 ──────────────────────────────────────
	var tableNames []string
	if tableName != "" {
		// 指定了表名：只处理这一张表
		tableNames = []string{tableName}
	} else {
		// 未指定表名：获取数据库所有表
		var tables []string
		if err := db.Raw("SHOW TABLES").Scan(&tables).Error; err != nil {
			return fmt.Errorf("获取表列表失败: %w", err)
		}
		if len(tables) == 0 {
			return fmt.Errorf("数据库 %s 中没有找到任何表", cfg.Database)
		}
		tableNames = tables
		fmt.Printf("共找到 %d 张表，开始生成所有表...\n", len(tableNames))
	}

	// ── 生成数据模型（始终覆盖）──────────────────────────────────
	fmt.Println("\n【第一步】生成数据模型（Model）...")
	if len(tableNames) == 1 {
		model := g.GenerateModel(tableNames[0], fieldOpts...)
		g.ApplyBasic(model)
	} else {
		allModel := g.GenerateAllTable(fieldOpts...)
		g.ApplyBasic(allModel...)
	}
	g.Execute()
	fmt.Println("数据模型生成完成。")

	// ── 创建输出目录 ──────────────────────────────────────────────
	ensureDir(cfg.RepoPath)
	ensureDir(cfg.ApiPath)
	ensureDir(cfg.VoPath)
	ensureDir(cfg.DtoPath)

	// ── 逐张表生成 Repo / API / VO / DTO（已存在的文件跳过）──────
	fmt.Printf("\n【第二步】生成 Repo / API / VO / DTO...\n")
	for _, tbl := range tableNames {
		fmt.Printf("\n─── 表: %s ───\n", tbl)
		generateForTable(tbl, cfg, db,
			repoGenTmplPath, repoTmplPath, apiTmplPath, voTmplPath, dtoTmplPath)
	}

	fmt.Println("\n全部生成完成！")
	return nil
}
