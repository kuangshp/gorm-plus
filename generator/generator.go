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
	cfg.ProtoPath = resolve(cfg.ProtoPath)
	cfg.VoPath = resolve(cfg.VoPath)
	cfg.DtoPath = resolve(cfg.DtoPath)
	cfg.MapperPath = resolve(cfg.MapperPath)
	return nil
}

// Title 将字符串中每个单词的首字母转为大写,其余字母保持不变。
// 单词以 Unicode 空白字符分隔。是 strings.Title 的替代实现。
func Title(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevIsSep := true // 字符串开头视为分隔后
	for _, r := range s {
		if unicode.IsSpace(r) {
			b.WriteRune(r)
			prevIsSep = true
			continue
		}
		if prevIsSep {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteRune(r)
		}
		prevIsSep = false
	}
	return b.String()
}

// ═══════════════════════════════════════════════════════════
//  嵌入模板
// ═══════════════════════════════════════════════════════════

//go:embed template/api_template.txt
var embeddedApiTemplate string

//go:embed template/proto_template.txt
var embeddedProtoTemplate string

//go:embed template/base_proto_template.txt
var embeddedBaseProtoTemplate string

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

//go:embed template/entity_proto_mapper_template.txt
var embeddedEntityProtoMapperTemplate string

//go:embed template/api_proto_mapper_template.txt
var embeddedAPIProtoMapperTemplate string

var embeddedTemplates = map[string]string{
	"api_template.txt":                 embeddedApiTemplate,
	"proto_template.txt":               embeddedProtoTemplate,
	"base_proto_template.txt":          embeddedBaseProtoTemplate,
	"dto_template.txt":                 embeddedDtoTemplate,
	"base_api_template.txt":            embeddedBaseApiTemplate,
	"repository_gen_template.txt":      embeddedRepoGenTemplate,
	"repository_template.txt":          embeddedRepoTemplate,
	"vo_template.txt":                  embeddedVoTemplate,
	"mapper_template.txt":              embeddedMapperTemplate,
	"entity_proto_mapper_template.txt": embeddedEntityProtoMapperTemplate,
	"api_proto_mapper_template.txt":    embeddedAPIProtoMapperTemplate,
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

func normalizeTableName(table string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(table), "`"))
}

func buildExcludeTableSet(excludeTables []string) map[string]struct{} {
	excludeSet := make(map[string]struct{}, len(excludeTables))
	for _, table := range excludeTables {
		table = normalizeTableName(table)
		if table == "" {
			continue
		}
		excludeSet[table] = struct{}{}
	}
	return excludeSet
}

