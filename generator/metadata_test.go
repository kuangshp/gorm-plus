package generator

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/migrator"
)

func TestColumnMetadata(t *testing.T) {
	for _, test := range []struct {
		name, typ, full, want    string
		length, precision, scale int64
	}{
		{"mysql unsigned", "bigint", "bigint unsigned", "bigint unsigned", 0, 0, 0},
		{"mysql decimal", "decimal", "decimal(12,4)", "decimal(12,4)", 0, 12, 4},
		{"postgres numeric", "numeric", "", "numeric(18,6)", 0, 18, 6},
		{"postgres varchar", "varchar", "", "varchar(80)", 80, 0, 0},
		{"sqlserver decimal", "", "decimal", "decimal(19,4)", 0, 19, 4},
		{"sqlserver nvarchar", "", "nvarchar", "nvarchar(120)", 120, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			col := migrator.ColumnType{
				NameValue:          sql.NullString{String: "test_column", Valid: true},
				DataTypeValue:      sql.NullString{String: test.typ, Valid: true},
				ColumnTypeValue:    sql.NullString{String: test.full, Valid: test.full != ""},
				LengthValue:        sql.NullInt64{Int64: test.length, Valid: true},
				DecimalSizeValue:   sql.NullInt64{Int64: test.precision, Valid: true},
				ScaleValue:         sql.NullInt64{Int64: test.scale, Valid: true},
				PrimaryKeyValue:    sql.NullBool{Bool: true, Valid: true},
				AutoIncrementValue: sql.NullBool{Bool: true, Valid: true},
				NullableValue:      sql.NullBool{Bool: false, Valid: true},
				CommentValue:       sql.NullString{String: "字段注释", Valid: true},
			}
			got := columnInfo(col)
			if got.Type != test.want || !got.IsKey || got.Extra != "auto_increment" || got.CanNull || got.Comment != "字段注释" {
				t.Fatalf("metadata = %+v", got)
			}
		})
	}
}

