package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/dal"
	"gorm.io/gorm"
)

// ================== DAL SQL 文件化查询 ==================

// DALInstance DAL 执行器类型，持有数据库连接和 SQL 加载器。
// 通过 NewDal 初始化全局默认实例后，直接使用 DALQuery 等包级泛型函数，无需传递实例。
// 多数据源时通过 WithDALDB(ctx, d) 注入 context，调用方写法完全不变。
type DALInstance = dal.DAL

// DALHook DAL 生命周期钩子接口，可用于慢 SQL 监控、链路追踪、指标采集等。
//
// 示例（慢 SQL 告警）：
//
//	type SlowDALHook struct{ Threshold time.Duration }
//
//	func (h *SlowDALHook) Before(ctx context.Context, sqlFile string, args []any) {}
//	func (h *SlowDALHook) After(ctx context.Context, sqlFile string, args []any, cost time.Duration, err error) {
//	    if cost > h.Threshold {
//	        log.Printf("[SLOW SQL] file=%s cost=%s", sqlFile, cost)
//	    }
//	}
type DALHook = dal.Hook

// DALOption DAL 配置项
type DALOption = dal.Option

// DALPageResult 分页结果
type DALPageResult[T any] = dal.PageResult[T]

// DALExecResult 执行结果（含影响行数）
type DALExecResult = dal.ExecResult

// DALSQLLoader SQL 文件加载器接口
type DALSQLLoader = dal.SQLLoader

// DALDBProvider 数据库提供器接口，可自行实现读写分离、多租户等动态切换场景
type DALDBProvider = dal.DBProvider

// NewEmbedLoader 创建基于 embed.FS 的 SQL Loader（生产推荐）。
//
// SQL 文件通过 //go:embed 在编译期打包进二进制，生产部署只需一个可执行文件。
// 推荐配合 fs.Sub 去掉顶层目录前缀，调用时路径更简洁：
//
//	//go:embed rawsql
//	var SQLFS embed.FS
//
//	sub, _ := fs.Sub(SQLFS, "rawsql")
//	d, err := gormplus.NewDal(db, gormplus.NewEmbedLoader(sub))
//
//	// 调用时只需相对路径
//	gormplus.DALQuery[UserVO](ctx, "account/list.sql", 1, 10, 0)
var NewEmbedLoader = dal.NewEmbedLoader

// WithDALDebug 开启 DAL Debug 日志。
// 开启后每条 SQL 执行都会打印：文件路径、耗时、SQL 文本、参数、错误。
// 查询返回零行时额外打印 [WARN]，便于发现路径写错或条件有误。
// 建议仅在开发和测试环境开启。
var WithDALDebug = dal.WithDebug

// WithDALHook 注册 DAL 生命周期 Hook，可注册多个，按注册顺序依次调用。
var WithDALHook = dal.WithHook

// WithDALCacheCleanup 开启定时缓存清理（默认永不清理）。
// SQL 文件数量较多时建议开启，防止内存持续增长。
// 推荐间隔：生产环境 30 分钟 ~ 1 小时。
// 需配合 d.Close() 在程序退出时停止后台 goroutine。
var WithDALCacheCleanup = dal.WithCacheCleanup

// NewDal 创建并初始化默认全局 DAL 实例，返回句柄和错误。
//
// 应用启动时调用一次，之后直接使用 DALQuery 等包级函数，无需传递实例。
// 返回的句柄仅用于生命周期管理（d.Close()），查询时不需要它。
//
// 示例：
//
//	//go:embed rawsql
//	var SQLFS embed.FS
//
//	func main() {
//	    sub, _ := fs.Sub(SQLFS, "rawsql")
//	    d, err := gormplus.NewDal(
//	        db,
//	        gormplus.NewEmbedLoader(sub),
//	        gormplus.WithDALDebug(true),
//	        gormplus.WithDALCacheCleanup(30*time.Minute),
//	    )
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    defer d.Close()
//
//	    // 后续直接使用，无需传句柄
//	    rows, err := gormplus.DALQuery[AccountVO](ctx, "account/list.sql", 1, 10, 0)
//	}
func NewDal(db *gorm.DB, loader dal.SQLLoader, opts ...dal.Option) (*dal.DAL, error) {
	return dal.NewDal(db, loader, opts...)
}

