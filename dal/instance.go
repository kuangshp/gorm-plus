package dal

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

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