func filterExcludedTables(tables []string, excludeTables []string) []string {
	if len(tables) == 0 || len(excludeTables) == 0 {
		return tables
	}

	excludeSet := buildExcludeTableSet(excludeTables)
	if len(excludeSet) == 0 {
		return tables
	}

	filtered := make([]string, 0, len(tables))
	for _, table := range tables {
		if _, excluded := excludeSet[normalizeTableName(table)]; excluded {
			continue
		}
		filtered = append(filtered, table)
	}
	return filtered
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

var acronymRe = regexp.MustCompile(`([A-Z])([A-Z]+)([a-z]|$)`)

func normalizeAcronyms(s string) string {
	return acronymRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := acronymRe.FindStringSubmatch(m)
		// sub[1]=首大写, sub[2]=中间连续大写, sub[3]=后面的小写或空
		return sub[1] + strings.ToLower(sub[2]) + sub[3]
	})
}
func Case2Camel(name string) string {
	name = strings.Replace(name, "_", " ", -1)
	name = Title(name)
	acronyms := []string{"IP", "ID", "URL", "API", "IOS", "XML", "JSON", "JWT", "SQL", "ORM"}
	for _, acronym := range acronyms {
		name = strings.ReplaceAll(name, Title(strings.ToLower(acronym)), acronym)
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
	ParamName                        string
	JsonTag, JsonTagOpt              string
	CanNull, IsKey                   bool
	Extra, Comment, Validate         string
	IsTimeType, IsAuditField         bool
	IsDecimalType, IsFloatType       bool
	IsBytesType                      bool
}

type ApiTemplateData struct {
	TableName, ModelName, EntityName, TableComment string
	Columns, ModelColumns                          []ColumnInfo
}

type ProtoTemplateData struct {
	TableName, ModelName, EntityName, TableComment string
	ProtoPackage, BaseProtoImport                  string
	ModelColumns, WritableColumns                  []ColumnInfo
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
	PrimaryKeyType                    string
	PrimaryKeys                       []ColumnInfo
	HasCompositePrimaryKey            bool
	PrimaryKeyParamList               string
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

type ProtoMapperTemplateData struct {
	TableName, ModelName, ModelNameLower, TableComment string
	Package, ModelPkgPath, ModelPkgName                string
	ProtoPkgPath, ProtoPkgName                         string
	APITypesPkgPath                                    string
	HasTimeField, HasWritableTimeField                 bool
	HasDecimalField, HasFloatField                     bool
	Columns, WritableColumns                           []ColumnInfo
}

// ═══════════════════════════════════════════════════════════
//  模板加载 & 渲染
// ═══════════════════════════════════════════════════════════

func loadTemplate(templatePath string) (*template.Template, error) {
	templateName := filepath.Base(templatePath)
	funcMap := template.FuncMap{
		"lowerFirst": lowerFirst,
		"add":        func(a, b int) int { return a + b },
		"protoValidation": func(col ColumnInfo, required bool) string {
			return buildProtoValidationOptions(col, required)
		},
		"fieldComment": func(col ColumnInfo) string {
			if strings.TrimSpace(col.Comment) != "" {
				return col.Comment
			}
			return col.FieldName
		},
	}
	if fileContent, err := os.ReadFile(templatePath); err == nil {
		return template.New(templateName).Funcs(funcMap).Parse(string(fileContent))
	}
	embeddedContent, ok := embeddedTemplates[templateName]
	if !ok {
		return nil, fmt.Errorf("模板 %q 不存在且无内嵌版本", templatePath)
	}
	return template.New(templateName).Funcs(funcMap).Parse(embeddedContent)
}

func buildProtoValidationOptions(col ColumnInfo, required bool) string {
	var options []string
	if required {
		options = append(options, "(buf.validate.field).required = true")
	}
	sqlType := strings.ToLower(col.Type)
	if col.FieldType == "string" {
		if length, ok := parseSQLTypeLength(sqlType); ok {
			if strings.HasPrefix(sqlType, "char(") {
				options = append(options, fmt.Sprintf("(buf.validate.field).string.len = %d", length))
			} else if strings.HasPrefix(sqlType, "varchar(") {
				options = append(options, fmt.Sprintf("(buf.validate.field).string.max_len = %d", length))
			}
		}
		switch {
		case strings.Contains(sqlType, "datetime") || strings.Contains(sqlType, "timestamp"):
			options = append(options, "(buf.validate.field).string.(date_time_format) = true")
		case strings.Contains(sqlType, "date"):
			options = append(options, "(buf.validate.field).string.(date_format) = true")
		}
	}
	if isDecimalSQLType(col.Type) && col.FieldType == "double" {
		options = append(options, "(buf.validate.field).double.finite = true")
	}
	if enumValues := extractEnumValuesFromComment(col.Comment); len(enumValues) > 0 {
		ruleType := col.FieldType
		if ruleType == "double" {
			ruleType = "double"
		}
		options = append(options, fmt.Sprintf("(buf.validate.field).%s = {in: [%s]}", ruleType, strings.Join(enumValues, ", ")))
	}
	if len(options) == 0 {
		return ""
	}
	return " [\n    " + strings.Join(options, ",\n    ") + "\n  ]"
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

// getProtoType 将数据库字段类型转换为 proto3 标量类型。
// 时间类型统一使用 Unix 秒，API 层的字符串时间由 mapper 负责转换。
func getProtoType(sqlType string) string {
	s := strings.ToLower(sqlType)
	switch {
	case strings.Contains(s, "bool"):
		return "bool"
	case strings.Contains(s, "datetime"), strings.Contains(s, "timestamp"), strings.Contains(s, "date"):
		return "int64"
	case strings.Contains(s, "int"):
		return "int64"
	case strings.Contains(s, "decimal"), strings.Contains(s, "numeric"),
		strings.Contains(s, "float"), strings.Contains(s, "double"), strings.Contains(s, "real"):
		return "double"
	case strings.Contains(s, "binary"), strings.Contains(s, "blob"), strings.Contains(s, "bytea"):
		return "bytes"
	default:
		return "string"
	}
}

// getProtoRequestType 将数据库字段类型转换为 proto 请求字段类型。
// 时间由调用方以字符串传入，响应 Model 则通过 getProtoType 使用 Unix 秒。
func getProtoRequestType(sqlType string) string {
	if isTimeSQLType(sqlType) {
		return "string"
	}
	return getProtoType(sqlType)
}

func getProtoPackage(packageName string) string {
	pkg := strings.ToLower(filepath.Base(filepath.Clean(packageName)))
	pkg = regexp.MustCompile(`[^a-z0-9_]`).ReplaceAllString(pkg, "_")
	if pkg == "" || pkg == "." || (pkg[0] >= '0' && pkg[0] <= '9') {
		return "pb"
	}
	return pkg
}

func getGoTypeForApiDto(sqlType string) string {
	s := strings.ToLower(sqlType)
	if isTimeSQLType(s) {
		return "string"
	}
	if strings.Contains(s, "decimal") || strings.Contains(s, "float") || strings.Contains(s, "double") {
		return "string"
	}
	return getGoType(sqlType)
}

func isTimeSQLType(sqlType string) bool {
	s := strings.ToLower(sqlType)
	return strings.Contains(s, "datetime") || strings.Contains(s, "timestamp") || strings.Contains(s, "date")
}

func getGoTypeForVo(sqlType string) string { return getGoTypeForApiDto(sqlType) }

func isDecimalSQLType(sqlType string) bool {
	return strings.Contains(strings.ToLower(sqlType), "decimal")
}

func isIntegerSQLType(sqlType string) bool {
	return strings.Contains(strings.ToLower(sqlType), "int")
}

func isUnsignedSQLType(sqlType string) bool {
	return strings.Contains(strings.ToLower(sqlType), "unsigned")
}

func parseSQLTypeLength(sqlType string) (int, bool) {
	re := regexp.MustCompile(`\((\d+)`)
	m := re.FindStringSubmatch(sqlType)
	if len(m) < 2 {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

func hasRulePrefix(rules []string, prefix string) bool {
	for _, rule := range rules {
		if rule == prefix || strings.HasPrefix(rule, prefix+"=") {
			return true
		}
	}
	return false
}

func appendRule(rules []string, rule string) []string {
	if rule == "" {
		return rules
	}
	for _, item := range rules {
		if item == rule {
			return rules
		}
	}
	return append(rules, rule)
}

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

func extractEnumValuesFromComment(comment string) []string {
	re := regexp.MustCompile(`(?:^|[\s,，;；:：、])(\d+)\s*(?:是|[、.．:：=\-])`)
	seen := make(map[string]struct{})
	var vals []string
	for _, m := range re.FindAllStringSubmatch(comment, -1) {
		if len(m) < 2 {
			continue
		}
		if _, ok := seen[m[1]]; ok {
			continue
		}
		seen[m[1]] = struct{}{}
		vals = append(vals, m[1])
	}
	if len(vals) < 2 {
		return nil
	}
	return vals
}

func generateValidateRule(col ColumnInfo) string {
	var rules []string
	fieldType := col.FieldType
	if fieldType == "" {
		fieldType = getGoTypeForApiDto(col.Type)
	}
	colName := strings.ToLower(col.Name)
	sqlType := strings.ToLower(col.Type)
	if !col.CanNull {
		rules = appendRule(rules, "required")
	}
	if fieldType == "string" {
		switch {
		case strings.Contains(colName, "email"):
			rules = appendRule(rules, "email")
		case strings.Contains(colName, "mobile"):
			rules = appendRule(rules, "mobile")
		case strings.Contains(colName, "phone"):
			rules = appendRule(rules, "e164")
		case strings.Contains(colName, "uuid"):
			rules = appendRule(rules, "uuid")
		case strings.Contains(colName, "url"):
			rules = appendRule(rules, "url")
		case strings.Contains(colName, "uri"):
			rules = appendRule(rules, "uri")
		case strings.Contains(colName, "ipv4"):
			rules = appendRule(rules, "ipv4")
		case strings.Contains(colName, "ipv6"):
			rules = appendRule(rules, "ipv6")
		case colName == "ip" || strings.HasSuffix(colName, "_ip"):
			rules = appendRule(rules, "ip")
		case strings.Contains(colName, "latitude") || colName == "lat" || strings.HasSuffix(colName, "_lat"):
			rules = appendRule(rules, "latitude")
		case strings.Contains(colName, "longitude") || colName == "lng" || colName == "lon" ||
			strings.HasSuffix(colName, "_lng") || strings.HasSuffix(colName, "_lon"):
			rules = appendRule(rules, "longitude")
		}
	}
	if strings.HasPrefix(sqlType, "varchar(") {
		if length, ok := parseSQLTypeLength(sqlType); ok {
			rules = appendRule(rules, fmt.Sprintf("max=%d", length))
		}
	}
	if strings.HasPrefix(sqlType, "char(") {
		if length, ok := parseSQLTypeLength(sqlType); ok {
			rules = appendRule(rules, fmt.Sprintf("len=%d", length))
		}
	}
	if isDecimalSQLType(col.Type) {
		rules = appendRule(rules, "decimal")
	}
	if strings.Contains(sqlType, "json") {
		rules = appendRule(rules, "json")
	}
	enumVals := extractEnumValuesFromComment(col.Comment)
	if len(enumVals) > 0 {
		rules = appendRule(rules, "oneof="+strings.Join(enumVals, " "))
	}
	if isIntegerSQLType(col.Type) {
		if colName == "id" || strings.HasSuffix(colName, "_id") {
			rules = appendRule(rules, "number")
			if !hasRulePrefix(rules, "gte") {
				rules = appendRule(rules, "gte=1")
			}
		}
		if isUnsignedSQLType(col.Type) && !hasRulePrefix(rules, "gte") {
			rules = appendRule(rules, "gte=0")
		}
	}
	if len(enumVals) == 0 && !col.CanNull && fieldType == "int64" &&
		(strings.Contains(colName, "status") || strings.Contains(colName, "type") || strings.Contains(colName, "is_")) &&
		!hasRulePrefix(rules, "gte") {
		rules = appendRule(rules, "gte=1")
	}
	if col.CanNull && len(rules) > 0 {
		rules = append([]string{"omitempty"}, rules...)
	}
	return strings.Join(rules, ",")
}

func buildRepoData(columns []ColumnInfo, modelName, pkg, daoPath, modelPath, tableName string) RepositoryTemplateData {
	columnData := make([]ColumnInfo, len(columns))
	primaryKeyField, primaryKeyColumn, primaryKeyType := "ID", "id", "int64"
	primaryKeySelected := false
	primaryKeys := make([]ColumnInfo, 0, len(columns))
	for i, col := range columns {
		fn := Case2Camel(col.Name)
		fieldType := getGoType(col.Type)
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: fn, FieldType: fieldType,
			CanNull: col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
		}
		if col.IsKey && (!primaryKeySelected || strings.Contains(strings.ToLower(col.Extra), "auto_increment")) {
			primaryKeyField = fn
			primaryKeyColumn = col.Name
			primaryKeyType = fieldType
			primaryKeySelected = true
		}
		if col.IsKey {
			primaryKeys = append(primaryKeys, columnData[i])
		}
	}
	if len(primaryKeys) == 0 {
		primaryKeys = []ColumnInfo{{
			Name:      primaryKeyColumn,
			FieldName: primaryKeyField,
			FieldType: primaryKeyType,
			IsKey:     true,
		}}
	}
	for i := range primaryKeys {
		if len(primaryKeys) == 1 {
			primaryKeys[i].ParamName = LowerCamelCase(modelName) + "Id"
		} else {
			primaryKeys[i].ParamName = LowerCamelCase(primaryKeys[i].FieldName)
		}
	}
	return RepositoryTemplateData{
		ModelName: modelName, ModelNameLower: LowerCamelCase(modelName),
		EntityName: modelName + "Entity", EntityNameLower: LowerCamelCase(modelName + "Entity"),
		Package: pkg, DaoPath: pkg + "/" + daoPath, ModelPath: pkg + "/" + modelPath,
		ModelPkgName: getLastPathSegment(modelPath), Columns: columnData,
		PrimaryKeyField: primaryKeyField, PrimaryKeyColumn: primaryKeyColumn, PrimaryKeyType: primaryKeyType,
		PrimaryKeys:            primaryKeys,
		HasCompositePrimaryKey: len(primaryKeys) > 1,
		PrimaryKeyParamList:    buildPrimaryKeyParamList(primaryKeys),
		TableName:              tableName,
	}
}

func buildPrimaryKeyParamList(primaryKeys []ColumnInfo) string {
	params := make([]string, 0, len(primaryKeys))
	for _, key := range primaryKeys {
		params = append(params, fmt.Sprintf("%s %s", key.ParamName, key.FieldType))
	}
	return strings.Join(params, ", ")
}

func generateRepositoryFile(columns []ColumnInfo, modelName, pkg, daoPath, modelPath, tmplPath, tableName string) (string, error) {
	return renderTemplate(tmplPath, buildRepoData(columns, modelName, pkg, daoPath, modelPath, tableName))
}

func generateRepositoryExtFile(columns []ColumnInfo, modelName, pkg, daoPath, modelPath, tmplPath, tableName string) (string, error) {
	return renderTemplate(tmplPath, buildRepoData(columns, modelName, pkg, daoPath, modelPath, tableName))
}

func buildApiColumns(columns []ColumnInfo) []ColumnInfo {
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
				Type: col.Type, FieldType: getGoTypeForApiDto(col.Type),
				Name: col.Name, Extra: col.Extra, Comment: col.Comment,
			}),
		}
	}
	return columnData
}

func generateApiFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath string) (string, error) {
	columnData := buildApiColumns(columns)
	modelColumns := make([]ColumnInfo, len(columnData))
	copy(modelColumns, columnData)
	for i := range modelColumns {
		if isTimeSQLType(modelColumns[i].Type) {
			modelColumns[i].FieldType = "int64"
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
		ModelColumns: modelColumns,
	})
}

func generateProtoFile(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB, tmplPath, protoPackage, baseProtoImport string) (string, error) {
	modelColumns := make([]ColumnInfo, 0, len(columns))
	writableColumns := make([]ColumnInfo, 0, len(columns))
	for _, col := range columns {
		modelColumn := ColumnInfo{
			Name: col.Name, Type: col.Type, FieldName: Case2Camel(col.Name),
			ParamName: LowerCamelCase(Case2Camel(col.Name)),
			FieldType: getProtoType(col.Type), CanNull: col.CanNull, IsKey: col.IsKey,
			Extra: col.Extra, Comment: col.Comment,
		}
		if col.Name != "deleted_at" {
			modelColumns = append(modelColumns, modelColumn)
		}
		switch col.Name {
		case "id", "created_at", "updated_at", "deleted_at", "created_by", "updated_by":
		default:
			requestColumn := modelColumn
			requestColumn.FieldType = getProtoRequestType(col.Type)
			writableColumns = append(writableColumns, requestColumn)
		}
	}
	tableComment := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}
	return renderTemplate(tmplPath, ProtoTemplateData{
		TableName: tableName, ModelName: modelName,
		EntityName: modelName + "Entity", TableComment: tableComment,
		ProtoPackage: protoPackage, BaseProtoImport: baseProtoImport,
		ModelColumns: modelColumns, WritableColumns: writableColumns,
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

func buildProtoMapperData(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB,
	pkg, modelPkgPath, protoPkgPath, apiTypesPkgPath string) ProtoMapperTemplateData {
	columnData := make([]ColumnInfo, 0, len(columns))
	writableColumns := make([]ColumnInfo, 0, len(columns))
	hasTime, hasWritableTime, hasDecimal, hasFloat := false, false, false, false
	for _, col := range columns {
		s := strings.ToLower(col.Type)
		isTime := strings.Contains(s, "datetime") || strings.Contains(s, "timestamp") || strings.Contains(s, "date")
		isDecimal := strings.Contains(s, "decimal") || strings.Contains(s, "numeric")
		isFloat := strings.Contains(s, "float") || strings.Contains(s, "double") || strings.Contains(s, "real")
		isBytes := strings.Contains(s, "binary") || strings.Contains(s, "blob") || strings.Contains(s, "bytea")
		hasTime = hasTime || isTime
		hasDecimal = hasDecimal || isDecimal
		hasFloat = hasFloat || isFloat
		item := ColumnInfo{
			Name: col.Name, Type: col.Type, FieldName: Case2Camel(col.Name),
			ParamName:  normalizeAcronyms(Case2Camel(col.Name)),
			IsTimeType: isTime, IsDecimalType: isDecimal, IsFloatType: isFloat, IsBytesType: isBytes,
			CanNull: col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
		}
		if col.Name != "deleted_at" {
			columnData = append(columnData, item)
		}
		switch col.Name {
		case "id", "created_at", "updated_at", "deleted_at", "created_by", "updated_by":
		default:
			writableColumns = append(writableColumns, item)
			hasWritableTime = hasWritableTime || isTime
		}
	}
	tableComment := getTableComment(db, tableName)
	if tableComment == "" {
		tableComment = modelName
	}
	return ProtoMapperTemplateData{
		TableName: tableName, ModelName: modelName, ModelNameLower: LowerCamelCase(modelName),
		TableComment: tableComment, Package: pkg,
		ModelPkgPath: modelPkgPath, ModelPkgName: getLastPathSegment(modelPkgPath),
		ProtoPkgPath: protoPkgPath, ProtoPkgName: getLastPathSegment(protoPkgPath),
		APITypesPkgPath: apiTypesPkgPath, HasTimeField: hasTime, HasWritableTimeField: hasWritableTime,
		HasDecimalField: hasDecimal, HasFloatField: hasFloat,
		Columns: columnData, WritableColumns: writableColumns,
	}
}

// ═══════════════════════════════════════════════════════════
//  单表文件生成入口
// ═══════════════════════════════════════════════════════════

func generateForTable(tbl string, cfg *Config, db *gorm.DB,
	repoGenTmplPath, repoTmplPath, apiTmplPath, protoTmplPath, voTmplPath, dtoTmplPath,
	mapperTmplPath, entityProtoMapperTmplPath, apiProtoMapperTmplPath string) {

	columns, err := getTableColumns(db, tbl)
	if err != nil {
		fmt.Printf("[%s] 获取表结构失败，跳过: %v\n", tbl, err)
		return
	}
	modelName := Case2Camel(strings.ToUpper(tbl[:1]) + tbl[1:])

	// Proto .proto（go-zero RPC 描述文件，已存在则跳过）
	if cfg.ProtoPath != "" {
		protoPackage := getProtoPackage(cfg.Package)
		baseProtoImport := filepath.Join(filepath.Base(filepath.Clean(cfg.ProtoPath)), "base.proto")
		baseProtoFile := filepath.Join(cfg.ProtoPath, "base.proto")
		if _, err := os.Stat(baseProtoFile); os.IsNotExist(err) {
			if content, err := renderTemplate(
				filepath.Join(filepath.Dir(protoTmplPath), "base_proto_template.txt"),
				ProtoTemplateData{ProtoPackage: protoPackage},
			); err != nil {
				fmt.Printf("渲染 base.proto 失败: %v\n", err)
			} else {
				if err := os.WriteFile(baseProtoFile, []byte(content), 0644); err != nil {
					fmt.Printf("写入 base.proto 失败: %v\n", err)
				} else {
					fmt.Printf("已生成: %s\n", baseProtoFile)
				}
			}
		}
		if content, err := generateProtoFile(tbl, columns, modelName, db, protoTmplPath, protoPackage, baseProtoImport); err != nil {
			fmt.Printf("[%s] 生成 proto 失败: %v\n", tbl, err)
		} else {
			writeFileIfNotExist(
				filepath.Join(cfg.ProtoPath, LowerCamelCase(Case2Camel(tbl))+".proto"),
				content, "proto",
			)
		}
	}
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

	// Mapper（已存在则跳过）。没有 ProtoPath 时保持原来的 DTO/VO ↔ Entity mapper。
	if cfg.MapperPath != "" {
		if cfg.ProtoPath == "" {
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
		} else {
			entityProtoMapperPath := filepath.Join(cfg.MapperPath, "entityproto")
			apiProtoMapperPath := filepath.Join(cfg.MapperPath, "apiproto")
			ensureDir(entityProtoMapperPath)
			if cfg.ApiPath != "" {
				ensureDir(apiProtoMapperPath)
			}
			protoPkgPath := filepath.Join(pathToPkg(cfg.ProtoPath), getProtoPackage(cfg.Package))
			apiTypesPkgPath := ""
			if cfg.ApiPath != "" {
				apiTypesPkgPath = pathToPkg(filepath.Join(filepath.Dir(cfg.ApiPath), "internal", "types"))
			}
			data := buildProtoMapperData(tbl, columns, modelName, db,
				cfg.Package, pathToPkg(cfg.ModelPkgPath), protoPkgPath, apiTypesPkgPath)
			if content, err := renderTemplate(entityProtoMapperTmplPath, data); err != nil {
				fmt.Printf("[%s] 生成 Entity/Proto mapper 失败: %v\n", tbl, err)
			} else {
				writeFileIfNotExist(
					fmt.Sprintf("%s/%sMapper.go", entityProtoMapperPath, LowerCamelCase(Case2Camel(tbl))),
					content, "Entity/Proto mapper",
				)
			}
			if cfg.ApiPath != "" {
				if content, err := renderTemplate(apiProtoMapperTmplPath, data); err != nil {
					fmt.Printf("[%s] 生成 API/Proto mapper 失败: %v\n", tbl, err)
				} else {
					writeFileIfNotExist(
						fmt.Sprintf("%s/%sMapper.go", apiProtoMapperPath, LowerCamelCase(Case2Camel(tbl))),
						content, "API/Proto mapper",
					)
				}
			}
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

	// 按 db_type 自动选择驱动(mysql / postgres / sqlite / sqlserver)
	db, err := OpenDB(cfg)
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
	protoTmplPath := filepath.Join(templateDir, "proto_template.txt")
	dtoTmplPath := filepath.Join(templateDir, "dto_template.txt")
	repoGenTmplPath := filepath.Join(templateDir, "repository_gen_template.txt")
	repoTmplPath := filepath.Join(templateDir, "repository_template.txt")
	voTmplPath := filepath.Join(templateDir, "vo_template.txt")
	mapperTmplPath := filepath.Join(templateDir, "mapper_template.txt")
	entityProtoMapperTmplPath := filepath.Join(templateDir, "entity_proto_mapper_template.txt")
	apiProtoMapperTmplPath := filepath.Join(templateDir, "api_proto_mapper_template.txt")

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
			//return LowerCamelCase(Case2Camel(col))
			s := Case2Camel(col)
			s = normalizeAcronyms(s)
			return strings.ToLower(s[:1]) + s[1:]
		}),
		gen.FieldGORMTag("updated_at", func(_ field.GormTag) field.GormTag {
			return map[string][]string{"column": {"updated_at"}, "comment": {"更新时间"}}
		}),
		gen.FieldGORMTag("created_at", func(_ field.GormTag) field.GormTag {
			return map[string][]string{"column": {"created_at"}, "comment": {"创建时间"}}
		}),
		gen.FieldType("deleted_at", "gorm.DeletedAt"),
		// 乐观锁
		gen.FieldType("version", "optimisticlock.Version"),
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

	if len(cfg.ExcludeTables) > 0 {
		before := len(modelTables)
		modelTables = filterExcludedTables(modelTables, cfg.ExcludeTables)
		fmt.Printf("  已排除 %d 张表: %v\n", before-len(modelTables), cfg.ExcludeTables)
	}
	if len(modelTables) == 0 {
		fmt.Println("  没有需要生成的表。")
		return nil
	}

	// ③ 统一生成（含所有历史表 + 新表）
	allModels := make([]interface{}, 0, len(modelTables))
	for _, tbl := range modelTables {
		tableFieldOpts := append([]gen.ModelOpt{}, fieldOpts...)
		tableFieldOpts = append(tableFieldOpts, sensitiveModelOpts(tbl, cfg.SensitiveFields)...)
		allModels = append(allModels, g.GenerateModel(tbl, tableFieldOpts...))
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
	ensureDir(cfg.ProtoPath)
	ensureDir(cfg.VoPath)
	ensureDir(cfg.DtoPath)
	ensureDir(cfg.MapperPath)

	var step2Tables []string
	if isSingleTable {
		step2Tables = []string{inputTable}
	} else {
		step2Tables = modelTables
	}
	step2Tables = filterExcludedTables(step2Tables, cfg.ExcludeTables)

	fmt.Printf("\n【第二步】生成 Repo / API / Proto / VO / DTO / Mapper（共 %d 张表）...\n", len(step2Tables))
	for _, tbl := range step2Tables {
		fmt.Printf("\n─── 表: %s ───\n", tbl)
		generateForTable(tbl, cfg, db,
			repoGenTmplPath, repoTmplPath,
			apiTmplPath, protoTmplPath, voTmplPath, dtoTmplPath,
			mapperTmplPath, entityProtoMapperTmplPath, apiProtoMapperTmplPath)
	}

	fmt.Println("\n全部生成完成！")
	return nil
}

func sensitiveModelOpts(table string, configs []SensitiveFieldConfig) []gen.ModelOpt {
	var opts []gen.ModelOpt
	for _, cfg := range configs {
		if !strings.EqualFold(strings.TrimSpace(cfg.Table), strings.TrimSpace(table)) || strings.TrimSpace(cfg.Field) == "" {
			continue
		}
		logicalColumn := strings.TrimSpace(cfg.Field)
		logicalName := Case2Camel(logicalColumn)
		cipherColumn := strings.TrimSpace(cfg.CipherField)
		if cipherColumn == "" {
			cipherColumn = logicalColumn + "_cipher"
		}
		indexColumn := strings.TrimSpace(cfg.IndexField)
		if indexColumn == "" {
			indexColumn = logicalColumn + "_index"
		}
		sensitiveType := strings.TrimSpace(cfg.Type)
		if sensitiveType == "" {
			sensitiveType = "phone"
		}
		tagValue := sensitiveFieldTagValue(sensitiveType, cipherColumn, indexColumn)
		opts = append(opts, gen.FieldNew(logicalName, "string", field.Tag{
			"gorm":     "-",
			"json":     LowerCamelCase(logicalName),
			"gormplus": tagValue,
		}))
	}
	return opts
}

func sensitiveFieldTagValue(sensitiveType, cipherColumn, indexColumn string) string {
	return fmt.Sprintf("type:%s;cipher:%s;index:%s", sensitiveType, cipherColumn, indexColumn)
}
