package gormplus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"gorm.io/gorm"
)

// ErrUnmanagedTransaction 表示事务未通过本库入口创建，无法跟踪提交和缓存失效。
var ErrUnmanagedTransaction = errors.New("事务请通过 gormplus.Transaction、TransactionCtx、TransactionAs 或 TransactionAsCtx 创建")

type transactionScopeKey struct{}

// TransactionScope 跟踪一次事务或嵌套保存点的生命周期，供生成的 Repository 使用。
// 不替代数据库事务；事务回调中不要手动 Commit/Rollback，也不要并发使用事务连接。
type TransactionScope struct {
	mu        sync.Mutex
	pool      gorm.ConnPool
	parent    *TransactionScope
	closed    bool
	committed bool
	prefixes  map[string]struct{}
}

// Check 拒绝在提交、回滚或退出保存点之后复用事务仓储。
func (s *TransactionScope) Check() error {
	if s == nil {
		return ErrUnmanagedTransaction
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return sql.ErrTxDone
	}
	return nil
}

// InvalidateTable 在提交后按表失效缓存；同一事务内重复调用会合并。
func (s *TransactionScope) InvalidateTable(table string) {
	prefix := table + "."
	s.mu.Lock()
	if !s.closed {
		if s.prefixes == nil {
			s.prefixes = map[string]struct{}{}
		}
		s.prefixes[prefix] = struct{}{}
		s.mu.Unlock()
		return
	}
	committed, parent := s.committed, s.parent
	s.mu.Unlock()
	if committed {
		if parent != nil {
			parent.InvalidateTable(table)
		} else {
			SFInvalidatePrefix(prefix)
		}
	}
}

// RepositoryTransactionScope 校验事务来源和连接，普通连接不会被当成事务绑定。
func RepositoryTransactionScope(db *gorm.DB) (*TransactionScope, error) {
	if db == nil || db.Statement == nil || db.Statement.Context == nil {
		return nil, gorm.ErrInvalidTransaction
	}
	if db.Error != nil {
		return nil, db.Error
	}
	scope, _ := db.Statement.Context.Value(transactionScopeKey{}).(*TransactionScope)
	if scope == nil {
		return nil, ErrUnmanagedTransaction
	}
	a, b := reflect.ValueOf(scope.pool), reflect.ValueOf(db.Statement.ConnPool)
	if !a.IsValid() || !b.IsValid() || a.Type() != b.Type() || !a.Comparable() || !a.Equal(b) {
		return nil, gorm.ErrInvalidTransaction
	}
	return scope, scope.Check()
}

func (s *TransactionScope) finish(publish bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed, s.committed = true, publish
	prefixes := s.prefixes
	s.prefixes = nil
	s.mu.Unlock()
	if !publish {
		return
	}
	if s.parent != nil {
		for prefix := range prefixes {
			s.parent.InvalidateTable(prefix[:len(prefix)-1])
		}
		return
	}
	names := make([]string, 0, len(prefixes))
	for prefix := range prefixes {
		names = append(names, prefix)
	}
	SFInvalidatePrefixes(names)
}

func transactionWithScope(db *gorm.DB, fn func(*gorm.DB) error, opts ...*sql.TxOptions) (err error) {
	if db == nil || db.Statement == nil || db.Statement.Context == nil || fn == nil {
		return fmt.Errorf("事务数据库和回调不能为空")
	}
	if db.Error != nil {
		return db.Error
	}
	var parent *TransactionScope
	if _, inTransaction := db.Statement.ConnPool.(gorm.TxCommitter); inTransaction {
		parent, err = RepositoryTransactionScope(db)
		if err != nil {
			return err
		}
		if db.DisableNestedTransaction {
			return fmt.Errorf("已关闭嵌套保存点，不能再嵌套调用事务入口")
		}
	}
	var scope *TransactionScope
	callbackSucceeded := false
	completed := false
	defer func() {
		// panic 由 GORM 回滚；这里只清理状态，不吞掉 panic。
		if scope != nil && !completed {
			scope.finish(false)
		}
	}()
	err = db.Transaction(func(tx *gorm.DB) error {
		scope = &TransactionScope{pool: tx.Statement.ConnPool, parent: parent}
		ctx := context.WithValue(tx.Statement.Context, transactionScopeKey{}, scope)
		callbackErr := fn(tx.WithContext(ctx))
		callbackSucceeded = callbackErr == nil
		return callbackErr
	}, opts...)
	if scope != nil {
		// 回调成功但 Commit 报错时，提交结果可能不确定，保守失效缓存。
		scope.finish(err == nil || callbackSucceeded)
	}
	completed = true
	return err
}

func transactionContext(ctx context.Context, db *gorm.DB) (context.Context, error) {
	if ctx == nil || db == nil || db.Statement == nil || db.Statement.Context == nil {
		return nil, fmt.Errorf("事务 context 和数据库不能为空")
	}
	if scope, ok := db.Statement.Context.Value(transactionScopeKey{}).(*TransactionScope); ok {
		ctx = context.WithValue(ctx, transactionScopeKey{}, scope)
	}
	return ctx, nil
}