func TestSQLiteGeneration(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module generationfixture\n\ngo 1.25.5\n\nrequire github.com/kuangshp/gorm-plus v0.0.0\nreplace github.com/kuangshp/gorm-plus => %s\n", root)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("GENERATOR_TEMPLATE_DIR", filepath.Join(root, "generator", "template"))
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "fixture.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	for _, ddl := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(80) NOT NULL, amount NUMERIC(18,6), rate REAL, enabled BOOL, payload BLOB, attributes JSONB, created_at DATETIME)`,
		`CREATE TABLE memberships (tenant_id INTEGER NOT NULL, user_id INTEGER NOT NULL, name TEXT, PRIMARY KEY (tenant_id,user_id))`,
		`CREATE TABLE ignored (id INTEGER PRIMARY KEY)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	cols, err := getTableColumns(db, "memberships")
	if err != nil || len(cols) != 3 || !cols[0].IsKey || !cols[1].IsKey || cols[0].CanNull {
		t.Fatalf("composite metadata = %+v, %v", cols, err)
	}
	if _, err := getTableColumns(db, "missing_table"); err == nil {
		t.Fatal("missing table must return an error")
	}
	oldLines, oldIndex := inputLines, inputIndex
	t.Cleanup(func() { inputLines, inputIndex = oldLines, oldIndex })
	run := func(table string) error {
		inputLines, inputIndex = []string{table}, 0
		return Generate(&Config{DBType: "sqlite", Database: filepath.Join(dir, "fixture.db"),
			OutPath: "dao", ModelPkgPath: "model", RepoPath: "repository", DtoPath: "dto", VoPath: "vo", APIMapperPath: "mapper",
			Package: "generationfixture", ExcludeTables: []string{"ignored"}})
	}
	if err := run(""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dao/gen.go", "model/items.gen.go", "repository/items_gen.go", "dto/itemsDto.go", "vo/itemsVo.go", "mapper/itemsMapper.go", "repository/memberships_gen.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing output %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "model/ignored.gen.go")); !os.IsNotExist(err) {
		t.Fatal("excluded table generated")
	}
	// 单表增量生成必须保留历史 DAO 与用户扩展文件。
	extension := filepath.Join(dir, "repository/items.go")
	original, _ := os.ReadFile(extension)
	custom := append(original, []byte("\n// retained custom code\n")...)
	if err := os.WriteFile(extension, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := run("notes"); err != nil {
		t.Fatal(err)
	}
	generated, _ := os.ReadFile(filepath.Join(dir, "dao/gen.go"))
	if !strings.Contains(string(generated), "ItemsEntity") || !strings.Contains(string(generated), "NotesEntity") {
		t.Fatal("single-table generation lost history")
	}
	if got, _ := os.ReadFile(extension); string(got) != string(custom) {
		t.Fatal("custom extension overwritten")
	}
	if err := run("missing_table"); err == nil {
		t.Fatal("generation must fail when metadata fails")
	}
	if _, err := os.Stat(filepath.Join(dir, "model/sqlite_sequence.gen.go")); !os.IsNotExist(err) {
		t.Fatal("SQLite system table generated")
	}
	// 执行实际生成的 Repository，验证条件写入后的表级失效。
	if err := os.WriteFile(filepath.Join(dir, "repository/cache_test.go"), []byte(`package repository
import (
 "context"
 "testing"
 "time"
 "errors"
 "generationfixture/dao"
 "generationfixture/model"
 gormplus "github.com/kuangshp/gorm-plus"
 "github.com/kuangshp/gorm-plus/query"
 "gorm.io/driver/sqlite"
 "gorm.io/gorm"
)
func TestConditionalWritesInvalidateTable(t *testing.T) {
 db, err := gorm.Open(sqlite.Open("../fixture.db")); if err != nil {t.Fatal(err)}
 sqlDB, _ := db.DB(); defer sqlDB.Close()
 dao.SetDefault(db)
 ctx := context.Background()
 repo := NewItemsRepository()
 if err := db.Create(&model.ItemsEntity{ID: 1, Name: "before"}).Error; err != nil {t.Fatal(err)}
 cached := query.Query().WithCache(time.Minute).Build()
 check := func(name string, count int64) {
  t.Helper()
  list, err := repo.FindList(ctx, cached); if err != nil || int64(len(list)) != count {t.Fatalf("list=%v err=%v",list,err)}
  total, err := repo.Count(ctx, cached); if err != nil || total != count {t.Fatalf("count=%d err=%v",total,err)}
  exists, err := repo.Exists(ctx, cached); if err != nil || exists != (count > 0) {t.Fatalf("exists=%v err=%v",exists,err)}
  item, err := repo.FindById(ctx, 1, cached)
  if count > 0 {
   if err != nil || item.Name != name || list[0].Name != name {t.Fatalf("stale detail or list: item=%+v list=%v err=%v",item,list,err)}
  } else if !errors.Is(err, gorm.ErrRecordNotFound) {t.Fatalf("deleted detail returned: %+v, %v",item,err)}
 }
 check("before",1)
 where := func(w gormplus.IGenWrapper[dao.IItemsEntityDo]) {w.Where(dao.ItemsEntity.ID.Eq(1))}
 if err := repo.UpdateByWrapper(ctx,where,gormplus.WithUpdateColumns(dao.ItemsEntity.Name.Value("after"))); err != nil {t.Fatal(err)}
 check("after",1)
 if err := repo.DeleteByWrapper(ctx,where); err != nil {t.Fatal(err)}
 check("",0)
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	// 保留已有业务扩展后，WithTx 仍必须返回完整业务接口并保留依赖。
	customSource := strings.Replace(string(custom), "*defaultItemsRepository", "*defaultItemsRepository\n\tdependency *int", 1)
	customSource = strings.Replace(customSource, "iDefaultItemsRepository", "iDefaultItemsRepository\n\tDependency() *int", 1)
	customSource += "\nfunc (r *customerItemsRepository) Dependency() *int { return r.dependency }\n"
	if err := os.WriteFile(extension, []byte(customSource), 0600); err != nil {
		t.Fatal(err)
	}
	txTests, err := os.ReadFile(filepath.Join(root, "generator/testdata/repository_transactions_test.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repository/transactions_test.go"), txTests, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-mod=mod", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code failed compilation: %v\n%s", err, output)
	}
	// 模板错误必须返回到入口，不能只打印后宣称成功。
	brokenTemplates := filepath.Join(dir, "broken_templates")
	if err := os.Mkdir(brokenTemplates, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenTemplates, "repository_gen_template.txt"), []byte("{{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GENERATOR_TEMPLATE_DIR", brokenTemplates)
	if err := run("notes"); err == nil || !strings.Contains(err.Error(), "渲染") {
		t.Fatalf("expected template error, got %v", err)
	}
}

func TestGeneratedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeGeneratedFile(filepath.Join(dir, "missing", "file.go"), "code", false); err == nil {
		t.Fatal("write error ignored")
	}
}