// NewDalWithProvider 使用自定义 DBProvider 创建 DAL 实例（读写分离、多租户等场景）。
//
// 示例（读写分离）：
//
//	type RWProvider struct{ write, read *gorm.DB }
//	func (p *RWProvider) Get(ctx context.Context) *gorm.DB {
//	    if isReadOnly(ctx) { return p.read.WithContext(ctx) }
//	    return p.write.WithContext(ctx)
//	}
//	d, err := gormplus.NewDalWithProvider(&RWProvider{write: wDB, read: rDB}, loader)
//	ctx = gormplus.WithDALDB(ctx, d)
func NewDalWithProvider(provider dal.DBProvider, loader dal.SQLLoader, opts ...dal.Option) (*dal.DAL, error) {
	return dal.NewWithProvider(provider, loader, opts...)
}

// WithDALDB 将指定 DAL 实例注入 context，用于多数据源场景。
// 注入后，当前 context 链路内所有 DALQuery 等调用自动使用该实例，写法完全不变。
//
// 示例：
//
//	// 切换到报表库
//	reportDAL, _ := gormplus.NewDal(reportDB, gormplus.NewEmbedLoader(sub))
//	ctx = gormplus.WithDALDB(ctx, reportDAL)
//	rows, err := gormplus.DALQuery[ReportVO](ctx, "report/monthly.sql", 2024)
func WithDALDB(ctx context.Context, d *dal.DAL) context.Context {
	return dal.WithDB(ctx, d)
}

// DALPreload 预热 SQL 文件缓存（使用默认全局实例）。
// 应用启动时提前加载，避免首次请求延迟；路径写错在启动时就报错。
//
// 示例：
//
//	err := gormplus.DALPreload(
//	    "account/list.sql",
//	    "account/find_by_id.sql",
//	    "order/page.sql",
//	)
//	if err != nil { log.Fatal("SQL 预热失败:", err) }
func DALPreload(files ...string) error {
	return dal.Preload(files...)
}

// DALQuery 查询多条记录（位置参数 ?）。
//
// SQL 示例：
//
//	-- rawsql/account/list.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  status = ? AND deleted_at IS NULL
//	ORDER BY created_at DESC LIMIT ? OFFSET ?
//
// Go 示例：
//
//	rows, err := gormplus.DALQuery[AccountVO](ctx, "account/list.sql", 1, 10, 0)
func DALQuery[T any](ctx context.Context, sqlFile string, args ...any) ([]T, error) {
	return dal.Query[T](ctx, sqlFile, args...)
}

// DALQueryOne 查询单条记录（位置参数 ?）。
//
// 返回值语义：(*T,nil)=找到  (nil,nil)=不存在  (nil,error)=出错
//
// SQL 示例：
//
//	-- rawsql/account/find_by_id.sql
//	SELECT id, username, email, status FROM account
//	WHERE id = ? AND deleted_at IS NULL LIMIT 1
//
// Go 示例：
//
//	account, err := gormplus.DALQueryOne[AccountVO](ctx, "account/find_by_id.sql", 123)
//	if err != nil { return err }
//	if account == nil { return errors.New("账号不存在") }
func DALQueryOne[T any](ctx context.Context, sqlFile string, args ...any) (*T, error) {
	return dal.QueryOne[T](ctx, sqlFile, args...)
}

// DALQueryNamed 命名参数查询多条记录（命名参数 @name）。
//
// SQL 示例：
//
//	-- rawsql/account/search.sql
//	SELECT id, username, status FROM account
//	WHERE deleted_at IS NULL
//	  AND (@username = '' OR username LIKE CONCAT('%', @username, '%'))
//	  AND (@status  = -1 OR status   = @status)
//	ORDER BY created_at DESC LIMIT @limit OFFSET @offset
//
// Go 示例：
//
//	rows, err := gormplus.DALQueryNamed[AccountVO](ctx, "account/search.sql", map[string]any{
//	    "username": "张", "status": 1, "limit": 10, "offset": 0,
//	})
func DALQueryNamed[T any](ctx context.Context, sqlFile string, params map[string]any) ([]T, error) {
	return dal.QueryNamed[T](ctx, sqlFile, params)
}

