package dal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// 测试辅助 /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// mockLoader 内存 SQL Loader，用于单元测试
type mockLoader struct {
	mu      sync.RWMutex
	sqls    map[string]string
	cleared bool
}

func newMockLoader(sqls map[string]string) *mockLoader {
	return &mockLoader{sqls: sqls}
}

func (m *mockLoader) Load(file string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sql, ok := m.sqls[file]
	if !ok {
		return "", fmt.Errorf("dal.mockLoader.Load [%s]: file not found", file)
	}

	return sql, nil
}

func (m *mockLoader) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleared = true
}

// setupDB 创建 SQLite 内存数据库，自动建表并插入测试数据
func setupDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	sqls := []string{
		`CREATE TABLE account (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			username   TEXT    NOT NULL,
			status     INTEGER NOT NULL DEFAULT 1,
			balance    REAL    NOT NULL DEFAULT 0,
			deleted_at DATETIME
		)`,
		`CREATE TABLE sys_config (
			key   TEXT NOT NULL,
			value TEXT NOT NULL
		)`,
		`INSERT INTO account (username, status, balance) VALUES ('alice', 1, 100.0)`,
		`INSERT INTO account (username, status, balance) VALUES ('bob',   1, 200.0)`,
		`INSERT INTO account (username, status, balance) VALUES ('carol', 0, 0.0)`,
		`INSERT INTO sys_config (key, value) VALUES ('site_name', 'TestSite')`,
	}

	for _, s := range sqls {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("setup db: %v", err)
		}
	}

	return db
}

// newTestDAL 创建测试用 DAL，loader 由外部传入
func newTestDAL(t *testing.T, loader SQLLoader, opts ...Option) (*DAL, context.Context) {
	t.Helper()

	db := setupDB(t)

	d, err := NewDal(db, loader, opts...)
	if err != nil {
		t.Fatalf("NewDal: %v", err)
	}

	t.Cleanup(func() { d.Close() })

	return d, context.Background()
}

// AccountVO 测试用 VO
type AccountVO struct {
	ID       int64   `gorm:"column:id"`
	Username string  `gorm:"column:username"`
	Status   int     `gorm:"column:status"`
	Balance  float64 `gorm:"column:balance"`
}

// ConfigVO 测试用 VO
type ConfigVO struct {
	Key   string `gorm:"column:key"`
	Value string `gorm:"column:value"`
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// NewDal /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestNewDal_OK(t *testing.T) {
	db := setupDB(t)
	loader := newMockLoader(map[string]string{})

	d, err := NewDal(db, loader)
	if err != nil {
		t.Fatalf("expect no error, got %v", err)
	}
	defer d.Close()
}

func TestNewDal_NilDB(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, err := NewDal(nil, loader)
	if err == nil {
		t.Fatal("expect error when db is nil")
	}
}

func TestNewDal_NilLoader(t *testing.T) {
	db := setupDB(t)
	_, err := NewDal(db, nil)
	if err == nil {
		t.Fatal("expect error when loader is nil")
	}
}

func TestNewWithProvider_OK(t *testing.T) {
	db := setupDB(t)
	loader := newMockLoader(map[string]string{})
	provider := &singleDBProvider{db: db}

	d, err := NewWithProvider(provider, loader)
	if err != nil {
		t.Fatalf("expect no error, got %v", err)
	}
	defer d.Close()
}

func TestNewWithProvider_NilProvider(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, err := NewWithProvider(nil, loader)
	if err == nil {
		t.Fatal("expect error when provider is nil")
	}
}

func TestNewWithProvider_NilLoader(t *testing.T) {
	db := setupDB(t)
	provider := &singleDBProvider{db: db}
	_, err := NewWithProvider(provider, nil)
	if err == nil {
		t.Fatal("expect error when loader is nil")
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// Close //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestClose_Idempotent(t *testing.T) {
	db := setupDB(t)
	loader := newMockLoader(map[string]string{})

	d, err := NewDal(db, loader)
	if err != nil {
		t.Fatal(err)
	}

	// 多次 Close 不应该 panic
	d.Close()
	d.Close()
	d.Close()
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// Preload ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestPreload_OK(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/list.sql": "SELECT * FROM account",
	})
	_, _ = newTestDAL(t, loader)

	if err := Preload("account/list.sql"); err != nil {
		t.Fatalf("Preload: %v", err)
	}
}

