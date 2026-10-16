package generator

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// PostgreSQL 集成测试使用独立临时数据库，需要测试账号有 CREATEDB 权限。
// 未设置时跳过，不连接开发或生产配置。
func TestPostgresGeneration(t *testing.T) {
	if os.Getenv("GORMPLUS_TEST_POSTGRES_DSN") == "" {
		t.Skip("设置 GORMPLUS_TEST_POSTGRES_DSN（PostgreSQL URL）运行真实数据库兼容测试")
	}
	testDatabaseGeneration(t, "postgres")
}

func generationTestDatabase(t *testing.T, dir, dialect string) (*Config, *gorm.DB) {
	t.Helper()
	cfg := &Config{DBType: dialect, Database: filepath.Join(dir, "fixture.db")}
	// 子进程仅使用本测试传入的地址，不继承其他用例的数据库。
	t.Setenv("GORMPLUS_FIXTURE_POSTGRES_DSN", "")
	if dialect == "postgres" {
		source, err := url.Parse(os.Getenv("GORMPLUS_TEST_POSTGRES_DSN"))
		if err != nil || source.Hostname() == "" || source.User == nil || !(source.Scheme == "postgres" || source.Scheme == "postgresql") {
			t.Fatal("GORMPLUS_TEST_POSTGRES_DSN 必须为带用户名的 PostgreSQL URL")
		}
		admin, err := gorm.Open(postgres.Open(source.String()))
		if err != nil {
			t.Fatal(err)
		}
		adminConn, err := admin.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { adminConn.Close() })
		cfg.Host, cfg.Username = source.Hostname(), source.User.Username()
		cfg.Password, _ = source.User.Password()
		cfg.Port = 5432
		if source.Port() != "" {
			cfg.Port, err = strconv.Atoi(source.Port())
			if err != nil {
				t.Fatal(err)
			}
		}
		cfg.Database = fmt.Sprintf("gormplus_test_%d_%d", os.Getpid(), time.Now().UnixNano())
		if err := admin.Exec(`CREATE DATABASE "` + cfg.Database + `"`).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := admin.Exec(`DROP DATABASE "` + cfg.Database + `"`).Error; err != nil {
				t.Errorf("清理临时数据库: %v", err)
			}
		})
		fixtureURL := &url.URL{Scheme: "postgres", Host: source.Host, User: source.User, Path: "/" + cfg.Database}
		fixtureURL.RawQuery = "sslmode=disable"
		t.Setenv("GORMPLUS_FIXTURE_POSTGRES_DSN", fixtureURL.String())
	}
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var version string
	sqlVersion := "SELECT sqlite_version()"
	if dialect == "postgres" {
		sqlVersion = "SELECT version()"
	}
	if err := db.Raw(sqlVersion).Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("实际数据库：%s %s", dialect, version)
	if dialect == "postgres" {
		var actual string
		if err := db.Raw("SELECT current_database()").Scan(&actual).Error; err != nil {
			t.Fatal(err)
		}
		if actual != cfg.Database {
			t.Fatalf("DSN selected %q, expected isolated test database %q", actual, cfg.Database)
		}
	}
	for _, ddl := range generationTestDDL(dialect) {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if dialect == "postgres" {
		for _, ddl := range []string{
			"COMMENT ON TABLE items IS '兼容测试商品'",
			"COMMENT ON COLUMN items.name IS '商品名称'",
		} {
			if err := db.Exec(ddl).Error; err != nil {
				t.Fatal(err)
			}
		}
		if got := getTableComment(db, "items"); got != "兼容测试商品" {
			t.Fatalf("table comment=%q", got)
		}
		cols, err := getTableColumns(db, "items")
		if err != nil {
			t.Fatal(err)
		}
		byName := map[string]ColumnInfo{}
		for _, col := range cols {
			byName[col.Name] = col
		}
		if !byName["id"].IsKey || byName["id"].Extra != "auto_increment" {
			t.Fatalf("serial metadata: %+v", byName["id"])
		}
		if byName["amount"].Type != "numeric(18,6)" {
			t.Fatalf("numeric metadata: %+v", byName["amount"])
		}
		if !strings.Contains(byName["name"].Type, "varchar") || byName["name"].Comment != "商品名称" {
			t.Fatalf("varchar metadata: %+v", byName["name"])
		}
	}
	return cfg, db
}

func generationTestDDL(dialect string) []string {
	if dialect == "postgres" {
		return []string{
			`CREATE TABLE items (id BIGSERIAL PRIMARY KEY, name VARCHAR(80) NOT NULL, amount NUMERIC(18,6), rate REAL, enabled BOOL, payload BYTEA, attributes JSONB, created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ)`,
			`CREATE TABLE memberships (tenant_id BIGINT NOT NULL, user_id BIGINT NOT NULL, name TEXT, PRIMARY KEY (tenant_id,user_id))`,
			`CREATE TABLE ignored (id BIGINT PRIMARY KEY)`,
		}
	}
	return []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(80) NOT NULL, amount NUMERIC(18,6), rate REAL, enabled BOOL, payload BLOB, attributes JSONB, created_at DATETIME, deleted_at DATETIME)`,
		`CREATE TABLE memberships (tenant_id INTEGER NOT NULL, user_id INTEGER NOT NULL, name TEXT, PRIMARY KEY (tenant_id,user_id))`,
		`CREATE TABLE ignored (id INTEGER PRIMARY KEY)`,
	}
}
