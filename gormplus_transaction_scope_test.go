package gormplus

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gen"
	"gorm.io/gorm"
)

type scopeCache struct{ prefixes []string }

func (*scopeCache) Get(string) (any, bool)            { return nil, false }
func (*scopeCache) Set(string, any, time.Duration)    {}
func (*scopeCache) Del(string)                        {}
func (c *scopeCache) DelByPrefixes(prefixes []string) { c.prefixes = append(c.prefixes, prefixes...) }

func scopeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTransactionScopeCommitRollbackAndPanic(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			db := scopeDB(t)
			cache := &scopeCache{}
			ForceReplaceCache(cache)
			t.Cleanup(func() { ForceReplaceCache(NewMemoryCache()) })
			abort := errors.New("abort")
			var scope *TransactionScope
			var err error
			func() {
				defer func() {
					got := recover()
					if outcome == "panic" && got != abort {
						t.Fatalf("panic lost: %v", got)
					}
					if outcome != "panic" && got != nil {
						panic(got)
					}
				}()
				err = Transaction(db, func(tx *gorm.DB) error {
					var bindErr error
					scope, bindErr = RepositoryTransactionScope(tx)
					if bindErr != nil {
						return bindErr
					}
					scope.InvalidateTable("users")
					scope.InvalidateTable("users")
					scope.InvalidateTable("orders")
					if len(cache.prefixes) != 0 {
						t.Fatal("invalidated before commit")
					}
					if err := tx.Exec("INSERT INTO records(id) VALUES(1)").Error; err != nil {
						return err
					}
					if outcome == "panic" {
						panic(abort)
					}
					if outcome == "rollback" {
						return abort
					}
					return nil
				})
			}()
			if outcome == "rollback" && !errors.Is(err, abort) || outcome == "commit" && err != nil {
				t.Fatal(err)
			}
			if err := scope.Check(); !errors.Is(err, sql.ErrTxDone) {
				t.Fatalf("scope still open: %v", err)
			}
			if outcome == "commit" {
				sort.Strings(cache.prefixes)
				if !reflect.DeepEqual(cache.prefixes, []string{"sf:orders.", "sf:users."}) {
					t.Fatalf("deduplicated invalidations=%v", cache.prefixes)
				}
			} else if len(cache.prefixes) != 0 {
				t.Fatalf("rollback invalidated cache: %v", cache.prefixes)
			}
			var n int64
			if err := db.Table("records").Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if outcome == "commit" {
				want = 1
			}
			if n != want {
				t.Fatalf("rows=%d want=%d", n, want)
			}
		})
	}
}

func TestNestedTransactionScopes(t *testing.T) {
	for _, outerRollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[outerRollback], func(t *testing.T) {
			db := scopeDB(t)
			cache := &scopeCache{}
			ForceReplaceCache(cache)
			t.Cleanup(func() { ForceReplaceCache(NewMemoryCache()) })
			abort := errors.New("abort")
			type key struct{}
			var child *TransactionScope
			err := TransactionCtx(context.Background(), db, func(outer *gorm.DB) error {
				scope, err := RepositoryTransactionScope(outer)
				if err != nil {
					return err
				}
				scope.InvalidateTable("parent")
				// 新请求上下文不能丢掉父级 scope，嵌套成功只合并到父级。
				err = TransactionCtx(context.WithValue(context.Background(), key{}, "trace"), outer, func(inner *gorm.DB) error {
					if inner.Statement.Context.Value(key{}) != "trace" {
						t.Fatal("context lost")
					}
					child, err = RepositoryTransactionScope(inner)
					if err != nil {
						return err
					}
					child.InvalidateTable("child")
					return inner.Exec("INSERT INTO records(id) VALUES(1)").Error
				})
				if err != nil {
					return err
				}
				if err := child.Check(); !errors.Is(err, sql.ErrTxDone) {
					t.Fatalf("child scope still open: %v", err)
				}
				err = Transaction(outer, func(inner *gorm.DB) error {
					child, err := RepositoryTransactionScope(inner)
					if err != nil {
						return err
					}
					child.InvalidateTable("rolled_back_child")
					if err := inner.Exec("INSERT INTO records(id) VALUES(2)").Error; err != nil {
						return err
					}
					return abort
				})
				if !errors.Is(err, abort) {
					t.Fatalf("child rollback: %v", err)
				}
				if len(cache.prefixes) != 0 {
					t.Fatalf("child published before outer commit: %v", cache.prefixes)
				}
				if outerRollback {
					return abort
				}
				return nil
			})
			if outerRollback && !errors.Is(err, abort) || !outerRollback && err != nil {
				t.Fatal(err)
			}
			if outerRollback {
				if len(cache.prefixes) != 0 {
					t.Fatal(cache.prefixes)
				}
			} else {
				sort.Strings(cache.prefixes)
				if !reflect.DeepEqual(cache.prefixes, []string{"sf:child.", "sf:parent."}) {
					t.Fatal(cache.prefixes)
				}
			}
			var n int64
			if err := db.Table("records").Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if outerRollback {
				want = 0
			}
			if n != want {
				t.Fatalf("nested rows=%d want=%d", n, want)
			}
		})
	}
}

func TestRepositoryTransactionScopeRejectsInvalidBinding(t *testing.T) {
	db := scopeDB(t)
	if _, err := RepositoryTransactionScope(nil); err == nil {
		t.Fatal("nil accepted")
	}
	if _, err := RepositoryTransactionScope(db); !errors.Is(err, ErrUnmanagedTransaction) {
		t.Fatal(err)
	}
	raw := db.Begin()
	if _, err := RepositoryTransactionScope(raw); !errors.Is(err, ErrUnmanagedTransaction) {
		t.Fatal(err)
	}
	if err := Transaction(raw, func(*gorm.DB) error { return nil }); !errors.Is(err, ErrUnmanagedTransaction) {
		t.Fatal(err)
	}
	raw.Rollback()
	var escaped *gorm.DB
	err := Transaction(db, func(tx *gorm.DB) error {
		escaped = tx
		// 即使带了 scope，换成另一条普通连接也不能绑定。
		other := db.WithContext(tx.Statement.Context)
		if _, err := RepositoryTransactionScope(other); !errors.Is(err, gorm.ErrInvalidTransaction) {
			t.Fatalf("wrong pool accepted: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RepositoryTransactionScope(escaped); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("closed accepted: %v", err)
	}
	if err := Transaction(nil, func(*gorm.DB) error { return nil }); err == nil {
		t.Fatal("nil db accepted")
	}
	if err := Transaction(db, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := TransactionAs(db, func(tx *gorm.DB, _ ...gen.DOOption) *gorm.DB { return tx }, nil); err == nil {
		t.Fatal("nil typed callback accepted")
	}
	if err := TransactionAsCtx(context.Background(), db, (func(*gorm.DB, ...gen.DOOption) *gorm.DB)(nil), func(*gorm.DB) error { return nil }); err == nil {
		t.Fatal("nil converter accepted")
	}
}
