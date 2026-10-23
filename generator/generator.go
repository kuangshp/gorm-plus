package generator

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	gozerogen "github.com/kuangshp/gorm-plus/generator/gozero"
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
	cfg.APIMapperPath = resolve(cfg.APIMapperPath)
	cfg.ProtoMapperPath = resolve(cfg.ProtoMapperPath)
	if cfg.MapperPath != "" {
		if cfg.ProtoPath == "" {
			if cfg.APIMapperPath == "" {
				cfg.APIMapperPath = cfg.MapperPath
			}
		} else {
			if cfg.ProtoMapperPath == "" {
				cfg.ProtoMapperPath = filepath.Join(cfg.MapperPath, "entityproto")
			}
			if cfg.ApiPath != "" && cfg.APIMapperPath == "" {
				cfg.APIMapperPath = filepath.Join(cfg.MapperPath, "apiproto")
			}
		}
	}
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
	"enum_template.txt":                embeddedEnumTemplate,
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

type ColumnInfo struct {
	Name, Type, FieldName, FieldType string
	ParamName                        string
	JsonTag, JsonTagOpt              string
	CanNull, IsKey                   bool
	Extra, Comment, Validate         string
	IsTimeType, IsAuditField         bool
	IsDecimalType, IsFloatType       bool
	IsBytesType, IsJSONType          bool
	EnumValues                       []string // 数据库原生 ENUM 的有序合法值；普通字段为空。
	EnumTypeName                     string   // 实体同包的表名、字段名及 Enum 组成的类型名称。
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
	TableName, ModelName, ModelNameLower, TableComment         string
	Package, ModelPkgPath, ModelPkgName                        string
	DtoPkgPath, DtoPkgName, DtoStructName                      string
	VoPkgPath, VoPkgName, VoStructName                         string
	SameDtoPkg, IsGoZero                                       bool
	HasTimeField, HasDecimalField, HasFloatField, HasJSONField bool
	Columns                                                    []ColumnInfo
}

type ProtoMapperTemplateData struct {
	TableName, ModelName, ModelNameLower, TableComment string
	Package, ModelPkgPath, ModelPkgName                string
	ProtoPkgPath, ProtoPkgName                         string
	APITypesPkgPath                                    string
	HasTimeField, HasWritableTimeField                 bool
	HasDecimalField, HasFloatField, HasJSONField       bool
	Columns, WritableColumns                           []ColumnInfo
}

// ═══════════════════════════════════════════════════════════
//  模板加载 & 渲染
// ═══════════════════════════════════════════════════════════

