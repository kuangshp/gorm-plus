// Package dal 提供基于 GORM + SQL 文件的轻量级 DAL（Data Access Layer）。
//
// # 特性
//
//   - SQL 文件化管理，业务逻辑与 SQL 分离
//   - 泛型查询，无需手动类型断言
//   - 支持位置参数（?）和命名参数（@name）
//   - 事务支持，含 Debug 日志和 Hook
//   - 分页查询，count SQL 自动推导
//   - SQL 缓存 + singleflight 防击穿
//   - 可插拔 Hook（监控、链路追踪等）
//   - 定时自动清理缓存，防止内存无限增长
//   - 多数据源支持，通过 context 切换，调用方无感知
//
// # 推荐目录结构
//
//	query/dal/
//	├── dal.go        ← 本文件（dal 库）
//	├── init.go       ← 调用方初始化（embed 声明必须在此文件）
//	└── rawsql/
//	    ├── account/
//	    │   ├── list.sql
//	    │   ├── page.sql
//	    │   ├── count_page.sql
//	    │   └── find_by_id.sql
//	    └── order/
//	        ├── page.sql
//	        └── count_page.sql
//
// # 初始化（init.go，调用方编写）
//
//	package dal
//
//	import (
//	    "embed"
//	    "io/fs"
//	    "gorm.io/gorm"
//	)
//
//	//go:embed rawsql
//	var SQLFS embed.FS
//
//	func Init(db *gorm.DB) {
//	    sub, _ := fs.Sub(SQLFS, "rawsql")
//	    dal.NewDal(
//	        db,
//	        dal.NewEmbedLoader(sub),
//	        dal.WithDebug(true),
//	        dal.WithCacheCleanup(30*time.Minute),
//	    )
//	    // 或者直接传 SQLFS，dal 内部自动 Sub
//	    // dal.NewDal(db, dal.NewEmbedLoader(dal.SQLFS), dal.WithDebug(true))
//	}
//
// # 单数据源使用（绝大多数场景）
//
//	rows, err  := dal.Query[AccountVO](ctx, "account/list.sql", 1, 10, 0)
//	one, err   := dal.QueryOne[AccountVO](ctx, "account/find_by_id.sql", 123)
//	page, err  := dal.QueryPage[AccountVO](ctx, "account/page.sql", []any{1}, []any{10, 0})
//
// # 多数据源使用
//
//	// 初始化第二个数据源
//	reportDAL := dal.NewInstance(reportDB, dal.NewEmbedLoader(sub))
//
//	// 在请求入口注入，后续调用写法完全不变
//	ctx = dal.WithDB(ctx, reportDAL)
//	rows, err := dal.Query[ReportVO](ctx, "report/list.sql", 1, 10, 0)
package dal

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// DB Provider ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// DBProvider 数据库提供器接口
//
// 通过实现此接口可以支持：
//   - 单库
//   - 多库
//   - 读写分离
//   - 多租户
//   - 分库分表
type DBProvider interface {
	Get(ctx context.Context) *gorm.DB
}

// singleDBProvider 单数据库实现（包内私有）
type singleDBProvider struct {
	db *gorm.DB
}