// DALQueryOneNamed 命名参数查询单条记录（命名参数 @name）。
//
// 返回值语义：(*T,nil)=找到  (nil,nil)=不存在  (nil,error)=出错
//
// Go 示例：
//
//	account, err := gormplus.DALQueryOneNamed[AccountVO](
//	    ctx, "account/find_by_username.sql",
//	    map[string]any{"username": "admin"},
//	)
func DALQueryOneNamed[T any](ctx context.Context, sqlFile string, params map[string]any) (*T, error) {
	return dal.QueryOneNamed[T](ctx, sqlFile, params)
}

// DALExec 执行 SQL，不关心影响行数（INSERT / UPDATE / DELETE）。
//
// Go 示例：
//
//	err := gormplus.DALExec(ctx, "account/disable.sql", 123)
func DALExec(ctx context.Context, sqlFile string, args ...any) error {
	return dal.Exec(ctx, sqlFile, args...)
}

// DALExecAffected 执行 SQL 并返回影响行数。
//
// Go 示例：
//
//	result, err := gormplus.DALExecAffected(ctx, "account/update_status.sql", 0, 123)
//	if err != nil { return err }
//	if result.RowsAffected == 0 { return errors.New("记录不存在") }
func DALExecAffected(ctx context.Context, sqlFile string, args ...any) (*dal.ExecResult, error) {
	return dal.ExecAffected(ctx, sqlFile, args...)
}

// DALCount 查询数量，支持位置参数和命名参数。SQL 必须返回单个数值列（COUNT(*)）。
//
// 位置参数示例：
//
//	total, err := gormplus.DALCount(ctx, "account/count_page.sql", 1)
//
// 命名参数示例：
//
//	total, err := gormplus.DALCount(ctx, "order/count_page.sql",
//	    map[string]any{"account_id": 123, "status": 1},
//	)
func DALCount(ctx context.Context, sqlFile string, args ...any) (int64, error) {
	return dal.Count(ctx, sqlFile, args...)
}

// DALQueryPage 位置参数分页查询。
//
// count SQL 由数据 SQL 文件名自动推导：文件名前加 "count_" 前缀。
//
//	"account/page.sql"  →  "account/count_page.sql"
//
// 数据 SQL 示例：
//
//	-- rawsql/account/page.sql
//	SELECT id, username, status FROM account
//	WHERE status = ? AND deleted_at IS NULL
//	ORDER BY created_at DESC LIMIT ? OFFSET ?
//
// Count SQL 示例（过滤条件一致，去掉 LIMIT/OFFSET）：
//
//	-- rawsql/account/count_page.sql
//	SELECT COUNT(*) FROM account WHERE status = ? AND deleted_at IS NULL
//
// Go 示例：
//
//	result, err := gormplus.DALQueryPage[AccountVO](
//	    ctx, "account/page.sql",
//	    []any{1},      // 业务过滤参数，同时传给 count SQL
//	    []any{10, 0},  // 分页参数（LIMIT, OFFSET），仅传给数据 SQL
//	)
//	// result.List — 当页数据  result.Total — 总条数
func DALQueryPage[T any](ctx context.Context, dataSqlFile string, filterArgs []any, pageArgs []any) (dal.PageResult[T], error) {
	return dal.QueryPage[T](ctx, dataSqlFile, filterArgs, pageArgs)
}

// DALQueryPageNamed 命名参数分页查询。
//
// count SQL 命名规则同 DALQueryPage（文件名前加 "count_" 前缀）。
// limit 和 offset 放在 params 中，count SQL 不引用它们即可。
//
// 数据 SQL 示例：
//
//	-- rawsql/order/page.sql
//	SELECT id, order_no, amount, status FROM `order`
//	WHERE deleted_at IS NULL
//	  AND (@account_id = 0 OR account_id = @account_id)
//	  AND (@status = -1 OR status = @status)
//	ORDER BY created_at DESC LIMIT @limit OFFSET @offset
//
// Go 示例：
//
//	result, err := gormplus.DALQueryPageNamed[OrderVO](ctx, "order/page.sql", map[string]any{
//	    "account_id": 123, "status": 1, "limit": 10, "offset": 0,
//	})
func DALQueryPageNamed[T any](ctx context.Context, dataSqlFile string, params map[string]any) (dal.PageResult[T], error) {
	return dal.QueryPageNamed[T](ctx, dataSqlFile, params)
}