func TestPreload_FileNotFound(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, _ = newTestDAL(t, loader)

	if err := Preload("not_exist.sql"); err == nil {
		t.Fatal("expect error for missing file")
	}
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// WithDB ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestWithDB_SwitchesInstance(t *testing.T) {
	// 主库
	loader1 := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader1)

	// 切换到第二个库（同结构，不同数据）
	db2, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	db2.Exec(`CREATE TABLE account (id INTEGER PRIMARY KEY, username TEXT, status INTEGER, balance REAL, deleted_at DATETIME)`)
	db2.Exec(`INSERT INTO account VALUES (99, 'db2_user', 1, 999.0, NULL)`)

	loader2 := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	d2, err := NewDal(db2, loader2)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	ctx2 := WithDB(ctx, d2)

	rows, err := Query[AccountVO](ctx2, "account/list.sql", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 || rows[0].Username != "db2_user" {
		t.Fatalf("expect db2_user from second instance, got %+v", rows)
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// Query /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestQuery_ReturnsRows(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader)

	rows, err := Query[AccountVO](ctx, "account/list.sql", 1)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(rows))
	}
}

func TestQuery_EmptyResult(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader, WithDebug(true)) // debug 开启触发 WARN 日志

	rows, err := Query[AccountVO](ctx, "account/list.sql", 99)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 0 {
		t.Fatalf("expect 0 rows, got %d", len(rows))
	}
}

func TestQuery_LoaderError(t *testing.T) {
	loader := newMockLoader(map[string]string{}) // 没有注册任何 SQL
	_, ctx := newTestDAL(t, loader)

	_, err := Query[AccountVO](ctx, "not_exist.sql", 1)
	if err == nil {
		t.Fatal("expect error for missing SQL file")
	}
}

func TestQuery_NotInitialized(t *testing.T) {
	// 清空全局实例
	defaultDAL = nil

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expect panic when not initialized")
		}
	}()

	Query[AccountVO](context.Background(), "account/list.sql", 1) //nolint
}

////////////////////////////////////////////////////////////////////////////////
///////////////////////////////// QueryOne ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestQueryOne_Found(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	account, err := QueryOne[AccountVO](ctx, "account/find_by_id.sql", 1)
	if err != nil {
		t.Fatal(err)
	}

	if account == nil {
		t.Fatal("expect record, got nil")
	}

	if account.Username != "alice" {
		t.Fatalf("expect alice, got %s", account.Username)
	}
}

func TestQueryOne_NotFound(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	account, err := QueryOne[AccountVO](ctx, "account/find_by_id.sql", 9999)
	if err != nil {
		t.Fatal(err)
	}

	if account != nil {
		t.Fatal("expect nil for not found")
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// QueryNamed //////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestQueryNamed_ReturnsRows(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/search.sql": `SELECT id, username, status, balance FROM account WHERE status = @status`,
	})
	_, ctx := newTestDAL(t, loader)

	rows, err := QueryNamed[AccountVO](ctx, "account/search.sql", map[string]any{
		"status": 1,
	})
	if err != nil {
		t.Fatalf("QueryNamed: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(rows))
	}
}

func TestQueryNamed_EmptyResult(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/search.sql": `SELECT id, username, status, balance FROM account WHERE status = @status`,
	})
	_, ctx := newTestDAL(t, loader)

	rows, err := QueryNamed[AccountVO](ctx, "account/search.sql", map[string]any{
		"status": 99,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 0 {
		t.Fatalf("expect 0 rows, got %d", len(rows))
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////// QueryOneNamed /////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestQueryOneNamed_Found(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/find_by_username.sql": `SELECT id, username, status, balance FROM account WHERE username = @username`,
	})
	_, ctx := newTestDAL(t, loader)

	account, err := QueryOneNamed[AccountVO](ctx, "account/find_by_username.sql", map[string]any{
		"username": "alice",
	})
	if err != nil {
		t.Fatal(err)
	}

	if account == nil || account.Username != "alice" {
		t.Fatalf("expect alice, got %v", account)
	}
}

