// Package query 慢查询
package query

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

// ================== 慢查询监控插件 ==================
//
// 通过 gorm Callback 钩子在每次 SQL 执行后检测耗时。
// 超过阈值的 SQL 会触发自定义 Logger，方便对接 zap / logrus / 告警系统。
// 覆盖 Query / Create / Update / Delete / Row / Raw 全部操作类型。
//
// ── 注册示例 ────────────────────────────────────────────────
//
//	// 使用标准库 log 输出（默认，无需配置 Logger）
//	err := query.RegisterSlowQuery(db, query.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	})
//
//	// 对接 zap（推荐生产环境）
//	err := query.RegisterSlowQuery(db, query.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	    Logger: func(ctx context.Context, info query.SlowQueryInfo) {
//	        zap.L().Warn("慢查询",
//	            zap.Duration("cost",  info.Duration),
//	            zap.String("table",  info.Table),
//	            zap.String("sql",    info.SQL),      // 已替换 ? 为实际参数，可直接复制执行
//	            zap.Int64("rows",    info.RowsAffected),
//	            zap.Error(info.Error),               // SQL 出错时非 nil
//	        )
//	    },
//	})
//
// ── 配合 traceID 透传 ────────────────────────────────────────
//
//	Logger: func(ctx context.Context, info query.SlowQueryInfo) {
//	    traceID, _ := ctx.Value("traceID").(string)
//	    zap.L().Warn("慢查询",
//	        zap.String("trace_id", traceID),
//	        zap.Duration("cost", info.Duration),
//	        zap.String("sql", info.SQL),
//	    )
//	},
//
// ── 注意事项 ─────────────────────────────────────────────────
//
//   - 与 RegisterTenant 同时使用，互不干扰，注册顺序无要求
//   - info.SQL 是通过 Dialector.Explain 生成的完整 SQL（已替换 ? 占位符）
//     可直接复制到数据库客户端执行，方便 EXPLAIN 分析
//   - Threshold 为 0 时自动设为默认值 200ms
//   - Logger 为 nil 时使用标准库 log.Printf 输出到 stderr

// SlowQueryInfo 慢查询详情，传递给自定义 Logger 函数
type SlowQueryInfo struct {
	// SQL 完整 SQL 语句，已将 ? 替换为实际参数值，可直接复制执行
	SQL string
	// Duration 实际执行耗时
	Duration time.Duration
	// RowsAffected 影响/返回的行数
	RowsAffected int64
	// Table 操作的主表名（来自 gorm Statement.Table）
	Table string
	// Error SQL 执行出错时非 nil；慢查询与执行错误可同时发生
	Error error
}

// SlowQueryConfig 慢查询插件配置
type SlowQueryConfig struct {
	// Threshold 慢查询阈值，超过此时间才触发 Logger。
	// 零值自动设为 200ms。建议生产环境设为 200ms~500ms。
	Threshold time.Duration

	// Logger 自定义日志函数。
	// 参数 ctx 来自 db.WithContext(ctx)，可用于透传 traceID 等链路信息。
	// 为 nil 时使用 log.Printf 输出到 stderr。
	Logger func(ctx context.Context, info SlowQueryInfo)
}

// slowQueryPlugin gorm.Plugin 实现
type slowQueryPlugin struct{ cfg SlowQueryConfig }

// sqStartKey 用于在 gorm Statement 中存储 SQL 开始时间的 key
const sqStartKey = "gorm-plus:sq:start"

// RegisterSlowQuery 向指定 DB 注册慢查询监控插件。
//
// 可与 RegisterTenant、其他 gorm 插件同时使用，互不干扰。
// 整个应用只需注册一次（通常在 main.go 或 wire.go 的初始化阶段）。
//
// 示例：
//
//	if err := query.RegisterSlowQuery(db, query.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	    Logger:    myZapSlowQueryLogger,
//	}); err != nil {
//	    log.Fatalf("注册慢查询插件失败: %v", err)
//	}
func RegisterSlowQuery(db *gorm.DB, cfg SlowQueryConfig) error {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 200 * time.Millisecond
	}
	return db.Use(&slowQueryPlugin{cfg: cfg})
}