// DALWithTx 开启事务，fn 返回 nil 时提交，返回 error 时自动回滚。
//
// 示例（下单扣库存）：
//
//	err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
//	    stock, err := gormplus.DALTxQueryOne[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
//	    if err != nil { return err }
//	    if stock == nil || stock.Quantity < qty { return errors.New("库存不足") }
//	    if err := gormplus.DALTxExec(ctx, tx, "stock/deduct.sql", qty, productID, qty); err != nil {
//	        return err
//	    }
//	    return gormplus.DALTxExec(ctx, tx, "order/insert.sql", accountID, productID, qty, amount, orderNo)
//	})
func DALWithTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return dal.WithTx(ctx, fn)
}

// DALTxQuery 在事务中查询多条记录（位置参数 ?）。
//
// Go 示例：
//
//	err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
//	    stocks, err := gormplus.DALTxQuery[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
//	    if err != nil { return err }
//	    return nil
//	})
func DALTxQuery[T any](ctx context.Context, tx *gorm.DB, sqlFile string, args ...any) ([]T, error) {
	return dal.TxQuery[T](ctx, tx, sqlFile, args...)
}

// DALTxQueryOne 在事务中查询单条记录（位置参数 ?）。
//
// 返回值语义：(*T,nil)=找到  (nil,nil)=不存在  (nil,error)=出错
//
// Go 示例：
//
//	err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
//	    account, err := gormplus.DALTxQueryOne[AccountVO](ctx, tx, "account/find_for_update.sql", accountID)
//	    if err != nil { return err }
//	    if account == nil { return errors.New("账号不存在") }
//	    return nil
//	})
func DALTxQueryOne[T any](ctx context.Context, tx *gorm.DB, sqlFile string, args ...any) (*T, error) {
	return dal.TxQueryOne[T](ctx, tx, sqlFile, args...)
}

// DALTxQueryNamed 在事务中命名参数查询多条记录（命名参数 @name）。
//
// Go 示例：
//
//	err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
//	    orders, err := gormplus.DALTxQueryNamed[OrderVO](ctx, tx,
//	        "order/list_by_account.sql",
//	        map[string]any{"account_id": 123, "status": 1},
//	    )
//	    if err != nil { return err }
//	    return nil
//	})
func DALTxQueryNamed[T any](ctx context.Context, tx *gorm.DB, sqlFile string, params map[string]any) ([]T, error) {
	return dal.TxQueryNamed[T](ctx, tx, sqlFile, params)
}

// DALTxCount 在事务中查询数量。
//
// Go 示例：
//
//	err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
//	    total, err := gormplus.DALTxCount(ctx, tx, "order/count_by_account.sql", accountID)
//	    if err != nil { return err }
//	    return nil
//	})
func DALTxCount(ctx context.Context, tx *gorm.DB, sqlFile string, args ...any) (int64, error) {
	return dal.TxCount(ctx, tx, sqlFile, args...)
}

// DALTxExec 在事务中执行 SQL（INSERT / UPDATE / DELETE）。
//
// SQL 示例：
//
//	-- rawsql/stock/deduct.sql
//	UPDATE stock SET quantity = quantity - ?, updated_at = NOW()
//	WHERE product_id = ? AND quantity >= ? AND deleted_at IS NULL
//
// Go 示例：
//
//	err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
//	    return gormplus.DALTxExec(ctx, tx, "stock/deduct.sql", qty, productID, qty)
//	})
func DALTxExec(ctx context.Context, tx *gorm.DB, sqlFile string, args ...any) error {
	return dal.TxExec(ctx, tx, sqlFile, args...)
}

// DALMustExec 执行失败直接 panic（慎用，仅适合初始化/启动阶段）。
//
// Go 示例：
//
//	gormplus.DALMustExec(ctx, "schema/create_account.sql")
func DALMustExec(ctx context.Context, sqlFile string, args ...any) {
	dal.MustExec(ctx, sqlFile, args...)
}

// DALMustQueryOne 查询失败或记录不存在时直接 panic（慎用，仅适合确定数据存在的场景）。
//
// Go 示例：
//
//	cfg := gormplus.DALMustQueryOne[ConfigVO](ctx, "config/find_by_key.sql", "site_name")
func DALMustQueryOne[T any](ctx context.Context, sqlFile string, args ...any) *T {
	return dal.MustQueryOne[T](ctx, sqlFile, args...)
}