func TestQueryOneNamed_NotFound(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/find_by_username.sql": `SELECT id, username, status, balance FROM account WHERE username = @username`,
	})
	_, ctx := newTestDAL(t, loader)

	account, err := QueryOneNamed[AccountVO](ctx, "account/find_by_username.sql", map[string]any{
		"username": "no_one",
	})
	if err != nil {
		t.Fatal(err)
	}

	if account != nil {
		t.Fatal("expect nil for not found")
	}
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Exec /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestExec_OK(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql": "UPDATE account SET status = 0 WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	if err := Exec(ctx, "account/disable.sql", 1); err != nil {
		t.Fatalf("Exec: %v", err)
	}
}

func TestExec_LoaderError(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, ctx := newTestDAL(t, loader)

	if err := Exec(ctx, "not_exist.sql", 1); err == nil {
		t.Fatal("expect error for missing file")
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// ExecAffected ////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestExecAffected_HitsRow(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql": "UPDATE account SET status = 0 WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	result, err := ExecAffected(ctx, "account/disable.sql", 1)
	if err != nil {
		t.Fatal(err)
	}

	if result.RowsAffected != 1 {
		t.Fatalf("expect 1 row affected, got %d", result.RowsAffected)
	}
}

func TestExecAffected_NoRowHit(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql": "UPDATE account SET status = 0 WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader, WithDebug(true)) // 开启 debug 触发 WARN

	result, err := ExecAffected(ctx, "account/disable.sql", 9999)
	if err != nil {
		t.Fatal(err)
	}

	if result.RowsAffected != 0 {
		t.Fatalf("expect 0 rows affected, got %d", result.RowsAffected)
	}
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Count ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestCount_WithPositionalArgs(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/count.sql": "SELECT COUNT(*) FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader)

	total, err := Count(ctx, "account/count.sql", 1)
	if err != nil {
		t.Fatal(err)
	}

	if total != 2 {
		t.Fatalf("expect 2, got %d", total)
	}
}

func TestCount_WithNamedArgs(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/count_named.sql": "SELECT COUNT(*) FROM account WHERE status = @status",
	})
	_, ctx := newTestDAL(t, loader)

	total, err := Count(ctx, "account/count_named.sql",
		map[string]any{"status": 1},
	)
	if err != nil {
		t.Fatal(err)
	}

	if total != 2 {
		t.Fatalf("expect 2, got %d", total)
	}
}

////////////////////////////////////////////////////////////////////////////////
///////////////////////////////// QueryPage //////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestQueryPage_FirstPage(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/page.sql":       "SELECT id, username, status, balance FROM account WHERE status = ? LIMIT ? OFFSET ?",
		"account/count_page.sql": "SELECT COUNT(*) FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader)

	result, err := QueryPage[AccountVO](ctx, "account/page.sql",
		[]any{1},     // filter: status = 1
		[]any{10, 0}, // page: LIMIT 10 OFFSET 0
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Total != 2 {
		t.Fatalf("expect total=2, got %d", result.Total)
	}

	if len(result.List) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(result.List))
	}
}