func (p *slowQueryPlugin) Name() string { return "gorm-plus:slow_query" }

func (p *slowQueryPlugin) Initialize(db *gorm.DB) error {
	// before：在 SQL 执行前记录开始时间
	before := func(db *gorm.DB) {
		db.Set(sqStartKey, time.Now())
	}

	// after：在 SQL 执行后计算耗时，超阈值则触发 Logger
	after := func(db *gorm.DB) {
		v, ok := db.Get(sqStartKey)
		if !ok {
			return
		}
		start, ok := v.(time.Time)
		if !ok {
			return
		}
		cost := time.Since(start)
		if cost < p.cfg.Threshold {
			return // 未超阈值，忽略
		}

		info := SlowQueryInfo{
			Duration:     cost,
			RowsAffected: db.Statement.RowsAffected,
			Error:        db.Error,
		}
		if db.Statement != nil {
			info.Table = db.Statement.Table
			// Explain 将 ? 替换为实际参数值，生成可直接执行的完整 SQL
			info.SQL = db.Dialector.Explain(
				db.Statement.SQL.String(),
				db.Statement.Vars...,
			)
		}

		// 从 Statement.Context 获取 ctx，方便 Logger 透传 traceID 等
		ctx := context.Background()
		if db.Statement != nil && db.Statement.Context != nil {
			ctx = db.Statement.Context
		}

		if p.cfg.Logger != nil {
			p.cfg.Logger(ctx, info)
		} else {
			// 默认输出到 stderr（无依赖，开箱即用）
			log.Printf("[SLOW SQL] cost=%v table=%s rows=%d sql=%s\n",
				info.Duration, info.Table, info.RowsAffected, info.SQL)
		}
	}

	// 为所有操作类型注册 before/after 钩子
	// 使用结构体数组统一注册，避免重复代码
	type opDef struct {
		name   string
		before func(string, func(*gorm.DB)) error
		after  func(string, func(*gorm.DB)) error
	}
	ops := []opDef{
		{
			name: "query",
			before: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Query().Before("gorm:query").Register(n, f)
			},
			after: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Query().After("gorm:after_query").Register(n, f)
			},
		},
		{
			name: "create",
			before: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Create().Before("gorm:create").Register(n, f)
			},
			after: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Create().After("gorm:after_create").Register(n, f)
			},
		},
		{
			name: "update",
			before: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Update().Before("gorm:update").Register(n, f)
			},
			after: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Update().After("gorm:after_update").Register(n, f)
			},
		},
		{
			name: "delete",
			before: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Delete().Before("gorm:delete").Register(n, f)
			},
			after: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Delete().After("gorm:after_delete").Register(n, f)
			},
		},
		{
			name:   "row",
			before: func(n string, f func(*gorm.DB)) error { return db.Callback().Row().Before("gorm:row").Register(n, f) },
			after: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Row().After("gorm:after_row").Register(n, f)
			},
		},
		{
			name:   "raw",
			before: func(n string, f func(*gorm.DB)) error { return db.Callback().Raw().Before("gorm:raw").Register(n, f) },
			after: func(n string, f func(*gorm.DB)) error {
				return db.Callback().Raw().After("gorm:after_raw").Register(n, f)
			},
		},
	}

	for _, op := range ops {
		bName := fmt.Sprintf("gorm-plus:sq:before_%s", op.name)
		aName := fmt.Sprintf("gorm-plus:sq:after_%s", op.name)
		if err := op.before(bName, before); err != nil {
			return fmt.Errorf("SlowQuery: 注册 before_%s 钩子失败: %w", op.name, err)
		}
		if err := op.after(aName, after); err != nil {
			return fmt.Errorf("SlowQuery: 注册 after_%s 钩子失败: %w", op.name, err)
		}
	}
	return nil
}
