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
	// 使用Title后，再把特定的全大写缩写词恢复
	name = strings.Title(name)
	// 处理常见的缩写词，如 IP, ID, URL, API 等
	acronyms := []string{"IP", "ID", "URL", "API", "IOS", "API", "XML", "JSON", "JWT", "SQL", "ORM"}
	for _, acronym := range acronyms {
		name = strings.ReplaceAll(name, strings.Title(strings.ToLower(acronym)), acronym)
	}
	return strings.Replace(name, " ", "", -1)
}

func LowerCamelCase(name string) string {
	// 如果已经是小写开头且没有大写字母，直接返回
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
	ModelPkgName    string // model包的名称，如 "entity"
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

	// 优先尝试从文件系统加载（方便用户自定义覆盖模板）
	fileContent, err := os.ReadFile(templatePath)
	if err == nil {
		return template.New(templateName).Funcs(funcMap).Parse(string(fileContent))
	}

	// 文件不存在时，回退到内嵌模板
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

// Generate 代码生成器主函数
func Generate(cfg *Config) error {
	// 构建DSN
	dsn := fmt.Sprintf("%s:%s@(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)

	db, err := gorm.Open(mysql.Open(dsn))
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}

	tableName := readInput("请输入表名（直接回车同步所有表）: ")

	// 获取模板路径 - 优先使用环境变量或当前工作目录
	templateDir := os.Getenv("GENERATOR_TEMPLATE_DIR")
	if templateDir == "" {
		// 尝试相对于可执行文件的路径
		exePath, err := os.Executable()
		if err == nil {
			// 对于 go run，可执行文件在缓存目录，需要找到源码目录
			// 尝试向上查找 generator/template 目录
			checkDir := filepath.Dir(exePath)
			for i := 0; i < 10; i++ {
				testDir := filepath.Join(checkDir, "generator", "template")
				if _, statErr := os.Stat(testDir); statErr == nil {
					templateDir = testDir
					break
				}
				// 继续向上查找
				parent := filepath.Dir(checkDir)
				if parent == checkDir {
					break
				}
				checkDir = parent
			}
		}
		// 如果还是没找到，尝试相对于当前工作目录的路径
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

	// 路径为空表示该功能未配置，保持空值，后续按空值判断是否生成

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
		return map[string][]string{
			"column":  {"updated_at"},
			"comment": {"更新时间"},
		}
	})
	autoCreateTimeField := gen.FieldGORMTag("created_at", func(tag field.GormTag) field.GormTag {
		return map[string][]string{
			"column":  {"created_at"},
			"comment": {"创建时间"},
		}
	})
	softDeleteField := gen.FieldType("deleted_at", "gorm.DeletedAt")
	fieldOpts := []gen.ModelOpt{jsonField, autoCreateTimeField, autoUpdateTimeField, softDeleteField}

	var allModel []interface{}
	if tableName != "" {
		fmt.Printf("生成表 %s 的模型...\n", tableName)
		model := g.GenerateModel(tableName, fieldOpts...)
		allModel = []interface{}{model}
	} else {
		fmt.Println("生成所有表的模型...")
		allModel = g.GenerateAllTable(fieldOpts...)
	}

	//g.ApplyBasic(allModel...)
	//g.Execute()

	if tableName != "" {
		fmt.Printf("生成表 %s 的模型...\n", tableName)
		g.GenerateModel(tableName, fieldOpts...)
	} else {
		fmt.Println("生成所有表的模型...")
		allModel = g.GenerateAllTable(fieldOpts...)
		g.ApplyBasic(allModel...)
		g.Execute()
	}

	// 未输入表名时只生成数据模型，不继续生成 repo/vo/dto/api
	//if tableName == "" {
	//	fmt.Println("生成完成!")
	//	return nil
	//}

	columns, err := getTableColumns(db, tableName)
	if err != nil {
		return fmt.Errorf("获取表结构失败: %w", err)
	}
	modelName := Case2Camel(strings.ToUpper(tableName[:1]) + tableName[1:])

	if cfg.RepoPath != "" {
		repoDir := cfg.RepoPath
		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			os.MkdirAll(repoDir, 0755)
		}

		// 1. 生成基础 repository1 (xxx_base.go) - 始终重新生成
		repoBaseContent, err := generateRepositoryFile(columns, modelName, cfg.Package, pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoGenTmplPath)
		if err != nil {
			fmt.Printf("生成repository基础内容失败: %v\n", err)
		} else {
			repoBaseFileName := fmt.Sprintf("%s/%s_gen.go", repoDir, strings.ToLower(modelName))
			if _, err := os.Stat(repoBaseFileName); os.IsNotExist(err) {
				err = os.WriteFile(repoBaseFileName, []byte(repoBaseContent), 0644)
				if err != nil {
					fmt.Printf("写入repository基础文件失败: %v\n", err)
				} else {
					fmt.Printf("repository基础文件已生成: %s\n", repoBaseFileName)
				}
			} else {
				fmt.Printf("repository_gen扩展文件已存在，不覆盖更新: %s\n", repoBaseFileName)
			}
		}

		// 2. 生成扩展 repository1 (xxx.go) - 如果已存在则跳过
		repoExtContent, err := generateRepositoryExtFile(columns, modelName, cfg.Package, pathToPkg(cfg.OutPath), pathToPkg(cfg.ModelPkgPath), repoTmplPath)
		if err != nil {
			fmt.Printf("生成repository扩展内容失败: %v\n", err)
		} else {
			repoExtFileName := fmt.Sprintf("%s/%s.go", repoDir, strings.ToLower(modelName))
			if _, err := os.Stat(repoExtFileName); os.IsNotExist(err) {
				err = os.WriteFile(repoExtFileName, []byte(repoExtContent), 0644)
				if err != nil {
					fmt.Printf("写入repository扩展文件失败: %v\n", err)
				} else {
					fmt.Printf("repository扩展文件已生成: %s\n", repoExtFileName)
				}
			} else {
				fmt.Printf("repository扩展文件已存在，不覆盖更新: %s\n", repoExtFileName)
			}
		}
	}

	if cfg.ApiPath != "" {
		apiDir := cfg.ApiPath
		if _, err := os.Stat(apiDir); os.IsNotExist(err) {
			os.MkdirAll(apiDir, 0755)
		}

		apiContent, err := generateApiFile(tableName, columns, modelName, db, apiTmplPath)
		if err != nil {
			return fmt.Errorf("生成api内容失败: %w", err)
		}
		apiFileName := fmt.Sprintf("%s/%s.api", apiDir, tableName)
		// 检查文件是否已存在
		if _, err := os.Stat(apiFileName); os.IsNotExist(err) {
			err = os.WriteFile(apiFileName, []byte(apiContent), 0644)
			if err != nil {
				return fmt.Errorf("写入api文件失败: %w", err)
			}
			fmt.Printf("api文件已生成: %s\n", apiFileName)

			goctlPath := getGoctlPath()
			cmd := exec.Command(goctlPath, "api", "go", "-api", apiFileName, "--dir", filepath.Dir(cfg.ApiPath), "--style=goZero")
			cmd.Dir = "."
			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Printf("执行goctl失败: %v\n%s\n", err, output)
			} else {
				fmt.Printf("go-zero代码生成成功\n%s\n", output)
			}
		} else {
			fmt.Printf("api文件已存在，不覆盖更新: %s\n", apiFileName)
		}
	}

	if cfg.VoPath != "" {
		voDir := cfg.VoPath
		if _, err := os.Stat(voDir); os.IsNotExist(err) {
			os.MkdirAll(voDir, 0755)
		}

		voContent, err := generateVoFile(tableName, columns, modelName, db, voTmplPath)
		if err != nil {
			fmt.Printf("生成vo内容失败: %v\n", err)
		} else {
			voFileName := fmt.Sprintf("%s/%sVo.go", voDir, strings.ToLower(modelName))
			err = os.WriteFile(voFileName, []byte(voContent), 0644)
			if err != nil {
				fmt.Printf("写入vo文件失败: %v\n", err)
			} else {
				fmt.Printf("vo文件已生成: %s\n", voFileName)
			}
		}
	}

	if cfg.DtoPath != "" {
		dtoDir := cfg.DtoPath
		if _, err := os.Stat(dtoDir); os.IsNotExist(err) {
			os.MkdirAll(dtoDir, 0755)
		}

		dtoContent, err := generateDtoFile(tableName, columns, modelName, db, dtoTmplPath)
		if err != nil {
			fmt.Printf("生成dto内容失败: %v\n", err)
		} else {
			dtoFileName := fmt.Sprintf("%s/%sDto.go", dtoDir, strings.ToLower(modelName))
			err = os.WriteFile(dtoFileName, []byte(dtoContent), 0644)
			if err != nil {
				fmt.Printf("写入dto文件失败: %v\n", err)
			} else {
				fmt.Printf("dto文件已生成: %s\n", dtoFileName)
			}
		}
	}

	fmt.Println("生成完成!")
	return nil
}
