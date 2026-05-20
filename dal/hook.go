package dal

import (
	"context"
	"time"
)

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
