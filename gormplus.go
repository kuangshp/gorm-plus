// Package gormplus 是基于 gorm 和 gorm-gen 的增强扩展包统一入口。
//
// 用户只需 import "github.com/kuangshp/gorm-plus" 即可使用全部功能，无需逐一引入子包。
//
// # 模块总览
//
//	┌─────────────────┬──────────────────────────────────────────────────────┐
//	│  模块            │  说明                                                │
//	├─────────────────┼──────────────────────────────────────────────────────┤
//	│  Query          │  原生 gorm 链式条件构造器                             │
//	│  DAL            │  SQL 文件化查询（embed + 泛型，复杂 SQL 首选）        │
//	│  GenWrap        │  gorm-gen 类型安全链式构造器                          │
//	│  DS             │  多数据源管理（任意驱动 / 主从分离 / 读写分离）        │
//	│  SF             │  SingleFlight + 可插拔缓存（防缓存击穿）              │
//	│  Tenant         │  多租户插件（自动注入 WHERE tenant_id = ?）           │
//	│  DataPermission │  数据权限插件（按角色 / 部门隔离数据）                │
//	│  AutoFill       │  自动填充插件（创建人 / 更新人自动写入）              │
//	│  SlowQuery      │  慢查询监控插件                                       │
//	│  Generator      │  代码生成器（Model / Repository / API）               │
//	└─────────────────┴──────────────────────────────────────────────────────┘
//
// # 推荐初始化顺序（main.go）
//
//	import (
//	    "gorm.io/driver/mysql"   // 按需替换为 postgres / sqlite / sqlserver
//	    gormplus "github.com/kuangshp/gorm-plus"
//	)
//
//	func main() {
//	    // ① 注册 ctx 解析器（gin 项目必须；go-zero / fiber 跳过）
//	    gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
//	        if ginCtx, ok := ctx.(*gin.Context); ok {
//	            return ginCtx.Request.Context()
//	        }
//	        return ctx
//	    })
//
//	    // ② 注册多数据源（Dialector 外部传入，不内置任何驱动）
//	    gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
//	        Master: gormplus.DataSourceNodeConfig{
//	            Dialector: mysql.Open("root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True"),
//	            Pool:      gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
//	        },
//	        Slaves: []gormplus.DataSourceNodeConfig{
//	            {Dialector: mysql.Open("root:pwd@tcp(slave:3306)/mydb?charset=utf8mb4&parseTime=True")},
//	        },
//	    })
//
//	    // ③ 打开 DB（多数据源场景也可从 DS.Write/Read 获取）
//	    db, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})
//
//	    // ④ 注册多租户插件
//	    gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	        TenantField:   "tenant_id",
//	        ExcludeTables: []string{"sys_config", "sys_dict"},
//	    })
//
//	    // ⑤ 注册数据权限插件
//	    gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
//	        ExcludeTables: []string{"sys_config", "sys_dict"},
//	    })
//
//	    // ⑥ 注册自动填充插件
//	    db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	        Fields: []gormplus.FieldConfig{
//	            {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
//	            {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	        },
//	    }))
//
//	    // ⑦ 注册慢查询监控
//	    gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
//	        Threshold: 200 * time.Millisecond,
//	        Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
//	            log.Printf("[慢查询] cost=%v table=%s sql=%s", info.Duration, info.Table, info.SQL)
//	        },
//	    })
//
//	    // ⑧ 注册 SF 缓存（可选，默认内存缓存；Redis 示例见 RegisterCache 注释）
//	    // gormplus.RegisterCache(&RedisSFCache{rdb: rdb})
//
//	    // ⑨ 优雅退出
//	    defer gormplus.StopSFCache()
//	    defer gormplus.DS.Close()
//	}
package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/plugin"
)

// ================== ctx 解析器 ==================

// RegisterCtxResolver 注册自定义 ctx 解析器，程序启动时调用一次。
//
// 解决 gin 项目直接传 *gin.Context 给 db.WithContext() 时，
// 插件无法从 *gin.Context 读取中间件写入 Request.Context() 数据的问题。
//
// 注册后包内所有插件（多租户、数据权限、自动填充）均自动使用此解析器，
// 业务代码可直接传 *gin.Context，无需手动调用 c.Request.Context()。
//
// gin 项目示例（必须注册）：
//
//	gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
//	    if ginCtx, ok := ctx.(*gin.Context); ok {
//	        return ginCtx.Request.Context()
//	    }
//	    return ctx
//	})
//
// go-zero / fiber 使用标准 context.Context，无需注册。
func RegisterCtxResolver(fn func(context.Context) context.Context) {
	plugin.RegisterCtxResolver(fn)
}

// 本包按模块拆分到多个文件，方便维护和查找。所有内容均在 package gormplus 下：
//
//	gormplus.go            - 包级文档 + RegisterCtxResolver
//	gormplus_datasource.go - 多数据源 DS / DSWithName 等
//	gormplus_query.go      - 原生 gorm 链式查询 Query / FindByPage / ScanByPage
//	gormplus_genwrap.go    - gorm-gen 类型安全链式 GenWrap / RawField
//	gormplus_sf.go         - SingleFlight + 缓存（含 P0 修复的 RawValue 协议）
//	gormplus_executor.go   - ExecuteQuery / ExecutePage / QueryOption 等
//	gormplus_tenant.go     - 多租户插件 RegisterTenant 等
//	gormplus_permission.go - 数据权限插件 RegisterDataPermission 等
//	gormplus_autofill.go   - 自动填充插件 NewAutoFillPlugin 等
//	gormplus_slowquery.go  - 慢查询监控 RegisterSlowQuery
//	gormplus_generator.go  - 代码生成器入口 Generate
//	gormplus_dal.go        - SQL 文件化查询 DALQuery / DALWithTx 等
