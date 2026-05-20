package gormplus

import (
	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/gorm"
)

// ================== 慢查询监控 ==================

// SlowQueryConfig 慢查询监控插件配置。Threshold 为 0 时自动设为 200ms。
type SlowQueryConfig = query.SlowQueryConfig

// SlowQueryInfo 慢查询详情，传递给自定义 Logger。
// SQL 字段已将 ? 替换为实际参数值，可直接复制到客户端执行 EXPLAIN 分析。
type SlowQueryInfo = query.SlowQueryInfo

// RegisterSlowQuery 向指定 DB 注册慢查询监控插件。
// 覆盖 Query / Create / Update / Delete / Row / Raw 全部操作类型。
// Threshold 为 0 时自动设为默认值 200ms。Logger 为 nil 时使用标准库 log 输出到 stderr。
//
// 使用示例：
//
//	// 对接 zap（推荐）
//	gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
//	        zap.L().Warn("慢查询",
//	            zap.Duration("cost",  info.Duration),
//	            zap.String("table",   info.Table),
//	            zap.String("sql",     info.SQL),
//	            zap.Int64("rows",     info.RowsAffected),
//	            zap.Error(info.Error),
//	        )
//	    },
//	})
//
//	// 配合 traceID 透传
//	gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
//	        traceID, _ := ctx.Value("traceID").(string)
//	        log.Printf("[慢查询] trace=%s cost=%v sql=%s", traceID, info.Duration, info.SQL)
//	    },
//	})
func RegisterSlowQuery(db *gorm.DB, cfg query.SlowQueryConfig) error {
	return query.RegisterSlowQuery(db, cfg)
}
