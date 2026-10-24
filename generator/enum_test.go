package generator

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-playground/validator/v10"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gen"
	"gorm.io/gorm"
)

func TestParseMySQLEnum(t *testing.T) { // 标签保留大小写，逗号与引号不能被当作成员分隔符。
	for _, test := range []struct {
		definition string   // 数据库返回的完整 ENUM 定义。
		want       []string // 按定义顺序保留的原值。
	}{
		{`enum('pending','PAID','cancelled')`, []string{"pending", "PAID", "cancelled"}},
		{`ENUM('1', '2', '3')`, []string{"1", "2", "3"}},
		{`enum('','x,y','it''s','a\\b','x\'y','line\nbreak')`, []string{"", "x,y", "it's", `a\b`, "x'y", "line\nbreak"}},
	} {
		got, err := parseMySQLEnum(test.definition)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s: %q %v; want %q", test.definition, got, err, test.want)
		}
	}
	for _, definition := range []string{"enum", "enum()", "enum('x',)", "enum('x'", "enum('x') trailing", "enum('unterminated)", "varchar(40)"} {
		if _, err := parseMySQLEnum(definition); err == nil {
			t.Fatalf("accepted %q", definition)
		}
	}
}

func TestEnumNamesAndCommentLabels(t *testing.T) { // 按表隔离类型，数字标签使用 Value 前缀，翻译只取明确备注。
	for _, table := range []string{"order", "test"} {
		definitions, err := buildEnumDefinitions(table, []ColumnInfo{{Name: "status", EnumValues: []string{"1", "2", "3"}, Comment: "订单状态：1、待支付，2、已支付，3、已取消"}})
		if err != nil {
			t.Fatal(err)
		}
		d := definitions[0]
		want := enumIdentifier(table) + "StatusEnum"
		if d.Name != want || !d.HasLabels || d.Members[0].Name != want+"Value1" || d.Members[1].Label != "已支付" || d.Comment != "订单状态" {
			t.Fatalf("unexpected enum: %+v", d)
		}
	}
	_, labels := enumCommentLabels("状态：pending=待支付;paid=已支付;cancelled=已取消", []string{"pending", "paid", "cancelled"})
	if labels["pending"] != "待支付" || labels["cancelled"] != "已取消" {
		t.Fatal(labels)
	}
	_, labels = enumCommentLabels("1是启用，2是禁用", []string{"1", "2"})
	if labels["1"] != "启用" || labels["2"] != "禁用" {
		t.Fatal(labels)
	}
	d, err := buildEnumDefinitions("order", []ColumnInfo{{Name: "status", EnumValues: []string{"pending", "paid"}, Comment: "订单状态"}})
	if err != nil || d[0].HasLabels || d[0].Members[0].Name != "OrderStatusEnumPending" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := buildEnumDefinitions("order", []ColumnInfo{{Name: "status", EnumValues: []string{"x-y", "x_y"}}}); err == nil {
		t.Fatal("constant collision accepted")
	}
}

func TestNativeEnumValidation(t *testing.T) { // 接口校验使用真实值；空格、逗号与管道字符不能破坏校验标签。
	values := []string{"pending", "in progress", "a,b", "x|y", ""}
	rule := generateValidateRule(ColumnInfo{Name: "status", Type: "enum", CanNull: true, EnumValues: values, Comment: "说明：1是旧值，2是旧值"})
	v := validator.New()
	for _, value := range values {
		if err := v.Var(value, rule); err != nil {
			t.Fatalf("rule=%s value=%q: %v", rule, value, err)
		}
	}
	if err := v.Var("other", rule); err == nil {
		t.Fatal("invalid native enum passed")
	}
	proto := buildProtoValidationOptions(ColumnInfo{FieldType: "string", Type: "enum", EnumValues: []string{"1", "paid"}}, false)
	if !strings.Contains(proto, `string = {in: ["1", "paid"]}`) {
		t.Fatal(proto)
	}
	if nativeEnumOneof([]string{"it's"}) != "" || nativeEnumOneof([]string{"0x2C"}) != "" {
		t.Fatal("unsafe oneof emitted")
	}
}

type enumCatalogDriver struct {
	columns []string         // 模拟数据库目录返回的列。
	rows    [][]driver.Value // 模拟真实目录中的枚举标签。
}
type enumCatalogConn struct {
	catalog *enumCatalogDriver // 只响应预期目录查询。
}
type enumCatalogRows struct {
	catalog *enumCatalogDriver // 模拟目录数据。
	index   int                // 下一条结果的位置。
}

