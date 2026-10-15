package query

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormclause "gorm.io/gorm/clause"
)

func lockingDryRunDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "mysql":
		driver = mysql.New(mysql.Config{DSN: "gorm:gorm@tcp(127.0.0.1:1)/gorm", SkipInitializeWithVersion: true})
	case "postgres":
		driver = postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 user=gorm password=gorm dbname=gorm sslmode=disable"})
	default:
		t.Fatal("unsupported test dialect:", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return db
}

func TestForUpdateSQL(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := lockingDryRunDB(t, dialect)
			for _, enabled := range []bool{false, true} {
				var rows []builderTestModel
				native := NewQuery[builderTestModel](db, context.Background()).
					WhereIf(true, "id = ?", 7).ForUpdateIf(enabled).Build().Find(&rows)
				wrapper := Wrap(&wrapperTestDO{db: db.Model(&builderTestModel{})}).
					WhereRaw("id = ?", 7).ForUpdateIf(enabled).Apply().UnderlyingDB().Find(&rows)
				opt := Query().ForUpdateIf(enabled).Build()
				optionQuery := db.Model(&builderTestModel{}).Clauses(opt.Clauses...).Where("id = ?", 7).Find(&rows)
				literal := MergeQueryOptions(QueryOption{ForUpdate: enabled}, QueryOption{ForUpdate: false})
				literalQuery := db.Model(&builderTestModel{}).Clauses(literal.Clauses...).Where("id = ?", 7).Find(&rows)
				first := FirstQueryOption([]QueryOption{{ForUpdate: enabled}})
				firstQuery := db.Model(&builderTestModel{}).Clauses(first.Clauses...).Where("id = ?", 7).Find(&rows)
				for name, result := range map[string]*gorm.DB{"Query": native, "GenWrap": wrapper, "QueryOpt": optionQuery, "literal option": literalQuery, "first option": firstQuery} {
					sql := result.Statement.SQL.String()
					if result.Error != nil || strings.HasSuffix(sql, "FOR UPDATE") != enabled {
						t.Fatalf("%s locking=%v: %s, %v", name, enabled, sql, result.Error)
					}
					if len(result.Statement.Vars) != 1 || result.Statement.Vars[0] != 7 {
						t.Fatalf("%s lost parameterized filter: %s, %v", name, sql, result.Statement.Vars)
					}
				}
			}
			// false 为不追加，不会清除调用方此前已经设置的锁。
			if sql := NewQuery[builderTestModel](db, context.Background()).ForUpdate().ForUpdateIf(false).ToSQL(); !strings.HasSuffix(sql, "FOR UPDATE") {
				t.Fatal("conditional no-op removed existing lock:", sql)
			}
		})
	}
}

func TestForUpdateKeepsTransactionAndContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := db.AutoMigrate(&builderTestModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&builderTestModel{ID: 1, Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "tenant-context")
	rollback := errors.New("rollback")
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		queries := []*gorm.DB{
			NewQuery[builderTestModel](tx, ctx).ForUpdate().Build(),
			Wrap(&wrapperTestDO{db: tx.Model(&builderTestModel{})}).ForUpdate().Apply().UnderlyingDB(),
		}
		for _, built := range queries {
			if built.Statement.ConnPool != tx.Statement.ConnPool || built.Statement.Context.Value(contextKey{}) != "tenant-context" {
				t.Fatal("locking query lost transaction connection or context")
			}
		}
		var row builderTestModel
		if err := queries[0].First(&row, 1).Error; err != nil {
			return err
		}
		if err := queries[1].Where("id = ?", row.ID).Update("name", "after").Error; err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("transaction error:", err)
	}
	var row builderTestModel
	if err := db.First(&row, 1).Error; err != nil || row.Name != "before" {
		t.Fatalf("locking query escaped rollback: name=%s err=%v", row.Name, err)
	}
}

func TestLockedQueriesBypassCache(t *testing.T) {
	options := map[string]QueryOption{
		"literal lock":      {ForUpdate: true, Cache: &CacheOption{Enable: true, TTL: time.Minute}},
		"lock before cache": Query().ForUpdate().WithCache(time.Minute).Build(),
		"cache before lock": Query().WithCache(time.Minute).ForUpdate().Build(),
		"merged options":    MergeQueryOptions(Query().ForUpdate().Build(), Query().WithCache(time.Minute).Build()),
		"raw shared lock":   Query().Clauses(gormclause.Locking{Strength: "SHARE"}).WithCache(time.Minute).Build(),
		"pointer lock":      Query().Clauses(&gormclause.Locking{Strength: "UPDATE"}).WithCache(time.Minute).Build(),
	}
	db := lockingDryRunDB(t, "mysql")
	built := Wrap(&wrapperTestDO{db: db.Model(&builderTestModel{})}).ForUpdate().Apply().UnderlyingDB()
	options["wrapper lock"] = Query().WithCache(time.Minute).Build().WithLockingFrom(built)
	for name, opt := range options {
		t.Run(name, func(t *testing.T) {
			calls := 0
			fn := func() (int, error) { calls++; return calls, nil }
			for want := 1; want <= 2; want++ {
				got, err := ExecuteQuery(opt, t.Name(), nil, fn)
				if err != nil || got != want {
					t.Fatalf("locked query reused cached value: got=%d want=%d err=%v", got, want, err)
				}
			}
			pageCalls := 0
			for want := 1; want <= 2; want++ {
				list, total, err := ExecutePage(opt, t.Name()+".page", nil, func() ([]int, int64, error) {
					pageCalls++
					return []int{pageCalls}, int64(pageCalls), nil
				})
				if err != nil || len(list) != 1 || list[0] != want || total != int64(want) {
					t.Fatalf("locked page reused cache: %v %d %v", list, total, err)
				}
			}
		})
	}
}

func TestLockedQueriesDoNotMergeConcurrentCalls(t *testing.T) {
	opt := Query().ForUpdate().WithSingleFlight(0).Build()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var workers sync.WaitGroup
	var calls atomic.Int32
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := ExecuteQuery(opt, t.Name(), nil, func() (int, error) {
				calls.Add(1)
				entered <- struct{}{}
				<-release
				return 1, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	// 两个事务都必须独立进入真实查询，而不是共享第一个调用的结果。
	defer workers.Wait()
	defer close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("locked queries were merged: calls=%d", calls.Load())
		}
	}
}

func TestOrdinaryQueriesStillUseCache(t *testing.T) {
	opt := QueryOption{ForUpdate: false, Cache: &CacheOption{Enable: true, TTL: time.Minute}}
	for i := 0; i < 2; i++ {
		got, err := ExecuteQuery(opt, t.Name(), nil, func() (int, error) { return i + 1, nil })
		if err != nil || got != 1 {
			t.Fatalf("ordinary query cache regressed: got=%d err=%v", got, err)
		}
	}
}
