package generator

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresDSNEscaping(t *testing.T) {
	for _, password := range []string{"", "ordinary", "has spaces", `single'quote\\backslash`, "x dbname=other"} {
		t.Run(password, func(t *testing.T) {
			cfg := &Config{Host: "127.0.0.1", Port: 15432, Username: "test user", Password: password, Database: "target db"}
			dsn, err := DBTypePostgres.BuildDSN(cfg)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Host != cfg.Host || int(parsed.Port) != cfg.Port || parsed.User != cfg.Username || parsed.Password != cfg.Password || parsed.Database != cfg.Database {
				t.Fatal("DSN values were changed or interpreted as other settings")
			}
			tz := parsed.RuntimeParams["timezone"]
			if tz == "" {
				tz = parsed.RuntimeParams["TimeZone"]
			}
			if parsed.TLSConfig != nil || tz != "Asia/Shanghai" {
				t.Fatal("connection options lost")
			}
		})
	}
}

// 表结构读取会把 QueryExecMode 放到参数首位，它不能参与 $n 的编号。
func TestPostgresMetadataPlaceholder(t *testing.T) {
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 port=1 user=test dbname=test sslmode=disable"), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var rows []struct{ ID int64 }
	built := db.Table("items").Limit(1).Scopes(func(q *gorm.DB) *gorm.DB {
		q.Statement.Vars = append([]any{pgx.QueryExecModeSimpleProtocol}, q.Statement.Vars...)
		return q
	}).Find(&rows)
	if built.Error != nil || built.Statement.SQL.String() != `SELECT * FROM "items" LIMIT $1` {
		t.Fatalf("invalid metadata placeholder: %s %v", built.Statement.SQL.String(), built.Error)
	}
	if len(built.Statement.Vars) != 2 || built.Statement.Vars[1] != 1 {
		t.Fatalf("metadata bind values=%v", built.Statement.Vars)
	}
}
