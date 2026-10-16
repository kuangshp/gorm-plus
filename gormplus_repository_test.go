package gormplus

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func repositoryTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return db
}

func TestRepositoryDefaultAndExplicitDB(t *testing.T) {
	saved := DefaultDB()
	repositoryDefaultDB.Store(nil)
	t.Cleanup(func() { repositoryDefaultDB.Store(saved) })
	if db, err := ResolveRepositoryDB(); db != nil || err != nil {
		t.Fatalf("legacy DAO fallback: %v %v", db, err)
	}
	dir := t.TempDir()
	primary := repositoryTestDB(t, filepath.Join(dir, "primary.db"))
	other := repositoryTestDB(t, filepath.Join(dir, "other.db"))
	Use(primary)
	if db, err := ResolveRepositoryDB(); err != nil || db != primary {
		t.Fatal("default connection lost")
	}
	if db, err := ResolveRepositoryDB(other); err != nil || db != other {
		t.Fatal("explicit connection ignored")
	}
	if DefaultDB() != primary {
		t.Fatal("explicit selection changed default")
	}
	for _, dbs := range [][]*gorm.DB{{nil}, {primary, other}, {new(gorm.DB)}} {
		if _, err := ResolveRepositoryDB(dbs...); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("nil default accepted")
			}
		}()
		Use(nil)
	}()
	if err := Transaction(primary, func(tx *gorm.DB) error {
		defer func() {
			if recover() == nil {
				t.Error("transaction registered as global default")
			}
		}()
		Use(tx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if DefaultDB() != primary {
		t.Fatal("invalid default replaced working connection")
	}
}

func TestRepositoryCacheDataSourceScope(t *testing.T) {
	dir := t.TempDir()
	a := repositoryTestDB(t, filepath.Join(dir, "a.db"))
	b := repositoryTestDB(t, filepath.Join(dir, "b.db"))
	same := repositoryTestDB(t, filepath.Join(dir, "a.db"))
	key := RepositoryCacheName(a, "items.FindById")
	if key == RepositoryCacheName(b, "items.FindById") || key != RepositoryCacheName(same, "items.FindById") {
		t.Fatal("data sources not isolated or stable")
	}
	if strings.Contains(key, dir) {
		t.Fatal("connection details exposed in key")
	}
	if key != RepositoryCacheName(a.Session(&gorm.Session{PrepareStmt: true}), "items.FindById") {
		t.Fatal("prepared session changed namespace")
	}
	if err := Transaction(a, func(tx *gorm.DB) error {
		if key != RepositoryCacheName(tx, "items.FindById") {
			t.Fatal("transaction changed namespace")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	opt := query.Query().WithCache(time.Minute).WithSingleFlight(time.Minute).Build()
	if got := RepositoryQueryOptions(a, opt); got.Cache == nil || got.SF == nil {
		t.Fatal("file database lost cache options")
	}
	memory := repositoryTestDB(t, ":memory:")
	if got := RepositoryQueryOptions(memory, opt); got.Cache != nil || got.SF != nil {
		t.Fatal("anonymous memory database shared cache")
	}
	if opt.Cache == nil || opt.SF == nil {
		t.Fatal("caller options mutated")
	}
}

func TestRepositoryDefaultDAORegistration(t *testing.T) {
	saved := DefaultDB()
	repositoryDefaults.Lock()
	savedInitializers := repositoryDefaults.initializers
	repositoryDefaults.initializers = make(map[string]func(*gorm.DB))
	repositoryDefaults.Unlock()
	repositoryDefaultDB.Store(nil)
	t.Cleanup(func() {
		repositoryDefaults.Lock()
		repositoryDefaults.initializers = savedInitializers
		repositoryDefaultDB.Store(saved)
		repositoryDefaults.Unlock()
	})
	db := repositoryTestDB(t, filepath.Join(t.TempDir(), "default.db"))
	calls := 0
	RegisterRepositoryDefault("fixture/dao", func(got *gorm.DB) {
		calls++
		if got != db {
			t.Error("wrong DAO connection")
		}
	})
	RegisterRepositoryDefault("fixture/dao", func(*gorm.DB) { t.Error("duplicate DAO initializer") })
	if calls != 0 {
		t.Fatal("initialized before Use")
	}
	Use(db)
	if calls != 1 {
		t.Fatalf("DAO initialized %d times", calls)
	}
	late := 0
	RegisterRepositoryDefault("late/dao", func(got *gorm.DB) {
		late++
		if got != db {
			t.Error("late DAO connection lost")
		}
	})
	if late != 1 {
		t.Fatal("late registration ignored default")
	}
}