func loadTemplate(templatePath string) (*template.Template, error) {
	templateName := filepath.Base(templatePath)
	funcMap := template.FuncMap{
		"quoteEnum": quoteEnumValue,
		"hasJSON": func(columns []ColumnInfo) bool { // 按字段决定 JSON 类型导入，兼容自定义模板数据。
			for _, column := range columns {
				if column.IsJSONType {
					return true
				}
			}
			return false
		},
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
	if len(col.EnumValues) > 0 {
		quoted := make([]string, len(col.EnumValues))
		for i, value := range col.EnumValues {
			quoted[i] = quoteEnumValue(value)
		}
		options = append(options, fmt.Sprintf("(buf.validate.field).string = {in: [%s]}", strings.Join(quoted, ", ")))
	} else if enumValues := extractEnumValuesFromComment(col.Comment); len(enumValues) > 0 {
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
	case isDecimalInputSQLType(s):
		return "float64"
	case isTimeSQLType(s):
		return "int64"
	case strings.Contains(s, "binary"), strings.Contains(s, "blob"), strings.Contains(s, "bytea"):
		return "[]byte"
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
	case isTimeSQLType(s):
		return "int64"
	case strings.Contains(s, "int"):
		return "int64"
	case isDecimalInputSQLType(s):
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

func getGoTypeForApiDto(sqlType string) string { // DTO 和 API 的 JSON 字段使用字符串，时间与小数使用字符串入参。
	s := strings.ToLower(sqlType)
	if isTimeSQLType(s) {
		return "string"
	}
	if isDecimalInputSQLType(s) {
		return "string"
	}
	return getGoType(sqlType)
}

// isDecimalInputSQLType identifies SQL fields exposed as decimal strings.
func isDecimalInputSQLType(sqlType string) bool {
	s := strings.ToLower(sqlType)
	return isDecimalSQLType(s) || strings.Contains(s, "float") || strings.Contains(s, "double") || s == "real"
}

func isTimeSQLType(sqlType string) bool {
	s := strings.ToLower(sqlType)
	return strings.Contains(s, "datetime") || strings.Contains(s, "timestamp") || strings.Contains(s, "date") || s == "time" || s == "timetz"
}

func getGoTypeForVo(sqlType string) string {
	if isTimeSQLType(sqlType) {
		return "int64"
	}
	return getGoTypeForApiDto(sqlType)
}

func isDecimalSQLType(sqlType string) bool {
	s := strings.ToLower(sqlType)
	return strings.Contains(s, "decimal") || strings.Contains(s, "numeric") || strings.Contains(s, "money")
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

// writeGeneratedFile preserves extension files and reports filesystem failures.
func writeGeneratedFile(path, content string, overwrite bool) (bool, error) {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("已存在，跳过: %s\n", path)
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, err
	}
	fmt.Printf("已生成: %s\n", path)
	return true, nil
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

func generateValidateRule(col ColumnInfo) string { // 根据数据库字段生成入参校验，JSON 字符串校验内部语法。
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
	if fieldType == "string" && !strings.Contains(sqlType, "json") {
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
	if len(col.EnumValues) > 0 {
		enumVals = col.EnumValues
		if rule := nativeEnumOneof(col.EnumValues); rule != "" {
			rules = appendRule(rules, rule)
		}
	} else if len(enumVals) > 0 {
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
			IsJSONType:    strings.Contains(strings.ToLower(col.Type), "json"), // JSON 字段以字符串形式暴露给 API。
			IsDecimalType: isDecimalInputSQLType(col.Type),
			JsonTag:       LowerCamelCase(Case2Camel(col.Name)), JsonTagOpt: jsonTagOpt,
			CanNull: col.CanNull, IsKey: col.IsKey, Extra: col.Extra, Comment: col.Comment,
			Validate: generateValidateRule(ColumnInfo{
				EnumValues: col.EnumValues, // 原生枚举以数据库合法值校验，不猜备注数字。
				CanNull:    col.CanNull, IsKey: col.IsKey,
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
			EnumValues: col.EnumValues, // 原生 ENUM 在 Proto 中仍为字符串，生成字符串集合校验。
			Name:       col.Name, Type: col.Type, FieldName: Case2Camel(col.Name),
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
			IsJSONType: strings.Contains(strings.ToLower(col.Type), "json"), // 响应中的 JSON 内容使用字符串。
			CanNull:    col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
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
			IsDecimalType: isDecimalInputSQLType(col.Type),
			IsJSONType:    strings.Contains(strings.ToLower(col.Type), "json"), // 请求中的 JSON 内容使用字符串。
			CanNull:       col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
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
	hasTime, hasDecimal, hasFloat, hasJSON := false, false, false, false
	for i, col := range columns {
		s := strings.ToLower(col.Type)
		isTime := isTimeSQLType(s)
		isDecimal := isDecimalSQLType(s)
		isFloat := isDecimalInputSQLType(s) && !isDecimal
		isJSON := strings.Contains(s, "json")
		hasJSON = hasJSON || isJSON
		hasFloat = hasFloat || isFloat
		if isTime && col.Name != "created_at" && col.Name != "updated_at" && col.Name != "deleted_at" {
			hasTime = true
		}
		if isDecimal {
			hasDecimal = true
		}
		columnData[i] = ColumnInfo{
			Name: col.Name, Type: col.Type,
			FieldName: Case2Camel(col.Name), FieldType: getGoType(col.Type),
			EnumTypeName: col.EnumTypeName, // Model 枚举与接口字符串之间显式转换。
			CanNull:      col.CanNull, IsKey: col.IsKey, Comment: col.Comment,
			IsTimeType: isTime, IsAuditField: auditFields[col.Name], IsDecimalType: isDecimal, IsFloatType: isFloat, IsJSONType: isJSON,
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
		HasTimeField: hasTime, HasDecimalField: hasDecimal, HasFloatField: hasFloat, HasJSONField: hasJSON, Columns: columnData,
	}
}

func buildProtoMapperData(tableName string, columns []ColumnInfo, modelName string, db *gorm.DB,
	pkg, modelPkgPath, protoPkgPath, apiTypesPkgPath string) ProtoMapperTemplateData {
	columnData := make([]ColumnInfo, 0, len(columns))
	writableColumns := make([]ColumnInfo, 0, len(columns))
	hasTime, hasWritableTime, hasDecimal, hasFloat, hasJSON := false, false, false, false, false
	for _, col := range columns {
		s := strings.ToLower(col.Type)
		isTime := isTimeSQLType(s)
		isDecimal := isDecimalSQLType(s)
		isFloat := strings.Contains(s, "float") || strings.Contains(s, "double") || strings.Contains(s, "real")
		isJSON := strings.Contains(s, "json")
		hasJSON = hasJSON || isJSON
		isBytes := strings.Contains(s, "binary") || strings.Contains(s, "blob") || strings.Contains(s, "bytea")
		hasTime = hasTime || isTime
		hasDecimal = hasDecimal || isDecimal
		hasFloat = hasFloat || isFloat
		item := ColumnInfo{
			Name: col.Name, Type: col.Type, FieldName: Case2Camel(col.Name),
			EnumTypeName: col.EnumTypeName, // RPC 字符串契约保持不变。
			ParamName:    normalizeAcronyms(Case2Camel(col.Name)),
			IsTimeType:   isTime, IsDecimalType: isDecimal, IsFloatType: isFloat, IsJSONType: isJSON, IsBytesType: isBytes,
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
		HasDecimalField: hasDecimal, HasFloatField: hasFloat, HasJSONField: hasJSON,
		Columns: columnData, WritableColumns: writableColumns,
	}
}

// ═══════════════════════════════════════════════════════════
//  单表文件生成入口
// ═══════════════════════════════════════════════════════════

func generateForTable(tbl string, columns []ColumnInfo, cfg *Config, db *gorm.DB,
	repoGenTmplPath, repoTmplPath, apiTmplPath, protoTmplPath, voTmplPath, dtoTmplPath,
	mapperTmplPath, entityProtoMapperTmplPath, apiProtoMapperTmplPath string) error {

	modelName := Case2Camel(strings.ToUpper(tbl[:1]) + tbl[1:])
	publicColumns := sensitivePublicColumns(tbl, columns, cfg.SensitiveFields)

	// 统一传递渲染和写入错误，避免某一层缺失却仍报告全部生成成功。
	emit := func(path string, overwrite bool, render func() (string, error)) error {
		content, err := render()
		if err != nil {
			return fmt.Errorf("渲染 %s 失败: %w", path, err)
		}
		if _, err = writeGeneratedFile(path, content, overwrite); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", path, err)
		}
		return nil
	}

	if cfg.ProtoPath != "" {
		protoPackage := getProtoPackage(cfg.Package)
		baseProtoImport := filepath.Join(filepath.Base(filepath.Clean(cfg.ProtoPath)), "base.proto")
		if err := emit(filepath.Join(cfg.ProtoPath, "base.proto"), false, func() (string, error) {
			return renderTemplate(filepath.Join(filepath.Dir(protoTmplPath), "base_proto_template.txt"), ProtoTemplateData{ProtoPackage: protoPackage})
		}); err != nil {
			return err
		}
		if err := emit(filepath.Join(cfg.ProtoPath, LowerCamelCase(Case2Camel(tbl))+".proto"), false, func() (string, error) {
			return generateProtoFile(tbl, publicColumns, modelName, db, protoTmplPath, protoPackage, baseProtoImport)
		}); err != nil {
			return err
		}
	}
	if cfg.RepoPath != "" {
		if err := emit(filepath.Join(cfg.RepoPath, strings.ToLower(modelName)+"_gen.go"), true, func() (string, error) {
			return generateRepositoryFile(columns, modelName, cfg.Package, pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoGenTmplPath, tbl)
		}); err != nil {
			return err
		}
		if err := emit(filepath.Join(cfg.RepoPath, strings.ToLower(modelName)+".go"), false, func() (string, error) {
			return generateRepositoryExtFile(columns, modelName, cfg.Package, pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoTmplPath, tbl)
		}); err != nil {
			return err
		}
	}
	if cfg.ApiPath != "" {
		if err := emit(filepath.Join(cfg.ApiPath, "base.api"), false, func() (string, error) {
			return renderTemplate(filepath.Join(filepath.Dir(apiTmplPath), "base_api_template.txt"), nil)
		}); err != nil {
			return err
		}
		content, err := generateApiFile(tbl, publicColumns, modelName, db, apiTmplPath)
		if err != nil {
			return err
		}
		apiFile := filepath.Join(cfg.ApiPath, LowerCamelCase(Case2Camel(tbl))+".api")
		created, err := writeGeneratedFile(apiFile, content, false)
		if err != nil {
			return err
		}
		if created {
			output, err := gozerogen.Run(context.Background(), gozerogen.RunOptions{
				TemplateHome: cfg.GoctlTemplateHome,
			}, "api", "go", "-api", apiFile, "--dir", filepath.Dir(cfg.ApiPath), "--style=goZero")
			if err != nil {
				return fmt.Errorf("[%s] goctl 执行失败: %w\n%s", tbl, err, output)
			}
			fmt.Printf("[%s] go-zero 代码生成成功\n", tbl)
		}
	}
	if cfg.ApiPath == "" {
		if cfg.VoPath != "" {
			if err := emit(filepath.Join(cfg.VoPath, LowerCamelCase(Case2Camel(modelName))+"Vo.go"), false, func() (string, error) {
				return generateVoFile(tbl, publicColumns, modelName, db, voTmplPath)
			}); err != nil {
				return err
			}
		}
		if cfg.DtoPath != "" {
			if err := emit(filepath.Join(cfg.DtoPath, LowerCamelCase(Case2Camel(modelName))+"Dto.go"), false, func() (string, error) {
				return generateDtoFile(tbl, publicColumns, modelName, db, dtoTmplPath)
			}); err != nil {
				return err
			}
		}
	}
	if cfg.ProtoPath == "" {
		if cfg.APIMapperPath != "" {
			dtoPkg, voPkg := "", ""
			if cfg.ApiPath == "" {
				dtoPkg, voPkg = pathToPkg(cfg.DtoPath), pathToPkg(cfg.VoPath)
			}
			data := buildMapperData(tbl, publicColumns, modelName, db,
				cfg.Package, pathToPkg(cfg.ModelPkgPath), dtoPkg, voPkg, cfg.ApiPath != "")
			return emit(filepath.Join(cfg.APIMapperPath, LowerCamelCase(Case2Camel(tbl))+"Mapper.go"), false, func() (string, error) {
				return renderTemplate(mapperTmplPath, data)
			})
		}
	} else if cfg.ProtoMapperPath != "" || (cfg.ApiPath != "" && cfg.APIMapperPath != "") {
		protoPkgPath := filepath.Join(pathToPkg(cfg.ProtoPath), getProtoPackage(cfg.Package))
		apiTypesPkgPath := ""
		if cfg.ApiPath != "" {
			apiTypesPkgPath = pathToPkg(filepath.Join(filepath.Dir(cfg.ApiPath), "internal", "types"))
		}
		data := buildProtoMapperData(tbl, publicColumns, modelName, db,
			cfg.Package, pathToPkg(cfg.ModelPkgPath), protoPkgPath, apiTypesPkgPath)
		if cfg.ProtoMapperPath != "" {
			if err := emit(filepath.Join(cfg.ProtoMapperPath, LowerCamelCase(Case2Camel(tbl))+"Mapper.go"), false, func() (string, error) {
				return renderTemplate(entityProtoMapperTmplPath, data)
			}); err != nil {
				return err
			}
		}
		if cfg.ApiPath != "" && cfg.APIMapperPath != "" {
			return emit(filepath.Join(cfg.APIMapperPath, LowerCamelCase(Case2Camel(tbl))+"Mapper.go"), false, func() (string, error) {
				return renderTemplate(apiProtoMapperTmplPath, data)
			})
		}
	}
	return nil
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

	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
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
	enumTmplPath := filepath.Join(templateDir, "enum_template.txt") // 实体同包的原生枚举模板。

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
	dataTypeMap := map[string]func(gorm.ColumnType) string{
		"int":       func(_ gorm.ColumnType) string { return "int64" },
		"int2":      func(_ gorm.ColumnType) string { return "int64" },
		"int4":      func(_ gorm.ColumnType) string { return "int64" },
		"mediumint": func(_ gorm.ColumnType) string { return "int64" },
		"smallint":  func(_ gorm.ColumnType) string { return "int64" },
		"integer":   func(_ gorm.ColumnType) string { return "int64" },
		"tinyint":   func(_ gorm.ColumnType) string { return "int64" },
		"bigint":    func(_ gorm.ColumnType) string { return "int64" },
		"int8":      func(_ gorm.ColumnType) string { return "int64" },
		"bit": func(_ gorm.ColumnType) string {
			if db.Dialector.Name() == "sqlserver" {
				return "bool"
			}
			if db.Dialector.Name() == "postgres" {
				return "string"
			}
			return "[]byte"
		},
		"bit varying":      func(_ gorm.ColumnType) string { return "string" },
		"bool":             func(_ gorm.ColumnType) string { return "bool" },
		"float":            func(_ gorm.ColumnType) string { return "float64" },
		"float4":           func(_ gorm.ColumnType) string { return "float64" },
		"float8":           func(_ gorm.ColumnType) string { return "float64" },
		"double precision": func(_ gorm.ColumnType) string { return "float64" },
		"numeric":          func(_ gorm.ColumnType) string { return "decimal.Decimal" },
		"money":            func(_ gorm.ColumnType) string { return "decimal.Decimal" },
		"smallmoney":       func(_ gorm.ColumnType) string { return "decimal.Decimal" },
		"timestamp": func(_ gorm.ColumnType) string {
			if db.Dialector.Name() == "sqlserver" {
				return "[]byte"
			}
			return "time.Time"
		},
		"rowversion":     func(_ gorm.ColumnType) string { return "[]byte" },
		"timetz":         func(_ gorm.ColumnType) string { return "time.Time" },
		"timestamptz":    func(_ gorm.ColumnType) string { return "time.Time" },
		"datetime2":      func(_ gorm.ColumnType) string { return "time.Time" },
		"datetimeoffset": func(_ gorm.ColumnType) string { return "time.Time" },
		"smalldatetime":  func(_ gorm.ColumnType) string { return "time.Time" },
		"bytea":          func(_ gorm.ColumnType) string { return "[]byte" },
		"jsonb":          func(_ gorm.ColumnType) string { return "datatypes.JSON" },
		"json":           func(_ gorm.ColumnType) string { return "datatypes.JSON" },
		"decimal":        func(_ gorm.ColumnType) string { return "decimal.Decimal" },
	}
	// 有些驱动返回大写 DatabaseTypeName，gorm-gen 的自定义映射按原值匹配。
	for typ, mapping := range dataTypeMap {
		dataTypeMap[strings.ToUpper(typ)] = mapping
	}
	g.WithDataTypeMap(dataTypeMap)
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
		if modelTables, err = getTables(db); err != nil {
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

	// 在 gorm-gen 写文件前检查所有表，避免元数据失败留下不完整产物。
	columnsByTable := make(map[string][]ColumnInfo, len(modelTables))
	enumsByTable := make(map[string][]enumDefinition, len(modelTables)) // 先验证所有枚举，再写生成文件。
	enumSymbols := make(map[string]string)                              // 检查跨表的 Go 符号冲突。
	for _, tbl := range modelTables {
		columns, err := getTableColumns(db, tbl)
		if err != nil {
			return fmt.Errorf("[%s] 获取表结构失败: %w", tbl, err)
		}
		columnsByTable[tbl] = columns
		definitions, err := buildEnumDefinitions(tbl, columns)
		if err != nil {
			return fmt.Errorf("[%s] 枚举定义失败: %w", tbl, err)
		}
		for _, definition := range definitions {
			symbols := []string{definition.Name, "Get" + definition.Name + "Label"}
			if definition.HasLabels {
				symbols = append(symbols, definition.LabelMap)
			}
			for _, member := range definition.Members {
				symbols = append(symbols, member.Name)
			}
			for _, symbol := range symbols {
				if previous, exists := enumSymbols[symbol]; exists {
					return fmt.Errorf("枚举符号 %s 在表 %s 与 %s 之间冲突", symbol, previous, tbl)
				}
				enumSymbols[symbol] = tbl
			}
		}
		enumsByTable[tbl] = definitions
	}
	for _, dir := range []string{cfg.OutPath, cfg.ModelPkgPath, cfg.RepoPath, cfg.ApiPath, cfg.ProtoPath, cfg.VoPath, cfg.DtoPath, cfg.APIMapperPath, cfg.ProtoMapperPath} {
		if dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("创建输出目录 %s 失败: %w", dir, err)
			}
		}
	}
	for _, tbl := range modelTables {
		path := enumFilePath(cfg, tbl)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("创建枚举目录失败: %w", err)
		}
		if err := writeEnumFile(path, enumTmplPath, getLastPathSegment(filepath.Dir(path)), enumsByTable[tbl]); err != nil {
			return fmt.Errorf("[%s] 生成枚举文件失败: %w", tbl, err)
		}
	}

	// ③ 统一生成（含所有历史表 + 新表）
	allModels := make([]interface{}, 0, len(modelTables))
	for _, tbl := range modelTables {
		tableFieldOpts := append([]gen.ModelOpt{}, fieldOpts...)
		tableFieldOpts = append(tableFieldOpts, enumModelOptions(columnsByTable[tbl])...)
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
		if err := generateForTable(tbl, columnsByTable[tbl], cfg, db,
			repoGenTmplPath, repoTmplPath,
			apiTmplPath, protoTmplPath, voTmplPath, dtoTmplPath,
			mapperTmplPath, entityProtoMapperTmplPath, apiProtoMapperTmplPath); err != nil {
			return fmt.Errorf("[%s] 生成失败: %w", tbl, err)
		}
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
		tagValue := sensitiveFieldTagValue(sensitiveType, cipherColumn, indexColumn, cfg.EncryptAtRest)
		opts = append(opts, gen.FieldNew(logicalName, "string", field.Tag{
			"gorm":     "-",
			"json":     LowerCamelCase(logicalName),
			"gormplus": tagValue,
		}))
		// 密文和查询索引仅供数据库与插件内部使用，禁止 JSON 序列化到前端。
		opts = append(opts,
			gen.FieldJSONTag(cipherColumn, "-"),
			gen.FieldJSONTag(indexColumn, "-"),
		)
	}
	return opts
}

func sensitiveFieldTagValue(sensitiveType, cipherColumn, indexColumn string, encryptAtRest bool) string {
	return fmt.Sprintf("type:%s;cipher:%s;index:%s;encrypt:%t", sensitiveType, cipherColumn, indexColumn, encryptAtRest)
}

// sensitivePublicColumns 将内部密文/索引列替换成对外业务字段。
// Repository/DAO 仍使用原始数据库列；API、Proto、DTO、VO、Mapper 只暴露业务字段。
func sensitivePublicColumns(table string, columns []ColumnInfo, configs []SensitiveFieldConfig) []ColumnInfo {
	result := append([]ColumnInfo(nil), columns...)
	for _, cfg := range configs {
		if !strings.EqualFold(strings.TrimSpace(cfg.Table), strings.TrimSpace(table)) || strings.TrimSpace(cfg.Field) == "" {
			continue
		}
		logicalColumn := strings.TrimSpace(cfg.Field)
		cipherColumn := strings.TrimSpace(cfg.CipherField)
		if cipherColumn == "" {
			cipherColumn = logicalColumn + "_cipher"
		}
		indexColumn := strings.TrimSpace(cfg.IndexField)
		if indexColumn == "" {
			indexColumn = logicalColumn + "_index"
		}
		filtered := result[:0]
		for _, column := range result {
			if strings.EqualFold(column.Name, cipherColumn) || strings.EqualFold(column.Name, indexColumn) {
				continue
			}
			filtered = append(filtered, column)
		}
		result = append(filtered, ColumnInfo{
			Name:       logicalColumn,
			Type:       "varchar(32)",
			FieldName:  Case2Camel(logicalColumn),
			FieldType:  "string",
			CanNull:    false,
			Comment:    "敏感字段（仅用于业务输入输出）",
			JsonTag:    LowerCamelCase(Case2Camel(logicalColumn)),
			JsonTagOpt: "",
		})
	}
	return result
}
