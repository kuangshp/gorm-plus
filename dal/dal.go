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

// 本文件仅保留包级文档（上方注释）。具体实现按职责拆分在以下文件：
//
//	provider.go  - DBProvider 接口与默认实现
//	loader.go    - SQLLoader 接口与 EmbedLoader
//	hook.go      - Hook 接口
//	options.go   - Option / WithDebug / WithHook / WithCacheCleanup
//	instance.go  - DAL 结构、NewDal、Close、WithDB、resolve、Preload 等
//	debug.go     - debugLog / debugWarnEmpty / runBeforeHooks / runAfterHooks
//	query.go     - Query / QueryOne / Count / QueryPage / Exec 等
//	tx.go        - WithTx / TxQuery / TxExec 等事务相关
//	must.go      - MustExec / MustQueryOne