func TestQueryPage_WithLimit(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/page.sql":       "SELECT id, username, status, balance FROM account WHERE status = ? LIMIT ? OFFSET ?",
		"account/count_page.sql": "SELECT COUNT(*) FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader)

	result, err := QueryPage[AccountVO](ctx, "account/page.sql",
		[]any{1},    // filter
		[]any{1, 0}, // LIMIT 1 OFFSET 0
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Total != 2 {
		t.Fatalf("expect total=2, got %d", result.Total)
	}

	if len(result.List) != 1 {
		t.Fatalf("expect 1 row in page, got %d", len(result.List))
	}
}

func TestQueryPage_CountSQLAutoInferred(t *testing.T) {
	// 验证 count SQL 路径推导：account/page.sql → account/count_page.sql
	called := map[string]bool{}

	loader := &trackingLoader{
		sqls: map[string]string{
			"account/page.sql":       "SELECT id, username, status, balance FROM account WHERE status = ? LIMIT ? OFFSET ?",
			"account/count_page.sql": "SELECT COUNT(*) FROM account WHERE status = ?",
		},
		called: called,
	}
	_, ctx := newTestDAL(t, loader)

	QueryPage[AccountVO](ctx, "account/page.sql", []any{1}, []any{10, 0}) //nolint

	if !called["account/count_page.sql"] {
		t.Fatal("expect count_page.sql to be loaded automatically")
	}
}

// trackingLoader 记录哪些 SQL 被加载过
type trackingLoader struct {
	sqls   map[string]string
	called map[string]bool
	mu     sync.Mutex
}

func (l *trackingLoader) Load(file string) (string, error) {
	l.mu.Lock()
	l.called[file] = true
	l.mu.Unlock()

	sql, ok := l.sqls[file]
	if !ok {
		return "", fmt.Errorf("file not found: %s", file)
	}
	return sql, nil
}

func (l *trackingLoader) ClearCache() {}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////// QueryPageNamed ////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestQueryPageNamed_OK(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/page.sql":       "SELECT id, username, status, balance FROM account WHERE status = @status LIMIT @limit OFFSET @offset",
		"account/count_page.sql": "SELECT COUNT(*) FROM account WHERE status = @status",
	})
	_, ctx := newTestDAL(t, loader)

	result, err := QueryPageNamed[AccountVO](ctx, "account/page.sql", map[string]any{
		"status": 1,
		"limit":  10,
		"offset": 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Total != 2 {
		t.Fatalf("expect total=2, got %d", result.Total)
	}

	if len(result.List) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(result.List))
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// WithTx ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestWithTx_CommitOnSuccess(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql":    "UPDATE account SET status = 0 WHERE id = ?",
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	err := WithTx(ctx, func(tx *gorm.DB) error {
		return TxExec(ctx, tx, "account/disable.sql", 1)
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// 验证事务已提交
	account, err := QueryOne[AccountVO](ctx, "account/find_by_id.sql", 1)
	if err != nil || account == nil || account.Status != 0 {
		t.Fatalf("expect status=0 after commit, got %+v", account)
	}
}

func TestWithTx_RollbackOnError(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql":    "UPDATE account SET status = 0 WHERE id = ?",
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	err := WithTx(ctx, func(tx *gorm.DB) error {
		if err := TxExec(ctx, tx, "account/disable.sql", 2); err != nil {
			return err
		}
		return errors.New("intentional rollback")
	})
	if err == nil {
		t.Fatal("expect error to trigger rollback")
	}

	// 验证事务已回滚，bob 的 status 仍为 1
	account, err := QueryOne[AccountVO](ctx, "account/find_by_id.sql", 2)
	if err != nil || account == nil || account.Status != 1 {
		t.Fatalf("expect status=1 after rollback, got %+v", account)
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// TxQuery ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestTxQuery_ReturnsRows(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/lock.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader)

	var rows []AccountVO
	err := WithTx(ctx, func(tx *gorm.DB) error {
		var err error
		rows, err = TxQuery[AccountVO](ctx, tx, "account/lock.sql", 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(rows))
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// TxQueryOne //////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestTxQueryOne_Found(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	var account *AccountVO
	err := WithTx(ctx, func(tx *gorm.DB) error {
		var err error
		account, err = TxQueryOne[AccountVO](ctx, tx, "account/find_by_id.sql", 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if account == nil || account.Username != "alice" {
		t.Fatalf("expect alice, got %+v", account)
	}
}

func TestTxQueryOne_NotFound(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	var account *AccountVO
	err := WithTx(ctx, func(tx *gorm.DB) error {
		var err error
		account, err = TxQueryOne[AccountVO](ctx, tx, "account/find_by_id.sql", 9999)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if account != nil {
		t.Fatal("expect nil for not found")
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////// TxQueryNamed //////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestTxQueryNamed_ReturnsRows(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/search.sql": "SELECT id, username, status, balance FROM account WHERE status = @status",
	})
	_, ctx := newTestDAL(t, loader)

	var rows []AccountVO
	err := WithTx(ctx, func(tx *gorm.DB) error {
		var err error
		rows, err = TxQueryNamed[AccountVO](ctx, tx, "account/search.sql", map[string]any{
			"status": 1,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 {
		t.Fatalf("expect 2 rows, got %d", len(rows))
	}
}

////////////////////////////////////////////////////////////////////////////////
///////////////////////////////// TxCount ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestTxCount_OK(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/count.sql": "SELECT COUNT(*) FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader)

	var total int64
	err := WithTx(ctx, func(tx *gorm.DB) error {
		var err error
		total, err = TxCount(ctx, tx, "account/count.sql", 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if total != 2 {
		t.Fatalf("expect 2, got %d", total)
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// TxExec ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestTxExec_OK(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql":    "UPDATE account SET status = 0 WHERE id = ?",
		"account/find_by_id.sql": "SELECT id, username, status, balance FROM account WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	err := WithTx(ctx, func(tx *gorm.DB) error {
		return TxExec(ctx, tx, "account/disable.sql", 1)
	})
	if err != nil {
		t.Fatal(err)
	}

	account, _ := QueryOne[AccountVO](ctx, "account/find_by_id.sql", 1)
	if account == nil || account.Status != 0 {
		t.Fatalf("expect status=0, got %+v", account)
	}
}

func TestTxExec_LoaderError(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, ctx := newTestDAL(t, loader)

	err := WithTx(ctx, func(tx *gorm.DB) error {
		return TxExec(ctx, tx, "not_exist.sql")
	})
	if err == nil {
		t.Fatal("expect error for missing SQL file")
	}
}

////////////////////////////////////////////////////////////////////////////////
///////////////////////////////// MustExec ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestMustExec_OK(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/disable.sql": "UPDATE account SET status = 0 WHERE id = ?",
	})
	_, ctx := newTestDAL(t, loader)

	// 不应 panic
	MustExec(ctx, "account/disable.sql", 1)
}

func TestMustExec_Panics(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, ctx := newTestDAL(t, loader)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expect panic for missing SQL file")
		}
	}()

	MustExec(ctx, "not_exist.sql")
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////// MustQueryOne /////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestMustQueryOne_Found(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"config/find_by_key.sql": "SELECT key, value FROM sys_config WHERE key = ?",
	})
	_, ctx := newTestDAL(t, loader)

	cfg := MustQueryOne[ConfigVO](ctx, "config/find_by_key.sql", "site_name")
	if cfg.Value != "TestSite" {
		t.Fatalf("expect TestSite, got %s", cfg.Value)
	}
}

func TestMustQueryOne_PanicsOnNotFound(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"config/find_by_key.sql": "SELECT key, value FROM sys_config WHERE key = ?",
	})
	_, ctx := newTestDAL(t, loader)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expect panic when record not found")
		}
	}()

	MustQueryOne[ConfigVO](ctx, "config/find_by_key.sql", "nonexistent_key")
}

func TestMustQueryOne_PanicsOnLoaderError(t *testing.T) {
	loader := newMockLoader(map[string]string{})
	_, ctx := newTestDAL(t, loader)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expect panic for missing SQL file")
		}
	}()

	MustQueryOne[ConfigVO](ctx, "not_exist.sql", "key")
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// Hook /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

type testHook struct {
	beforeCalls []string
	afterCalls  []string
	mu          sync.Mutex
}

func (h *testHook) Before(ctx context.Context, sqlFile string, args []any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.beforeCalls = append(h.beforeCalls, sqlFile)
}

func (h *testHook) After(ctx context.Context, sqlFile string, args []any, cost time.Duration, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterCalls = append(h.afterCalls, sqlFile)
}

func TestWithHook_CalledOnQuery(t *testing.T) {
	hook := &testHook{}
	loader := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader, WithHook(hook))

	Query[AccountVO](ctx, "account/list.sql", 1) //nolint

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if len(hook.beforeCalls) != 1 || hook.beforeCalls[0] != "account/list.sql" {
		t.Fatalf("expect Before called once with account/list.sql, got %v", hook.beforeCalls)
	}

	if len(hook.afterCalls) != 1 || hook.afterCalls[0] != "account/list.sql" {
		t.Fatalf("expect After called once with account/list.sql, got %v", hook.afterCalls)
	}
}

func TestWithHook_MultipleHooksCalledInOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex

	makeHook := func(name string) Hook {
		return &orderHook{name: name, order: &order, mu: &mu}
	}

	loader := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})
	_, ctx := newTestDAL(t, loader, WithHook(makeHook("h1")), WithHook(makeHook("h2")))

	Query[AccountVO](ctx, "account/list.sql", 1) //nolint

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 2 || order[0] != "h1" || order[1] != "h2" {
		t.Fatalf("expect hooks called in order [h1, h2], got %v", order)
	}
}

