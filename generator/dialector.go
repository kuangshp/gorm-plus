package generator

import (
	"fmt"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

// DBType 数据库类型枚举,YAML 里用对应字符串(大小写不敏感)。
//
// 使用示例(generator.yaml):
//
//	db_type: mysql       # 或 postgres / sqlite / sqlserver
type DBType string

const (
	DBTypeMySQL     DBType = "mysql"
	DBTypePostgres  DBType = "postgres"
	DBTypeSQLite    DBType = "sqlite"
	DBTypeSQLServer DBType = "sqlserver"
)

// allDBTypes 仅用于错误信息展示。
var allDBTypes = []DBType{DBTypeMySQL, DBTypePostgres, DBTypeSQLite, DBTypeSQLServer}

// Normalize 把任意大小写 / 别名归一化为标准 DBType 值。
// 支持的别名:
//   - "mysql"
//   - "postgres" / "postgresql" / "pgsql" / "pg"
//   - "sqlite" / "sqlite3"
//   - "sqlserver" / "mssql"
func (t DBType) Normalize() (DBType, error) {
	s := strings.ToLower(strings.TrimSpace(string(t)))
	switch s {
	case "mysql":
		return DBTypeMySQL, nil
	case "postgres", "postgresql", "pgsql", "pg":
		return DBTypePostgres, nil
	case "sqlite", "sqlite3":
		return DBTypeSQLite, nil
	case "sqlserver", "mssql":
		return DBTypeSQLServer, nil
	default:
		names := make([]string, 0, len(allDBTypes))
		for _, x := range allDBTypes {
			names = append(names, string(x))
		}
		return "", fmt.Errorf("不支持的 db_type=%q,可选值:%s",
			t, strings.Join(names, " / "))
	}
}

// BuildDSN 按数据库类型拼装 DSN。
// 对 SQLite,Database 字段直接作为文件路径(如 "./data.db" 或 ":memory:"),
// 其他字段被忽略。
func (t DBType) BuildDSN(cfg *Config) (string, error) {
	switch t {
	case DBTypeMySQL:
		return fmt.Sprintf("%s:%s@(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database), nil

	case DBTypePostgres:
		// host=... port=... user=... password=... dbname=... sslmode=disable
		port := cfg.Port
		if port == 0 {
			port = 5432
		}
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=Asia/Shanghai",
			cfg.Host, port, cfg.Username, cfg.Password, cfg.Database), nil

	case DBTypeSQLite:
		// SQLite:Database 字段就是文件路径
		if cfg.Database == "" {
			return "", fmt.Errorf("sqlite 必须在 database 字段填写文件路径")
		}
		return cfg.Database, nil

	case DBTypeSQLServer:
		// sqlserver://user:pass@host:port?database=name
		port := cfg.Port
		if port == 0 {
			port = 1433
		}
		return fmt.Sprintf("sqlserver://%s:%s@%s:%d?database=%s",
			cfg.Username, cfg.Password, cfg.Host, port, cfg.Database), nil

	default:
		return "", fmt.Errorf("BuildDSN 未实现 db_type=%q", t)
	}
}

// Dialector 返回对应数据库的 gorm.Dialector。
// 调用前应该先 Normalize() 一次,确保 t 是合法枚举值。
func (t DBType) Dialector(dsn string) (gorm.Dialector, error) {
	switch t {
	case DBTypeMySQL:
		return mysql.Open(dsn), nil
	case DBTypePostgres:
		return postgres.Open(dsn), nil
	case DBTypeSQLite:
		return sqlite.Open(dsn), nil
	case DBTypeSQLServer:
		return sqlserver.Open(dsn), nil
	default:
		return nil, fmt.Errorf("Dialector 未实现 db_type=%q", t)
	}
}

// OpenDB 一站式打开数据库连接:归一化 db_type → 拼 DSN → 建 dialector → gorm.Open。
//
// 返回的 *gorm.DB 用于代码生成器读取表元数据,生成完毕后可由调用方关闭。
func OpenDB(cfg *Config) (*gorm.DB, error) {
	t, err := DBType(cfg.DBType).Normalize()
	if err != nil {
		return nil, err
	}

	dsn, err := t.BuildDSN(cfg)
	if err != nil {
		return nil, err
	}

	dialector, err := t.Dialector(dsn)
	if err != nil {
		return nil, err
	}

	return gorm.Open(dialector)
}