func (p *singleDBProvider) Get(ctx context.Context) *gorm.DB {
	return p.db.WithContext(ctx)
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// SQL Loader ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// SQLLoader SQL 文件加载器接口
//
// 内置实现：EmbedLoader，基于 fs.FS（embed.FS）。
// 也可自行实现，例如从数据库、网络加载 SQL。
type SQLLoader interface {
	Load(file string) (string, error)
	ClearCache()
}

// EmbedLoader 基于 fs.FS 的 SQL Loader
//
// 适用场景：SQL 文件通过 //go:embed 在编译期打包进二进制，
// 生产部署只需一个可执行文件，无需上传任何 .sql 文件。
//
// 使用示例：
//
//	//go:embed rawsql
//	var SQLFS embed.FS
//
//	// 推荐：fs.Sub 去掉顶层目录前缀，调用时路径更简洁
//	sub, _ := fs.Sub(SQLFS, "rawsql")
//	dal.NewDal(db, dal.NewEmbedLoader(sub))
//
//	// 调用时只需相对路径
//	dal.Query[UserVO](ctx, "account/list.sql", args...)
type EmbedLoader struct {
	fs    fs.FS
	cache sync.Map
	group singleflight.Group
}

// NewEmbedLoader 创建基于 fs.FS 的 Loader
func NewEmbedLoader(fsys fs.FS) *EmbedLoader {
	return &EmbedLoader{fs: fsys}
}

// Load 从 embed.FS 加载 SQL 文件，自动缓存、并发安全、singleflight 防击穿
func (l *EmbedLoader) Load(file string) (string, error) {
	file = filepath.ToSlash(file)

	if v, ok := l.cache.Load(file); ok {
		return v.(string), nil
	}

	v, err, _ := l.group.Do(file, func() (interface{}, error) {
		b, err := fs.ReadFile(l.fs, file)
		if err != nil {
			return nil, fmt.Errorf("dal.EmbedLoader.Load [%s]: %w", file, err)
		}

		sqlText := strings.TrimSpace(string(b))
		l.cache.Store(file, sqlText)
		return sqlText, nil
	})

	if err != nil {
		return "", err
	}

	return v.(string), nil
}

// ClearCache 清空所有已缓存的 SQL
func (l *EmbedLoader) ClearCache() {
	l.cache.Range(func(k, _ any) bool {
		l.cache.Delete(k)
		return true
	})
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Hook ///////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// Hook DAL 生命周期钩子
//
// 可用于：
//   - 慢 SQL 监控
//   - Prometheus 指标采集
//   - 链路追踪（OpenTelemetry）
//   - 自定义日志上报
//
// 示例（慢 SQL 告警）：
//
//	type SlowQueryHook struct {
//	    Threshold time.Duration
//	}
//
//	func (h *SlowQueryHook) Before(ctx context.Context, sqlFile string, args []any) {}
//
//	func (h *SlowQueryHook) After(
//	    ctx context.Context,
//	    sqlFile string,
//	    args []any,
//	    cost time.Duration,
//	    err error,
//	) {
//	    if cost > h.Threshold {
//	        log.Printf("[SLOW SQL] file=%s cost=%s", sqlFile, cost)
//	    }
//	}
//
//	dal.NewDal(db, loader, dal.WithHook(&SlowQueryHook{Threshold: 500*time.Millisecond}))
type Hook interface {
	Before(ctx context.Context, sqlFile string, args []any)
	After(ctx context.Context, sqlFile string, args []any, cost time.Duration, err error)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// Options /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

type options struct {
	debug        bool
	hooks        []Hook
	cacheCleanup time.Duration
}

// Option DAL 配置项
type Option func(*options)

// WithDebug 开启 Debug 日志
//
// 开启后每条 SQL 执行都会打印：文件路径、耗时、SQL 文本、参数、错误。
// 当查询/执行返回零行时额外打印 [WARN]，便于发现路径写错或条件有误。
//
// 建议仅在开发和测试环境开启。
//
// 示例：
//
//	dal.NewDal(db, loader, dal.WithDebug(true))
func WithDebug(enable bool) Option {
	return func(o *options) {
		o.debug = enable
	}
}

// WithHook 注册生命周期 Hook，可注册多个，按注册顺序依次调用。
//
// 示例：
//
//	dal.NewDal(db, loader,
//	    dal.WithHook(&MetricsHook{}),
//	    dal.WithHook(&TracingHook{}),
//	)
func WithHook(h Hook) Option {
	return func(o *options) {
		o.hooks = append(o.hooks, h)
	}
}

// WithCacheCleanup 开启定时缓存清理
//
// SQL 文件加载后缓存在内存中，默认永不清理。
// 文件数量较多时建议开启，防止内存持续增长。
// 清理在后台 goroutine 执行，不阻塞正常请求。
//
// 推荐间隔：生产环境 30 分钟 ~ 1 小时。
//
// 示例：
//
//	dal.NewDal(db, loader, dal.WithCacheCleanup(30*time.Minute))
//	defer dal.Close()
func WithCacheCleanup(interval time.Duration) Option {
	return func(o *options) {
		o.cacheCleanup = interval
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// DAL ///////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// DAL 核心实例，持有数据库连接和 SQL 加载器。
//
// 通过 New 创建并作为句柄使用：
//
//	d, err := dal.NewDal(db, loader, opts...)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer d.Close()
type DAL struct {
	provider DBProvider
	loader   SQLLoader
	opts     options
	stopCh   chan struct{}
}

// 默认全局实例，通过 New 初始化后自动设置
var defaultDAL *DAL

// ctxKey 用于在 context 中存取 DAL 实例（多数据源场景）
type ctxKey struct{}

// NewDal 创建并初始化默认全局 DAL 实例，返回句柄和错误。
//
// 应用启动时调用一次，之后直接使用包级函数即可，无需传递任何实例。
// 返回的句柄仅用于生命周期管理（Close），查询时不需要它。
//
// 示例：
//
//	//go:embed rawsql
//	var SQLFS embed.FS
//
//	func main() {
//	    sub, _ := fs.Sub(SQLFS, "rawsql")
//	    d, err := dal.NewDal(
//	        db,
//	        dal.NewEmbedLoader(sub),
//	        dal.WithDebug(true),
//	    )
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    defer d.Close()
//
//	    // 后续直接使用，无需传句柄
//	    rows, err := dal.Query[AccountVO](ctx, "account/list.sql", 1, 10, 0)
//	}
func NewDal(
	db *gorm.DB,
	loader SQLLoader,
	opts ...Option,
) (*DAL, error) {
	if db == nil {
		return nil, fmt.Errorf("dal.NewDal: db 不能为 nil")
	}

	if loader == nil {
		return nil, fmt.Errorf("dal.NewDal: loader 不能为 nil")
	}

	d := newDAL(&singleDBProvider{db: db}, loader, opts...)
	defaultDAL = d

	return d, nil
}

// NewWithProvider 使用自定义 DBProvider 创建独立 DAL 实例
//
// 适用于读写分离、多租户等需要动态切换数据库的场景。
// 创建后通过 WithDB 注入 context，后续调用写法完全不变。
//
// 示例（读写分离）：
//
//	type RWProvider struct {
//	    write *gorm.DB
//	    read  *gorm.DB
//	}
//
//	func (p *RWProvider) Get(ctx context.Context) *gorm.DB {
//	    if isReadOnly(ctx) {
//	        return p.read.WithContext(ctx)
//	    }
//	    return p.write.WithContext(ctx)
//	}
//
//	d, err := dal.NewWithProvider(&RWProvider{write: wDB, read: rDB}, loader)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	ctx = dal.WithDB(ctx, d)
func NewWithProvider(
	provider DBProvider,
	loader SQLLoader,
	opts ...Option,
) (*DAL, error) {
	if provider == nil {
		return nil, fmt.Errorf("dal.NewWithProvider: provider 不能为 nil")
	}

	if loader == nil {
		return nil, fmt.Errorf("dal.NewWithProvider: loader 不能为 nil")
	}

	return newDAL(provider, loader, opts...), nil
}

func newDAL(
	provider DBProvider,
	loader SQLLoader,
	opts ...Option,
) *DAL {
	d := &DAL{
		provider: provider,
		loader:   loader,
		stopCh:   make(chan struct{}),
	}

	for _, opt := range opts {
		opt(&d.opts)
	}

	if d.opts.cacheCleanup > 0 {
		d.startCacheCleanup()
	}

	return d
}

// Close 停止后台定时缓存清理 goroutine
//
// 通过返回的句柄调用，建议配合 defer：
//
//	d, err := dal.NewDal(db, loader)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer d.Close()
func (d *DAL) Close() {
	select {
	case <-d.stopCh:
	default:
		close(d.stopCh)
	}
}

// WithDB 将指定 DAL 实例注入 context，用于多数据源场景。
//
// 注入后，当前 context 链路内所有 dal 调用自动使用该实例，
// 调用方写法完全不变，无需修改任何查询代码。
//
// 示例：
//
//	// 默认使用主库（无需任何操作）
//	rows, err := dal.Query[AccountVO](ctx, "account/list.sql", 1, 10, 0)
//
//	// 切换到报表库（注入一次，后续自动使用）
//	reportDAL, _ := dal.NewDal(reportDB, dal.NewEmbedLoader(reportSub))
//	ctx = dal.WithDB(ctx, reportDAL)
//	rows, err := dal.Query[ReportVO](ctx, "report/monthly.sql", 2024)
func WithDB(ctx context.Context, d *DAL) context.Context {
	return context.WithValue(ctx, ctxKey{}, d)
}

// resolve 从 context 取实例，取不到则使用默认全局实例
func resolve(ctx context.Context) *DAL {
	if d, ok := ctx.Value(ctxKey{}).(*DAL); ok && d != nil {
		return d
	}

	if defaultDAL == nil {
		panic("dal: 未初始化，请先调用 dal.NewDal()")
	}

	return defaultDAL
}

// Preload 预热 SQL 文件缓存（使用默认全局实例）
//
// 用途：
//   - 应用启动时提前加载，避免首次请求延迟
//   - 提前校验 embed 路径是否正确（写错在启动时就报错）
//
// 示例：
//
//	err := dal.Preload(
//	    "account/list.sql",
//	    "account/find_by_id.sql",
//	    "order/page.sql",
//	    "order/count_page.sql",
//	)
//	if err != nil {
//	    log.Fatal("SQL 预热失败:", err)
//	}
func Preload(files ...string) error {
	if defaultDAL == nil {
		panic("dal: 未初始化，请先调用 dal.NewDal()")
	}

	for _, file := range files {
		if _, err := defaultDAL.loader.Load(file); err != nil {
			return err
		}
	}

	return nil
}

func (d *DAL) stop() {
	select {
	case <-d.stopCh:
	default:
		close(d.stopCh)
	}
}

func (d *DAL) startCacheCleanup() {
	go func() {
		ticker := time.NewTicker(d.opts.cacheCleanup)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				d.loader.ClearCache()
				if d.opts.debug {
					log.Printf("[DAL] SQL 缓存已清理，下次请求将重新从 embed.FS 加载\n")
				}
			case <-d.stopCh:
				return
			}
		}
	}()
}

func (d *DAL) db(ctx context.Context) *gorm.DB {
	return d.provider.Get(ctx)
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Debug //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func debugLog(
	d *DAL,
	sqlFile string,
	sqlText string,
	args []any,
	cost time.Duration,
	err error,
) {
	if !d.opts.debug {
		return
	}
	log.Printf(
		"[DAL] file=%s cost=%s err=%v\nsql=\n%s\nargs=%+v\n",
		sqlFile, cost, err, sqlText, args,
	)
}

func debugWarnEmpty(d *DAL, sqlFile string) {
	if !d.opts.debug {
		return
	}
	log.Printf(
		"[DAL][WARN] 返回零行，请确认 SQL 路径和查询条件是否正确 file=%s\n",
		sqlFile,
	)
}

func runBeforeHooks(d *DAL, ctx context.Context, sqlFile string, args []any) {
	for _, h := range d.opts.hooks {
		h.Before(ctx, sqlFile, args)
	}
}

func runAfterHooks(d *DAL, ctx context.Context, sqlFile string, args []any, cost time.Duration, err error) {
	for _, h := range d.opts.hooks {
		h.After(ctx, sqlFile, args, cost, err)
	}
}

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Query //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// Query 查询多条记录（位置参数 ?）
//
// SQL 示例：
//
//	-- rawsql/account/list.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//	ORDER BY created_at DESC
//	LIMIT  ?
//	OFFSET ?
//
// Go 示例：
//
//	rows, err := dal.Query[AccountVO](
//	    ctx,
//	    "account/list.sql",
//	    1,  // status  = ?
//	    10, // LIMIT   = ?
//	    0,  // OFFSET  = ?
//	)
func Query[T any](
	ctx context.Context,
	sqlFile string,
	args ...any,
) ([]T, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result []T

	err = d.db(ctx).
		Raw(sqlText, args...).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.Query [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// QueryOne 查询单条记录（位置参数 ?）
//
// 返回值语义：
//   - (*T, nil)    — 查到记录
//   - (nil, nil)   — 记录不存在（debug 模式打印 WARN）
//   - (nil, error) — 执行出错
//
// SQL 示例：
//
//	-- rawsql/account/find_by_id.sql
//	SELECT id, username, email, status
//	FROM   account
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//	LIMIT 1
//
// Go 示例：
//
//	account, err := dal.QueryOne[AccountVO](ctx, "account/find_by_id.sql", 123)
//	if err != nil {
//	    return err
//	}
//	if account == nil {
//	    return errors.New("账号不存在")
//	}
func QueryOne[T any](
	ctx context.Context,
	sqlFile string,
	args ...any,
) (*T, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result T

	tx := d.db(ctx).
		Raw(sqlText, args...).
		Limit(1).
		Scan(&result)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, tx.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, tx.Error)

	if tx.Error != nil {
		return nil, fmt.Errorf("dal.QueryOne [%s]: %w", sqlFile, tx.Error)
	}

	// RowsAffected == 0 表示没有匹配的行
	if tx.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
		return nil, nil
	}

	return &result, nil
}

// QueryNamed 命名参数查询多条记录（命名参数 @name）
//
// SQL 使用 @name 作为占位符，通过 map 传参，与顺序无关。
// 适合参数较多、顺序容易混淆的场景。
//
// SQL 示例：
//
//	-- rawsql/account/search.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  deleted_at IS NULL
//	  AND  (@username  = ''  OR username   LIKE CONCAT('%', @username, '%'))
//	  AND  (@status   = -1  OR status    = @status)
//	  AND  (@start_at = ''  OR created_at >= @start_at)
//	  AND  (@end_at   = ''  OR created_at <= @end_at)
//	ORDER BY created_at DESC
//	LIMIT  @limit
//	OFFSET @offset
//
// Go 示例：
//
//	rows, err := dal.QueryNamed[AccountVO](
//	    ctx,
//	    "account/search.sql",
//	    map[string]any{
//	        "username": "张",
//	        "status":   1,
//	        "start_at": "2024-01-01",
//	        "end_at":   "2024-12-31",
//	        "limit":    10,
//	        "offset":   0,
//	    },
//	)
func QueryNamed[T any](
	ctx context.Context,
	sqlFile string,
	params map[string]any,
) ([]T, error) {
	d := resolve(ctx)
	start := time.Now()
	args := []any{params}
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result []T

	// 命名参数必须直接传 map，不能通过 args... 展开，否则 @name 不会被替换
	err = d.db(ctx).
		Raw(sqlText, params).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.QueryNamed [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// QueryOneNamed 命名参数查询单条记录（命名参数 @name）
//
// 返回值语义：
//   - (*T, nil)    — 查到记录
//   - (nil, nil)   — 记录不存在（debug 模式打印 WARN）
//   - (nil, error) — 执行出错
//
// SQL 示例：
//
//	-- rawsql/account/find_by_username.sql
//	SELECT id, username, password_hash, status
//	FROM   account
//	WHERE  username   = @username
//	  AND  deleted_at IS NULL
//	LIMIT 1
//
// Go 示例：
//
//	account, err := dal.QueryOneNamed[AccountVO](
//	    ctx,
//	    "account/find_by_username.sql",
//	    map[string]any{"username": "admin"},
//	)
//	if err != nil {
//	    return err
//	}
//	if account == nil {
//	    return errors.New("用户名不存在")
//	}
func QueryOneNamed[T any](
	ctx context.Context,
	sqlFile string,
	params map[string]any,
) (*T, error) {
	d := resolve(ctx)
	start := time.Now()
	args := []any{params}
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result T

	tx := d.db(ctx).
		Raw(sqlText, params).
		Limit(1).
		Scan(&result)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, tx.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, tx.Error)

	if tx.Error != nil {
		return nil, fmt.Errorf("dal.QueryOneNamed [%s]: %w", sqlFile, tx.Error)
	}

	if tx.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
		return nil, nil
	}

	return &result, nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Exec //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// ExecResult 执行结果
type ExecResult struct {
	RowsAffected int64 `json:"rowsAffected"`
}

// Exec 执行 SQL，不关心影响行数（INSERT / UPDATE / DELETE）
//
// SQL 示例：
//
//	-- rawsql/account/disable.sql
//	UPDATE account
//	SET    status = 0, updated_at = NOW()
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	err := dal.Exec(ctx, "account/disable.sql", 123)
func Exec(
	ctx context.Context,
	sqlFile string,
	args ...any,
) error {
	_, err := ExecAffected(ctx, sqlFile, args...)
	return err
}

// ExecAffected 执行 SQL 并返回影响行数
//
// debug 模式下，影响行数为 0 时打印 WARN。
//
// SQL 示例：
//
//	-- rawsql/account/update_status.sql
//	UPDATE account
//	SET    status = ?, updated_at = NOW()
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	result, err := dal.ExecAffected(ctx, "account/update_status.sql", 0, 123)
//	if err != nil {
//	    return err
//	}
//	if result.RowsAffected == 0 {
//	    return errors.New("记录不存在或已被删除")
//	}
func ExecAffected(
	ctx context.Context,
	sqlFile string,
	args ...any,
) (*ExecResult, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	tx := d.db(ctx).Exec(sqlText, args...)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, tx.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, tx.Error)

	if tx.Error != nil {
		return nil, fmt.Errorf("dal.ExecAffected [%s]: %w", sqlFile, tx.Error)
	}

	if tx.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return &ExecResult{RowsAffected: tx.RowsAffected}, nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Count /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// Count 查询数量（支持位置参数和命名参数）
//
// SQL 必须返回单个数值列（通常是 COUNT(*)）。
//
// 位置参数示例：
//
//	-- rawsql/account/count_page.sql
//	SELECT COUNT(*)
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//
//	total, err := dal.Count(ctx, "account/count_page.sql", 1)
//
// 命名参数示例：
//
//	-- rawsql/order/count_page.sql
//	SELECT COUNT(*)
//	FROM   `order`
//	WHERE  deleted_at  IS NULL
//	  AND  (@account_id = 0  OR account_id = @account_id)
//	  AND  (@status    = -1 OR status     = @status)
//
//	total, err := dal.Count(
//	    ctx, "order/count_page.sql",
//	    map[string]any{"account_id": 123, "status": 1},
//	)
func Count(
	ctx context.Context,
	sqlFile string,
	args ...any,
) (int64, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return 0, err
	}

	var total int64

	err = d.db(ctx).
		Raw(sqlText, args...).
		Scan(&total).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return 0, fmt.Errorf("dal.Count [%s]: %w", sqlFile, err)
	}

	return total, nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Page //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// PageResult 分页结果
type PageResult[T any] struct {
	List  []T   `json:"list"`
	Total int64 `json:"total"`
}

// QueryPage 位置参数分页查询
//
// count SQL 由数据 SQL 文件名自动推导，规则：文件名前加 "count_" 前缀。
//
//	"account/page.sql"  →  "account/count_page.sql"
//
// 数据 SQL 示例：
//
//	-- rawsql/account/page.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//	ORDER BY created_at DESC
//	LIMIT  ?
//	OFFSET ?
//
// Count SQL 示例（与数据 SQL 过滤条件完全一致，去掉 LIMIT/OFFSET）：
//
//	-- rawsql/account/count_page.sql
//	SELECT COUNT(*)
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	result, err := dal.QueryPage[AccountVO](
//	    ctx,
//	    "account/page.sql",
//	    []any{1},      // 业务过滤参数，同时传给 count SQL: status = ?
//	    []any{10, 0},  // 分页参数，仅传给数据 SQL: LIMIT=10, OFFSET=0
//	)
//	// result.List  — 当页数据
//	// result.Total — 总条数
func QueryPage[T any](
	ctx context.Context,
	dataSqlFile string,
	filterArgs []any,
	pageArgs []any,
) (PageResult[T], error) {
	var result PageResult[T]

	countSqlFile := buildCountSQLPath(dataSqlFile)

	// 安全合并，避免修改调用方传入的原始 slice
	dataArgs := make([]any, 0, len(filterArgs)+len(pageArgs))
	dataArgs = append(dataArgs, filterArgs...)
	dataArgs = append(dataArgs, pageArgs...)

	list, err := Query[T](ctx, dataSqlFile, dataArgs...)
	if err != nil {
		return result, err
	}

	total, err := Count(ctx, countSqlFile, filterArgs...)
	if err != nil {
		return result, err
	}

	result.List = list
	result.Total = total

	return result, nil
}

// QueryPageNamed 命名参数分页查询
//
// count SQL 文件命名规则同 QueryPage，文件名前加 "count_" 前缀。
// limit 和 offset 放在 params 中，count SQL 不引用它们即可。
//
// 数据 SQL 示例：
//
//	-- rawsql/order/page.sql
//	SELECT id, order_no, amount, status, created_at
//	FROM   `order`
//	WHERE  deleted_at IS NULL
//	  AND  (@account_id = 0  OR account_id = @account_id)
//	  AND  (@status    = -1 OR status     = @status)
//	ORDER BY created_at DESC
//	LIMIT  @limit
//	OFFSET @offset
//
// Count SQL 示例（不引用 @limit/@offset 即可）：
//
//	-- rawsql/order/count_page.sql
//	SELECT COUNT(*)
//	FROM   `order`
//	WHERE  deleted_at IS NULL
//	  AND  (@account_id = 0  OR account_id = @account_id)
//	  AND  (@status    = -1 OR status     = @status)
//
// Go 示例：
//
//	result, err := dal.QueryPageNamed[OrderVO](
//	    ctx,
//	    "order/page.sql",
//	    map[string]any{
//	        "account_id": 123,
//	        "status":     1,
//	        "limit":      10,
//	        "offset":     0,
//	    },
//	)
func QueryPageNamed[T any](
	ctx context.Context,
	dataSqlFile string,
	params map[string]any,
) (PageResult[T], error) {
	var result PageResult[T]

	countSqlFile := buildCountSQLPath(dataSqlFile)

	list, err := QueryNamed[T](ctx, dataSqlFile, params)
	if err != nil {
		return result, err
	}

	// count SQL 传相同 map，SQL 中不引用 @limit/@offset 即可自动忽略
	total, err := Count(ctx, countSqlFile, params)
	if err != nil {
		return result, err
	}

	result.List = list
	result.Total = total

	return result, nil
}

func buildCountSQLPath(dataSqlFile string) string {
	dir := filepath.Dir(dataSqlFile)
	base := filepath.Base(dataSqlFile)
	return filepath.ToSlash(filepath.Join(dir, "count_"+base))
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// Transaction ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// WithTx 开启事务，fn 返回 nil 时提交，返回 error 时自动回滚。
//
// 示例（下单扣库存）：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    stock, err := dal.TxQueryOne[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
//	    if err != nil {
//	        return err
//	    }
//	    if stock == nil || stock.Quantity < qty {
//	        return errors.New("库存不足")
//	    }
//	    if err := dal.TxExec(ctx, tx, "stock/deduct.sql", qty, productID, qty); err != nil {
//	        return err
//	    }
//	    return dal.TxExec(ctx, tx, "order/insert.sql", accountID, productID, qty, amount, orderNo)
//	})
func WithTx(
	ctx context.Context,
	fn func(tx *gorm.DB) error,
) error {
	return resolve(ctx).db(ctx).Transaction(fn)
}

// TxQuery 在事务中查询多条记录（位置参数 ?）
//
// SQL 示例：
//
//	-- rawsql/stock/find_for_update.sql
//	SELECT id, product_id, quantity
//	FROM   stock
//	WHERE  product_id = ?
//	  AND  deleted_at IS NULL
//	FOR UPDATE
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    stocks, err := dal.TxQuery[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
//	    if err != nil {
//	        return err
//	    }
//	    // ... 处理库存逻辑
//	    return nil
//	})
func TxQuery[T any](
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) ([]T, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result []T

	err = tx.WithContext(ctx).
		Raw(sqlText, args...).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.TxQuery [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// TxQueryOne 在事务中查询单条记录（位置参数 ?）
//
// 返回值语义：
//   - (*T, nil)    — 查到记录
//   - (nil, nil)   — 记录不存在（debug 模式打印 WARN）
//   - (nil, error) — 执行出错
//
// SQL 示例：
//
//	-- rawsql/account/find_for_update.sql
//	SELECT id, username, balance
//	FROM   account
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//	FOR UPDATE
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    account, err := dal.TxQueryOne[AccountVO](ctx, tx, "account/find_for_update.sql", accountID)
//	    if err != nil {
//	        return err
//	    }
//	    if account == nil {
//	        return errors.New("账号不存在")
//	    }
//	    // ... 处理余额逻辑
//	    return nil
//	})
func TxQueryOne[T any](
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) (*T, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result T

	txResult := tx.WithContext(ctx).
		Raw(sqlText, args...).
		Limit(1).
		Scan(&result)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, txResult.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, txResult.Error)

	if txResult.Error != nil {
		return nil, fmt.Errorf("dal.TxQueryOne [%s]: %w", sqlFile, txResult.Error)
	}

	if txResult.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
		return nil, nil
	}

	return &result, nil
}

// TxQueryNamed 在事务中命名参数查询多条记录（命名参数 @name）
//
// SQL 示例：
//
//	-- rawsql/order/list_by_account.sql
//	SELECT id, order_no, amount, status
//	FROM   `order`
//	WHERE  account_id = @account_id
//	  AND  status     = @status
//	  AND  deleted_at IS NULL
//	FOR UPDATE
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    orders, err := dal.TxQueryNamed[OrderVO](
//	        ctx, tx,
//	        "order/list_by_account.sql",
//	        map[string]any{"account_id": 123, "status": 1},
//	    )
//	    if err != nil {
//	        return err
//	    }
//	    // ... 处理订单逻辑
//	    return nil
//	})
func TxQueryNamed[T any](
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	params map[string]any,
) ([]T, error) {
	d := resolve(ctx)
	start := time.Now()
	args := []any{params}
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result []T

	err = tx.WithContext(ctx).
		Raw(sqlText, params).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.TxQueryNamed [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// TxCount 在事务中查询数量
//
// SQL 示例：
//
//	-- rawsql/order/count_by_account.sql
//	SELECT COUNT(*)
//	FROM   `order`
//	WHERE  account_id = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    total, err := dal.TxCount(ctx, tx, "order/count_by_account.sql", accountID)
//	    if err != nil {
//	        return err
//	    }
//	    log.Println("订单总数:", total)
//	    return nil
//	})
func TxCount(
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) (int64, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return 0, err
	}

	var total int64

	err = tx.WithContext(ctx).
		Raw(sqlText, args...).
		Scan(&total).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return 0, fmt.Errorf("dal.TxCount [%s]: %w", sqlFile, err)
	}

	return total, nil
}

// TxExec 在事务中执行 SQL（INSERT / UPDATE / DELETE）
//
// SQL 示例：
//
//	-- rawsql/stock/deduct.sql
//	UPDATE stock
//	SET    quantity   = quantity - ?,
//	       updated_at = NOW()
//	WHERE  product_id = ?
//	  AND  quantity   >= ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    return dal.TxExec(
//	        ctx, tx,
//	        "stock/deduct.sql",
//	        qty,       // quantity - ?
//	        productID, // product_id = ?
//	        qty,       // quantity >= ?（防超卖）
//	    )
//	})
func TxExec(
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) error {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return err
	}

	err = tx.WithContext(ctx).
		Exec(sqlText, args...).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return fmt.Errorf("dal.TxExec [%s]: %w", sqlFile, err)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Must //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// MustExec 执行失败直接 panic（慎用，仅适合初始化/启动阶段）
//
// SQL 示例：
//
//	-- rawsql/schema/create_account.sql
//	CREATE TABLE IF NOT EXISTS account (
//	    id            BIGINT        NOT NULL AUTO_INCREMENT PRIMARY KEY,
//	    username      VARCHAR(64)   NOT NULL UNIQUE,
//	    password_hash VARCHAR(128)  NOT NULL,
//	    status        TINYINT       NOT NULL DEFAULT 1,
//	    created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
//	    updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
//	    deleted_at    DATETIME      NULL
//	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
//
// Go 示例：
//
//	dal.MustExec(ctx, "schema/create_account.sql")
func MustExec(
	ctx context.Context,
	sqlFile string,
	args ...any,
) {
	if err := Exec(ctx, sqlFile, args...); err != nil {
		panic(err)
	}
}

// MustQueryOne 查询失败或记录不存在时直接 panic（慎用，仅适合确定数据存在的场景）
//
// SQL 示例：
//
//	-- rawsql/config/find_by_key.sql
//	SELECT `key`, `value`, remark
//	FROM   sys_config
//	WHERE  `key`      = ?
//	  AND  deleted_at IS NULL
//	LIMIT 1
//
// Go 示例：
//
//	cfg := dal.MustQueryOne[ConfigVO](ctx, "config/find_by_key.sql", "site_name")
func MustQueryOne[T any](
	ctx context.Context,
	sqlFile string,
	args ...any,
) *T {
	v, err := QueryOne[T](ctx, sqlFile, args...)
	if err != nil {
		panic(err)
	}
	if v == nil {
		panic(fmt.Errorf("dal.MustQueryOne [%s]: record not found", sqlFile))
	}
	return v
}