type orderHook struct {
	name  string
	order *[]string
	mu    *sync.Mutex
}

func (h *orderHook) Before(ctx context.Context, sqlFile string, args []any) {
	h.mu.Lock()
	*h.order = append(*h.order, h.name)
	h.mu.Unlock()
}

func (h *orderHook) After(ctx context.Context, sqlFile string, args []any, cost time.Duration, err error) {
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////// CacheCleanup /////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestWithCacheCleanup_ClearsCache(t *testing.T) {
	loader := newMockLoader(map[string]string{
		"account/list.sql": "SELECT id, username, status, balance FROM account WHERE status = ?",
	})

	db := setupDB(t)
	d, err := NewDal(db, loader, WithCacheCleanup(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// 触发一次加载，让缓存生效
	ctx := context.Background()
	Query[AccountVO](ctx, "account/list.sql", 1) //nolint

	// 等待定时清理触发
	time.Sleep(120 * time.Millisecond)

	if !loader.cleared {
		t.Fatal("expect cache to be cleared by background goroutine")
	}
}

func TestClose_StopsCacheCleanup(t *testing.T) {
	loader := newMockLoader(map[string]string{})

	db := setupDB(t)
	d, err := NewDal(db, loader, WithCacheCleanup(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	d.Close() // 立即关闭

	// 重置 cleared 标志
	loader.cleared = false

	// 等待超过一个清理周期，确认不再触发
	time.Sleep(120 * time.Millisecond)

	if loader.cleared {
		t.Fatal("expect no cache cleanup after Close")
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////// EmbedLoader ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestEmbedLoader_LoadAndCache(t *testing.T) {
	// 用 strings.NewReader 构造一个内存 fs.FS
	fsys := &singleFileFS{name: "test.sql", content: "SELECT 1"}
	loader := NewEmbedLoader(fsys)

	// 第一次加载
	sql1, err := loader.Load("test.sql")
	if err != nil {
		t.Fatal(err)
	}

	// 第二次应命中缓存（修改 fs 内容也不影响）
	fsys.content = "SELECT 2"
	sql2, err := loader.Load("test.sql")
	if err != nil {
		t.Fatal(err)
	}

	if sql1 != sql2 {
		t.Fatalf("expect cache hit: sql1=%s sql2=%s", sql1, sql2)
	}
}

func TestEmbedLoader_ClearCache(t *testing.T) {
	fsys := &singleFileFS{name: "test.sql", content: "SELECT 1"}
	loader := NewEmbedLoader(fsys)

	loader.Load("test.sql") //nolint

	loader.ClearCache()

	// 清空后修改内容，再次加载应读到新内容
	fsys.content = "SELECT 2"
	sql, err := loader.Load("test.sql")
	if err != nil {
		t.Fatal(err)
	}

	if sql != "SELECT 2" {
		t.Fatalf("expect SELECT 2 after cache clear, got %s", sql)
	}
}

func TestEmbedLoader_FileNotFound(t *testing.T) {
	loader := NewEmbedLoader(&singleFileFS{name: "other.sql", content: "SELECT 1"})

	_, err := loader.Load("not_exist.sql")
	if err == nil {
		t.Fatal("expect error for missing file")
	}
}

// singleFileFS 单文件内存 fs.FS，用于测试 EmbedLoader
type singleFileFS struct {
	name    string
	content string
}

func (f *singleFileFS) Open(name string) (fs.File, error) {
	if name != f.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &memFile{content: strings.NewReader(f.content)}, nil
}

type memFile struct {
	content *strings.Reader
}

func (f *memFile) Read(p []byte) (int, error) { return f.content.Read(p) }
func (f *memFile) Close() error               { return nil }
func (f *memFile) Stat() (fs.FileInfo, error) { return nil, nil }
