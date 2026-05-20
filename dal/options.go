package dal

import (
	"time"
)

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