func (d *enumCatalogDriver) Open(string) (driver.Conn, error) { // 提供不访问网络的目录查询连接。
	return &enumCatalogConn{catalog: d}, nil
}
func (c *enumCatalogConn) Prepare(query string) (driver.Stmt, error) { // 目录测试不使用预编译语句。
	return nil, fmt.Errorf("unexpected prepare %s", query)
}
func (*enumCatalogConn) Close() error { // 目录测试无外部资源。
	return nil
}
func (*enumCatalogConn) Begin() (driver.Tx, error) { // 不允许测试写数据库。
	return nil, fmt.Errorf("unexpected transaction")
}
func (c *enumCatalogConn) Query(query string, args []driver.Value) (driver.Rows, error) { // 校验目录查询参数，避免枚举绑定到错误的表。
	if strings.Contains(query, "pg_enum") {
		if len(args) != 1 || args[0] != `"sales"."order"` {
			return nil, fmt.Errorf("unexpected PostgreSQL args %v", args)
		}
	} else if strings.Contains(query, "information_schema.COLUMNS") {
		if len(args) != 2 || args[0] != "sales" || args[1] != "order" {
			return nil, fmt.Errorf("unexpected MySQL args %v", args)
		}
	} else {
		return nil, fmt.Errorf("unexpected query %s", query)
	}
	return &enumCatalogRows{catalog: c.catalog}, nil
}
func (r *enumCatalogRows) Columns() []string { // 返回目录结果列。
	return r.catalog.columns
}
func (*enumCatalogRows) Close() error { // 测试结果不持有外部资源。
	return nil
}
func (r *enumCatalogRows) Next(dest []driver.Value) error { // 按数据库给定顺序返回标签。
	if r.index >= len(r.catalog.rows) {
		return io.EOF
	}
	copy(dest, r.catalog.rows[r.index])
	r.index++
	return nil
}

var enumCatalogID atomic.Int64

func TestNativeEnumCatalogs(t *testing.T) { // MySQL 与 PostgreSQL 目录结果必须保留值、大小写、顺序及 schema 绑定。
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			catalog := &enumCatalogDriver{columns: []string{"name", "value"}, rows: [][]driver.Value{{"status", "pending"}, {"status", "PAID"}, {"status", "cancelled"}}}
			if dialect == "mysql" {
				catalog.columns = []string{"name", "definition"}
				catalog.rows = [][]driver.Value{{"status", `enum('pending','PAID','cancelled')`}}
			}
			name := fmt.Sprintf("gormplus-enum-%d", enumCatalogID.Add(1))
			sql.Register(name, catalog)
			pool, err := sql.Open(name, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Close() })
			var d gorm.Dialector = postgres.New(postgres.Config{Conn: pool})
			if dialect == "mysql" {
				d = mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true})
			}
			db, err := gorm.Open(d, &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			columns := []ColumnInfo{{Name: "status", Type: "enum"}, {Name: "description", Type: "text"}}
			if err := loadNativeEnums(db, "sales.order", columns); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(columns[0].EnumValues, []string{"pending", "PAID", "cancelled"}) || columns[0].EnumTypeName != "OrderStatusEnum" || len(columns[1].EnumValues) != 0 {
				t.Fatalf("%+v", columns)
			}
		})
	}
}

func TestEnumGeneratedLifecycle(t *testing.T) { // 翻译 map 随生成更新，删除枚举时清理自己的文件，拒绝覆盖用户文件。
	path := filepath.Join(t.TempDir(), "order_enum.gen.go")
	definitions, err := buildEnumDefinitions("order", []ColumnInfo{{Name: "status", EnumValues: []string{"1", "2"}, Comment: "状态：1=待支付，2=已支付"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnumFile(path, "enum_template.txt", "model", definitions); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	for _, want := range []string{"type OrderStatusEnum string", "orderStatusEnumLabels = map[OrderStatusEnum]string", `OrderStatusEnumValue1: "待支付"`, "func GetOrderStatusEnumLabel(value OrderStatusEnum) string"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("missing %s\n%s", want, content)
		}
	}
	definitions[0].Members[0].Label = "等待付款"
	if err := writeEnumFile(path, "enum_template.txt", "model", definitions); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(path)
	if !strings.Contains(string(content), "等待付款") {
		t.Fatal("label not updated")
	}
	definitions[0].HasLabels = false
	for i := range definitions[0].Members {
		definitions[0].Members[i].Label = ""
	}
	if err := writeEnumFile(path, "enum_template.txt", "model", definitions); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(path)
	if strings.Contains(string(content), "orderStatusEnumLabels") {
		t.Fatal("removed comment kept stale label map")
	}
	if err := writeEnumFile(path, "enum_template.txt", "model", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale enum file remained")
	}
	if err := os.WriteFile(path, []byte("package model\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEnumFile(path, "enum_template.txt", "model", definitions); err == nil {
		t.Fatal("user file overwritten")
	}
	if err := writeEnumFile(path, "enum_template.txt", "model", nil); err == nil {
		t.Fatal("user file removed")
	}
}

func TestGeneratedEnumModelRoundTrip(t *testing.T) { // 使用 SQLite 的真实 CHECK 表验证生成类型、DAO、Mapper 和数据库往返；原生目录另测。
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("go.mod", []byte(fmt.Sprintf("module enumfixture\n\ngo 1.26.10\nrequire github.com/kuangshp/gorm-plus v0.0.0\nreplace github.com/kuangshp/gorm-plus => %s\n", root)), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "enum.db")))
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	for _, table := range []string{"order", "test"} {
		if err := db.Exec(`CREATE TABLE "` + table + `" (id INTEGER PRIMARY KEY, status TEXT NOT NULL CHECK (status IN ('pending','paid','cancelled')), stage TEXT CHECK (stage IN ('1','2','3')))`).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"dao", "model", "dto", "vo", "mapper"} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	g := gen.NewGenerator(gen.Config{OutPath: filepath.Join(dir, "dao"), ModelPkgPath: filepath.Join(dir, "model"), Mode: gen.WithDefaultQuery | gen.WithoutContext | gen.WithQueryInterface})
	g.UseDB(db)
	g.WithDataTypeMap(map[string]func(gorm.ColumnType) string{
		"integer": func(gorm.ColumnType) string { return "int64" },
		"INTEGER": func(gorm.ColumnType) string { return "int64" },
	})
	g.WithModelNameStrategy(func(table string) string { return enumIdentifier(table) + "Entity" })
	var models []interface{}
	for _, table := range []string{"order", "test"} {
		columns, err := getTableColumns(db, table)
		if err != nil {
			t.Fatal(err)
		}
		for i := range columns {
			if columns[i].Name == "status" {
				columns[i].EnumValues = []string{"pending", "paid", "cancelled"}
				columns[i].Comment = "状态：pending=待支付，paid=已支付，cancelled=已取消"
				if table == "test" {
					columns[i].Comment = "测试状态"
				}
			}
			if columns[i].Name == "stage" {
				columns[i].EnumValues = []string{"1", "2", "3"}
				columns[i].Comment = "阶段：1、待支付，2、已支付，3、已取消"
			}
			if len(columns[i].EnumValues) > 0 {
				columns[i].Type = "enum"
				columns[i].EnumTypeName = enumTypeName(table, columns[i].Name)
			}
		}
		definitions, err := buildEnumDefinitions(table, columns)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeEnumFile(filepath.Join(dir, "model", enumFileName(table)), filepath.Join(root, "generator/template/enum_template.txt"), "model", definitions); err != nil {
			t.Fatal(err)
		}
		models = append(models, g.GenerateModel(table, enumModelOptions(columns)...))
		name := enumIdentifier(table)
		for filename, render := range map[string]func() (string, error){
			"dto/" + table + ".go": func() (string, error) {
				return generateDtoFile(table, columns, name, db, filepath.Join(root, "generator/template/dto_template.txt"))
			},
			"vo/" + table + ".go": func() (string, error) {
				return generateVoFile(table, columns, name, db, filepath.Join(root, "generator/template/vo_template.txt"))
			},
			"mapper/" + table + ".go": func() (string, error) {
				return renderTemplate(filepath.Join(root, "generator/template/mapper_template.txt"), buildMapperData(table, columns, name, db, "enumfixture", "model", "dto", "vo", false))
			},
		} {
			content, err := render()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writeGeneratedFile(filename, content, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	g.ApplyBasic(models...)
	g.Execute()
	source, err := os.ReadFile(filepath.Join(root, "generator/testdata/enum_roundtrip_test.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("enum_test.go", source, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated enum fixture failed: %v\n%s", err, output)
	}
}

func TestPostgresEnumGeneration(t *testing.T) { // 配置独立测试 DSN 时验证 PostgreSQL 原生 ENUM 的完整增量生成链路。
	if os.Getenv("GORMPLUS_TEST_POSTGRES_DSN") == "" {
		t.Skip("set GORMPLUS_TEST_POSTGRES_DSN to test real PostgreSQL ENUM generation")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf("module enumfixture\n\ngo 1.26.10\nrequire github.com/kuangshp/gorm-plus v0.0.0\nreplace github.com/kuangshp/gorm-plus => %s\n", root)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, db := generationTestDatabase(t, dir, "postgres")
	statements := []string{
		`CREATE TYPE native_order_status AS ENUM ('pending','paid','cancelled')`,
		`CREATE TYPE native_order_stage AS ENUM ('1','2','3')`,
		`CREATE TABLE "order" (id BIGINT PRIMARY KEY, status native_order_status NOT NULL, stage native_order_stage)`,
		`CREATE TABLE test (id BIGINT PRIMARY KEY, status native_order_status NOT NULL, stage native_order_stage)`,
		`COMMENT ON COLUMN "order".status IS '订单状态：pending=待支付，paid=已支付，cancelled=已取消'`,
		`COMMENT ON COLUMN "order".stage IS '阶段：1、待支付，2、已支付，3、已取消'`,
		`COMMENT ON COLUMN test.status IS '测试状态'`,
		`COMMENT ON COLUMN test.stage IS '阶段：1、待支付，2、已支付，3、已取消'`,
	}
	if err := os.Mkdir(filepath.Join(dir, "sql"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sql/20261023原生枚举测试.sql"), []byte(strings.Join(statements, ";\n")), 0600); err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	t.Setenv("GENERATOR_TEMPLATE_DIR", filepath.Join(root, "generator/template"))
	cfg.Package, cfg.OutPath, cfg.ModelPkgPath, cfg.RepoPath = "enumfixture", "dao", "model", "repository"
	cfg.DtoPath, cfg.VoPath, cfg.APIMapperPath = "dto", "vo", "mapper"
	oldLines, oldIndex := inputLines, inputIndex
	t.Cleanup(func() { inputLines, inputIndex = oldLines, oldIndex })
	for _, table := range []string{"order", "test"} {
		inputLines, inputIndex = []string{table}, 0
		if err := Generate(cfg); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"order", "test"} {
		if _, err := os.Stat(filepath.Join(dir, "model", enumFileName(table))); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile(filepath.Join(root, "generator/testdata/enum_roundtrip_test.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "enum_test.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	// 实际原生数据库写入并读回具名枚举，SQL 约束不使用 SQLite 模拟。
	if err := os.WriteFile(filepath.Join(dir, "postgres_test.go"), []byte(`package enumfixture_test
import("os";"testing";"enumfixture/model";"gorm.io/driver/postgres";"gorm.io/gorm")
func TestActualPostgresEnum(t *testing.T){
 db,err:=gorm.Open(postgres.Open(os.Getenv("GORMPLUS_FIXTURE_POSTGRES_DSN")));if err!=nil{t.Fatal(err)}
 pool,_:=db.DB();defer pool.Close()
 row:=&model.OrderEntity{ID:17,Status:model.OrderStatusEnumPaid,Stage:model.OrderStageEnumValue1}
 if err:=db.Create(row).Error;err!=nil{t.Fatal(err)}
 var loaded model.OrderEntity
 if err:=db.First(&loaded,17).Error;err!=nil{t.Fatal(err)}
 if loaded.Status!=model.OrderStatusEnumPaid||loaded.Status.Label()!="已支付"{t.Fatal(loaded)}
 if err:=db.Model(row).Update("status",model.OrderStatusEnumCancelled).Error;err!=nil{t.Fatal(err)}
 if err:=db.Model(row).Update("status",model.OrderStatusEnum("invalid")).Error;err==nil{t.Fatal("invalid PostgreSQL enum accepted")}
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PostgreSQL enum fixture failed: %v\n%s", err, output)
	}
}
