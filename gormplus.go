// Package gormplus 是基于 gorm 和 gorm-gen 的增强扩展包统一入口。
//
// 用户只需 import "github.com/kuangshp/gorm-plus" 即可使用所有功能，无需逐一引入子包。
//
// # 模块总览
//
//	┌─────────────────┬──────────────────────────────────────────────────────┐
//	│  模块            │  说明                                                │
//	├─────────────────┼──────────────────────────────────────────────────────┤
//	│  Query          │  原生 gorm 链式条件构造器                             │
//	│  GenWrap        │  gorm-gen 类型安全链式构造器                          │
//	│  DS             │  多数据源管理（主从分离、读写分离）                    │
//	│  SF             │  SingleFlight + 可插拔缓存（防缓存击穿）              │
//	│  Tenant         │  多租户插件（自动注入 WHERE tenant_id = ?）           │
//	│  DataPermission │  数据权限插件（按角色/部门隔离数据）                  │
//	│  AutoFill       │  自动填充插件（创建人/更新人自动写入）                │
//	│  SlowQuery      │  慢查询监控插件                                       │
//	│  Generator      │  代码生成器（Model / Repository / API）               │
//	└─────────────────┴──────────────────────────────────────────────────────┘
//
// # 推荐初始化顺序（main.go）
//
//	func main() {
//	    // 1. 注册 ctx 解析器（gin 项目必须；go-zero / fiber 跳过）
//	    gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
//	        if ginCtx, ok := ctx.(*gin.Context); ok {
//	            return ginCtx.Request.Context()
//	        }
//	        return ctx
//	    })
//
//	    // 2. 打开 DB
//	    db, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})
//
//	    // 3. 注册多租户插件
//	    gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	        TenantField:   "tenant_id",
//	        ExcludeTables: []string{"sys_config", "sys_dict"},
//	    })
//
//	    // 4. 注册数据权限插件
//	    gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
//	        ExcludeTables: []string{"sys_config", "sys_dict"},
//	    })
//
//	    // 5. 注册自动填充插件
//	    db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	        Fields: []gormplus.FieldConfig{
//	            {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
//	            {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	        },
//	    }))
//
//	    // 6. 注册慢查询监控
//	    gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
//	        Threshold: 200 * time.Millisecond,
//	        Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
//	            log.Printf("[慢查询] cost=%v sql=%s", info.Duration, info.SQL)
//	        },
//	    })
//
//	    // 7. 注册多数据源
//	    gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
//	        Master: gormplus.DataSourceNodeConfig{DSN: masterDSN},
//	        Slaves: []gormplus.DataSourceNodeConfig{{DSN: slaveDSN}},
//	    })
//
//	    // 8. 注册 SF 缓存（可选，默认内存缓存）
//	    // gormplus.RegisterCache(&RedisSFCache{rdb: rdb})
//
//	    // 9. 优雅退出
//	    defer gormplus.StopSFCache()
//	    defer gormplus.DS.Close()
//	}
package gormplus

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/kuangshp/gorm-plus/datasource"
	"github.com/kuangshp/gorm-plus/generator"
	"github.com/kuangshp/gorm-plus/plugin"
	"github.com/kuangshp/gorm-plus/query"
	"github.com/kuangshp/gorm-plus/sf"
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
// gin 项目（必须注册）：
//
//	gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
//	    if ginCtx, ok := ctx.(*gin.Context); ok {
//	        return ginCtx.Request.Context()
//	    }
//	    return ctx
//	})
//
// go-zero / fiber 使用标准 context，无需注册。
func RegisterCtxResolver(fn func(context.Context) context.Context) {
	plugin.RegisterCtxResolver(fn)
}

// ================== 多数据源管理 ==================

// DataSourceManager 多数据源管理器类型别名。
type DataSourceManager = datasource.Manager

// DataSourceGroupConfig 数据源组配置（一主多从）。
type DataSourceGroupConfig = datasource.GroupConfig

// DataSourceNodeConfig 单个数据源节点配置。
type DataSourceNodeConfig = datasource.NodeConfig

// DataSourcePoolConfig 连接池配置。
type DataSourcePoolConfig = datasource.PoolConfig

var (
	// DS 全局多数据源管理器，支持一主多从、读写分离、context 自动切换。
	//
	// 注册示例：
	//
	//   gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
	//       Master: gormplus.DataSourceNodeConfig{
	//           DSN:  "root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True",
	//           Pool: gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
	//       },
	//       Slaves: []gormplus.DataSourceNodeConfig{
	//           {DSN: "root:pwd@tcp(slave:3306)/mydb?charset=utf8mb4&parseTime=True"},
	//       },
	//   })
	//
	// Repository 层获取 DB：
	//
	//   db, err := gormplus.DS.Auto(ctx) // 读走从库，写走主库
	DS = datasource.NewManager()

	// DataSourceDefaultPool 默认连接池配置（生产推荐值：MaxOpen=50, MaxIdle=10, MaxLifetime=30min）。
	DataSourceDefaultPool = datasource.DefaultPool

	// NewDataSourceManager 创建新的数据源管理器（多实例场景）。
	NewDataSourceManager = datasource.NewManager

	// DSWithName 将数据源名写入 ctx，DS.Auto(ctx) 会读取它。
	//   ctx = gormplus.DSWithName(ctx, "analytics")
	DSWithName = datasource.WithName

	// DSNameFrom 从 ctx 读取数据源名。
	DSNameFrom = datasource.NameFromCtx

	// DSWithRead 标记 ctx 为读操作，DS.Auto(ctx) 将选择从库。
	//   ctx = gormplus.DSWithRead(ctx)
	DSWithRead = datasource.WithRead

	// DSWithWrite 标记 ctx 为写操作，DS.Auto(ctx) 将选择主库。
	//   ctx = gormplus.DSWithWrite(ctx)
	DSWithWrite = datasource.WithWrite

	// DSIsRead 判断 ctx 是否标记了读操作。
	DSIsRead = datasource.IsRead

	// DSIsWrite 判断 ctx 是否标记了写操作。
	DSIsWrite = datasource.IsWrite
)

// ================== Query 原生 gorm 链式条件构造器 ==================

// IQueryBuilder 原生 gorm 扩展条件构造器接口。
// 链式拼装扩展条件后调用 Build() 返回原生 *gorm.DB，继续使用所有 gorm 原生方法。
type IQueryBuilder = query.IQueryBuilder

// Query 创建原生 gorm 链式条件构造器。
//
// 使用示例：
//
//	// 分页列表查询
//	built := gormplus.Query[*model.Account](db, ctx).
//	    LLike("username", username).
//	    WhereIf(status != 0, "status = ?", status).
//	    BetweenIfNotZero("created_at", startTime, endTime).
//	    Build()
//
//	var total int64
//	built.Count(&total)
//	built.Order("created_at DESC").Limit(pageSize).Offset((page-1)*pageSize).Find(&list)
//
//	// 联表查询 + 映射到 VO
//	list, total, err := gormplus.ScanByPage[AccountVO](
//	    gormplus.Query[*model.Account](db, ctx).
//	        LLike("a.username", username).
//	        Build().
//	        Select("a.id", "a.username", "d.name AS dept_name").
//	        Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
//	        Order("a.created_at DESC"),
//	    pageNum, pageSize,
//	)
var Query = query.NewQuery

// FindByPage 泛型分页查询，返回 (数据列表, 总数, error)。
// 内部执行两次 SQL（COUNT + SELECT），Count 时自动去掉 ORDER BY 避免性能问题。
//
// 使用示例：
//
//	list, total, err := gormplus.FindByPage[*model.Account](
//	    gormplus.Query[*model.Account](db, ctx).
//	        LLike("username", username).
//	        WhereIf(status != 0, "status = ?", status).
//	        Build().
//	        Order("created_at DESC"),
//	    pageNum, pageSize,
//	)
func FindByPage[T any](q *gorm.DB, pageNum, pageSize int) ([]T, int64, error) {
	return query.FindByPage[T](q, pageNum, pageSize)
}

// ScanByPage 泛型分页扫描，返回 (数据列表, 总数, error)。
// 使用 Scan 代替 Find，适合联表查询、自定义 SELECT 字段映射到 VO 的场景。
//
// 使用示例：
//
//	type AccountVO struct {
//	    ID       int64  `json:"id"`
//	    Username string `json:"username"`
//	    DeptName string `json:"deptName"`
//	}
//
//	list, total, err := gormplus.ScanByPage[AccountVO](
//	    gormplus.Query[*model.Account](db, ctx).
//	        LLike("a.username", username).
//	        Build().
//	        Select("a.id", "a.username", "d.name AS dept_name").
//	        Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
//	        Order("a.created_at DESC"),
//	    pageNum, pageSize,
//	)
func ScanByPage[T any](q *gorm.DB, pageNum, pageSize int) ([]T, int64, error) {
	return query.ScanByPage[T](q, pageNum, pageSize)
}

// ================== GenWrap gorm-gen 类型安全链式构造器 ==================

// IGenWrapper gorm-gen 扩展条件构建器接口。
// 只包含 gorm-gen 原生不支持的能力，所有方法链式调用，Apply() 后返回原生 DO。
type IGenWrapper[D query.GenDo[D]] = query.IGenWrapper[D]

// GenWrap 将 gorm-gen 生成的 DO 包裹为 IGenWrapper，开启扩展条件链式构建。
// 调用 Apply() 后返回原生 DO，可继续使用所有 gorm-gen 原生方法。
//
// 使用示例：
//
//	// 基础查询
//	list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    LLike(dao.AccountEntity.Username, username).
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Order(dao.AccountEntity.CreatedAt.Desc()).
//	    Limit(pageSize).Offset((page-1)*pageSize).
//	    Find()
//
//	// 联表查询（使用别名）
//	list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    As("a").
//	    RawWhere("a.username LIKE ?", "%"+username+"%").
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Select(dao.AccountEntity.ID, dao.AccountEntity.Username).
//	    Find()
//
//	// 函数分组（组内可用 WhereIf / Like 等完整能力）
//	list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
//	    }).Apply().Find()
//	// => WHERE (username LIKE '%admin' AND status = 1)
func GenWrap[D query.GenDo[D]](do D) query.IGenWrapper[D] {
	return query.Wrap(do)
}

// ================== SingleFlight + 可插拔缓存 ==================

// SFCache 可插拔缓存接口，实现后通过 RegisterCache 注入。
//
// Redis 实现示例：
//
//	type RedisSFCache struct{ rdb *redis.Client }
//
//	func (c *RedisSFCache) Get(key string) (any, bool) {
//	    val, err := c.rdb.Get(context.Background(), "sf:"+key).Bytes()
//	    if err != nil { return nil, false }
//	    var result any
//	    json.Unmarshal(val, &result)
//	    return result, true
//	}
//	func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
//	    b, _ := json.Marshal(val)
//	    c.rdb.Set(context.Background(), "sf:"+key, b, ttl)
//	}
//	func (c *RedisSFCache) Del(key string) {
//	    c.rdb.Del(context.Background(), "sf:"+key)
//	}
//
//	// 注册
//	gormplus.RegisterCache(&RedisSFCache{rdb: rdb})
type SFCache = sf.SFCache

// MemoryCache 内置内存缓存，实现 SFCache 接口。
// 不注册任何缓存时自动使用，无需显式创建。
type MemoryCache = sf.MemoryCache

// DefaultSFTTL SF 不传 ttl 时的默认缓存时长（5 分钟）。
var DefaultSFTTL = sf.DefaultSFTTL

// RegisterCache 注册自定义缓存实现（Redis / Memcached 等），程序启动时调用一次。
// 注册后所有 SF / SFWithTTL / SFInvalidate 均使用此缓存。
// 不注册时默认使用内置内存缓存。
//
// 使用示例：
//
//	// 内存缓存（默认，零配置）
//	// 不需要调用 RegisterCache，直接使用 SF 即可
//
//	// Redis 缓存
//	gormplus.RegisterCache(&RedisSFCache{rdb: rdb})
func RegisterCache(c sf.SFCache) {
	sf.RegisterCache(c)
}

// NewMemoryCache 显式创建内存缓存实例（通常不需要，SF 会自动懒初始化）。
func NewMemoryCache() *sf.MemoryCache {
	return sf.NewMemoryCache()
}

// SF 通用 singleflight + 缓存查询封装，防止缓存击穿。
//
// 参数：
//   - fn:     实际查询函数，原封不动放入闭包
//   - fnName: 查询唯一标识，建议格式 "表名.方法名"，如 "Account.List"
//   - args:   影响查询结果的所有参数（分页、筛选条件等）
//   - ttl:    可选，缓存时长；不传时使用 DefaultSFTTL（5 分钟）
//
// 使用示例：
//
//	// 带缓存（30 秒）
//	list, err := gormplus.SF(func() ([]*model.Account, error) {
//	    var result []*model.Account
//	    err := gormplus.Query[*model.Account](db, ctx).
//	        WhereIf(status != 0, "status = ?", status).
//	        Build().Find(&result)
//	    return result, err
//	}, "Account.List", map[string]any{"status": status, "page": pageNum}, 30*time.Second)
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	return sf.SF(fn, fnName, args, ttl...)
}

// SFWithTTL 与 SF 相同，但 ttl 为必填参数，语义更明确。
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	return sf.SFWithTTL(fn, fnName, args, ttl)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，不缓存结果。
// 适合详情接口、用户余额等对实时性要求高的场景。
//
// 使用示例：
//
//	account, err := gormplus.SFNoCache(func() (*model.Account, error) {
//	    var a model.Account
//	    err := db.WithContext(ctx).Where("id = ?", id).First(&a).Error
//	    return &a, err
//	}, "Account.Detail", map[string]any{"id": id})
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return sf.SFNoCache(fn, fnName, args)
}

// SFInvalidate 主动使指定查询的缓存立即失效，通常在写操作后调用。
// args 需要与查询时传入的完全一致。
//
// 使用示例：
//
//	func (s *AccountService) Update(ctx context.Context, id int64) error {
//	    if err := repo.Update(ctx, id); err != nil { return err }
//	    gormplus.SFInvalidate("Account.List", map[string]any{"status": 1})
//	    return nil
//	}
func SFInvalidate(fnName string, args map[string]any) {
	sf.SFInvalidate(fnName, args)
}

// StopSFCache 停止内置内存缓存的后台清理 goroutine，应在应用退出时调用。
// 使用自定义缓存（Redis 等）时无需调用，由用户自行管理连接生命周期。
//
// 使用示例：
//
//	func main() {
//	    defer gormplus.StopSFCache()
//	    // ... 启动服务
//	}
func StopSFCache() {
	sf.StopSFCache()
}

// ================== 多租户插件 ==================

// TenantConfig 多租户插件配置。
// T 为租户 ID 类型，支持 string、int64 等任意可比较类型。
//
// 三种用法：
//
//	// 用法一：单字段（向后兼容）
//	gormplus.TenantConfig[int64]{TenantField: "tenant_id"}
//
//	// 用法二：多字段（同一表注入多个字段）
//	gormplus.TenantConfig[int64]{TenantFields: []gormplus.TenantFieldConfig[int64]{...}}
//
//	// 用法三：不同表用不同字段名
//	gormplus.TenantConfig[int64]{TenantField: "tenant_id", TableFields: map[string][]...{...}}
type TenantConfig[T comparable] = plugin.TenantConfig[T]

// TenantFieldConfig 单个租户字段的注入配置。
type TenantFieldConfig[T comparable] = plugin.TenantFieldConfig[T]

// JoinTenantConfig 联表中特定关联表的租户字段覆盖配置。
// 默认所有 JOIN 关联表自动注入，别名从 JOIN 语句中自动解析；
// 仅当关联表字段名与主表不同时才需要配置。
type JoinTenantConfig[T comparable] = plugin.JoinTenantConfig[T]

// InjectMode 租户条件注入方式（ModeScopes / ModeWhere 效果相同，保留兼容）。
type InjectMode = plugin.InjectMode

// DuplicateTenantPolicy 当业务代码已手动写了租户字段条件时，插件的处理策略。
type DuplicateTenantPolicy = plugin.DuplicateTenantPolicy

var (
	// ModeScopes 默认注入方式（推荐）。
	ModeScopes = plugin.ModeScopes
	// ModeWhere 与 ModeScopes 效果相同，保留兼容旧配置。
	ModeWhere = plugin.ModeWhere

	// PolicySkip 发现已有租户 AND 条件时跳过注入（默认）。
	PolicySkip = plugin.PolicySkip
	// PolicyReplace 强制以 ctx 中的值替换业务代码写的租户条件。
	PolicyReplace = plugin.PolicyReplace
	// PolicyAppend 不检查直接追加，性能最好但可能重复。
	PolicyAppend = plugin.PolicyAppend
)

// RegisterTenant 向指定 DB 注册多租户插件，整个应用只需调用一次。
//
// 注册后所有 db.WithContext(ctx) 的 Query / Update / Delete / Create 操作
// 均自动注入租户条件，业务代码无需任何改动。
//
// 使用示例：
//
//	// 单字段
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
//	// 多字段
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantFields: []gormplus.TenantFieldConfig[int64]{
//	        {Field: "tenant_id"},
//	        {Field: "org_id", GetTenantID: func(ctx context.Context) (int64, bool) {
//	            id, ok := ctx.Value("orgID").(int64)
//	            return id, ok && id != 0
//	        }},
//	    },
//	})
//
//	// 不同表用不同字段名
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	    TableFields: map[string][]gormplus.TenantFieldConfig[int64]{
//	        "sys_contract": {{Field: "company_id"}},
//	        "sys_order":    {{Field: "tenant_id"}, {Field: "org_id", GetTenantID: orgGetter}},
//	        "sys_log":      {}, // 空 slice = 跳过该表
//	    },
//	})
func RegisterTenant[T comparable](db *gorm.DB, cfg plugin.TenantConfig[T]) error {
	return plugin.RegisterTenant[T](db, cfg)
}

// NewTenantPlugin 工厂函数，返回多租户插件实例供手动 db.Use() 注册。
func NewTenantPlugin[T comparable](cfg plugin.TenantConfig[T]) (gorm.Plugin, error) {
	return plugin.NewTenantPlugin[T](cfg)
}

// WithTenantID 将租户 ID 写入 context，通常在中间件中调用。
//
//	// gin 中间件
//	ctx := gormplus.WithTenantID(c.Request.Context(), int64(1001))
//	c.Request = c.Request.WithContext(ctx)
func WithTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return plugin.WithTenantID(ctx, tenantID)
}

// TenantIDFromCtx 从 context 读取租户 ID，类型参数须与写入时一致。
func TenantIDFromCtx[T comparable](ctx context.Context) T {
	return plugin.TenantIDFromCtx[T](ctx)
}

// SkipTenant 返回跳过租户过滤的 context（超管操作、跨租户统计专用）。
//
// ⚠️ 应仅在受控的特权接口中使用。
//
//	ctx = gormplus.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无任何租户条件
func SkipTenant(ctx context.Context) context.Context {
	return plugin.SkipTenant(ctx)
}

// AllowGlobalOperation 返回临时允许无业务条件全表 Update / Delete 的新 context。
// 适合批量任务、数据迁移等场景。
//
//	ctx = gormplus.AllowGlobalOperation(ctx)
//	db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})
func AllowGlobalOperation(ctx context.Context) context.Context {
	return plugin.AllowGlobalOperation(ctx)
}

// WithOverrideTenantID 将覆盖租户 ID 写入 context。
// 仅在 TenantConfig.AllowOverrideTenantID=true 时生效。
// 适合超管管理后台查看指定租户数据。
//
//	ctx = gormplus.WithOverrideTenantID(ctx, int64(2002))
//	db.WithContext(ctx).Find(&list) // WHERE tenant_id = 2002
func WithOverrideTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return plugin.WithOverrideTenantID(ctx, tenantID)
}

// AddExcludeTable 运行时动态添加不参与租户过滤的表（线程安全）。
//
//	gormplus.AddExcludeTable[int64](db, "log_audit", "sys_trace")
func AddExcludeTable[T comparable](db *gorm.DB, tables ...string) error {
	return plugin.AddExcludeTable[T](db, tables...)
}

// RemoveExcludeTable 运行时动态移除排除表，使其重新参与租户过滤（线程安全）。
//
//	gormplus.RemoveExcludeTable[int64](db, "sys_dict")
func RemoveExcludeTable[T comparable](db *gorm.DB, tables ...string) error {
	return plugin.RemoveExcludeTable[T](db, tables...)
}

// ExcludedTables 返回当前所有排除表的名称列表快照（调试用）。
func ExcludedTables[T comparable](db *gorm.DB) ([]string, error) {
	return plugin.ExcludedTables[T](db)
}

// ================== 数据权限插件 ==================

// DataPermissionConfig 数据权限插件配置。
type DataPermissionConfig = plugin.DataPermissionConfig

// DataPermissionInjectFn 数据权限条件注入函数类型。
// 由业务层在中间件中实现，插件 Callback 触发时自动调用。
type DataPermissionInjectFn = plugin.DataPermissionInjectFn

// RegisterDataPermission 向指定 DB 注册数据权限插件，整个应用只需调用一次。
//
// 使用示例：
//
//	gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	})
func RegisterDataPermission(db *gorm.DB, cfg plugin.DataPermissionConfig) error {
	return plugin.RegisterDataPermission(db, cfg)
}

// NewDataPermissionPlugin 工厂函数，返回数据权限插件实例供手动 db.Use() 注册。
func NewDataPermissionPlugin(cfg plugin.DataPermissionConfig) (gorm.Plugin, error) {
	return plugin.NewDataPermissionPlugin(cfg)
}

// WithDataPermission 将数据权限注入函数写入 context，通常在中间件中调用。
//
// 使用示例：
//
//	func DataPermissionMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        claims, _ := jwt.ParseToken(c.GetHeader("Authorization"))
//	        injectFn := func(db *gorm.DB, tableName string) {
//	            switch claims.DataScope {
//	            case "3":
//	                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id = ?)", claims.DeptId)
//	            case "5":
//	                db.Where(tableName+".create_by = ?", claims.UserId)
//	            }
//	        }
//	        ctx := gormplus.WithDataPermission(c.Request.Context(), injectFn)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
func WithDataPermission(ctx context.Context, fn plugin.DataPermissionInjectFn) context.Context {
	return plugin.WithDataPermission(ctx, fn)
}

// DataPermissionFromCtx 从 context 中读取数据权限注入函数。
func DataPermissionFromCtx(ctx context.Context) plugin.DataPermissionInjectFn {
	return plugin.DataPermissionFromCtx(ctx)
}

// SkipDataPermission 返回跳过数据权限过滤的 context（超管、内部统计专用）。
//
//	ctx = gormplus.SkipDataPermission(ctx)
//	db.WithContext(ctx).Find(&allData)
func SkipDataPermission(ctx context.Context) context.Context {
	return plugin.SkipDataPermission(ctx)
}

// AddDataPermissionExcludeTable 运行时动态添加不参与数据权限过滤的表（线程安全）。
func AddDataPermissionExcludeTable(db *gorm.DB, tables ...string) error {
	return plugin.AddDataPermissionExcludeTable(db, tables...)
}

// RemoveDataPermissionExcludeTable 运行时动态移除排除表（线程安全）。
func RemoveDataPermissionExcludeTable(db *gorm.DB, tables ...string) error {
	return plugin.RemoveDataPermissionExcludeTable(db, tables...)
}

// DataPermissionExcludedTables 返回数据权限当前所有排除表快照（调试用）。
func DataPermissionExcludedTables(db *gorm.DB) ([]string, error) {
	return plugin.DataPermissionExcludedTables(db)
}

// ================== 自动填充插件 ==================

// AutoFillConfig 自动填充插件配置。
type AutoFillConfig = plugin.AutoFillConfig

// FieldConfig 单个字段的自动填充配置。
// Name 填 Go 结构体字段名（如 "UpdatedBy"）或列名（如 "updated_by"）均可。
type FieldConfig = plugin.FieldConfig

// FieldGetter 从 context 中获取字段值的函数类型，返回 any 支持任意类型。
type FieldGetter = plugin.FieldGetter

// context key 常量，用于在中间件和插件之间传递操作人等信息。
// 支持同时传递最多 10 个不同字段值（操作人 ID、姓名、部门 ID 等）。
var (
	CtxContextKey1  = plugin.CtxOperatorKey1
	CtxContextKey2  = plugin.CtxOperatorKey2
	CtxContextKey3  = plugin.CtxOperatorKey3
	CtxContextKey4  = plugin.CtxOperatorKey4
	CtxContextKey5  = plugin.CtxOperatorKey5
	CtxContextKey6  = plugin.CtxOperatorKey6
	CtxContextKey7  = plugin.CtxOperatorKey7
	CtxContextKey8  = plugin.CtxOperatorKey8
	CtxContextKey9  = plugin.CtxOperatorKey9
	CtxContextKey10 = plugin.CtxOperatorKey10
)

// NewAutoFillPlugin 创建自动填充插件实例，通过 db.Use() 注册。
//
// 使用示例：
//
//	// 操作人 ID（int64）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
//	// 操作人 ID + 姓名（多字段）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true},
//	        {Name: "UpdatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true, OnUpdate: true},
//	        {Name: "CreatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true},
//	        {Name: "UpdatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true, OnUpdate: true},
//	    },
//	}))
func NewAutoFillPlugin(cfg plugin.AutoFillConfig) *plugin.AutoFillPlugin {
	return plugin.NewAutoFillPlugin(cfg)
}

// CtxGetter 从 context 读取指定 key 的值，T 为期望类型。
// 类型不匹配时返回 T 的零值。内部自动调用 resolveCtx 兼容 *gin.Context。
//
// 使用示例：
//
//	gormplus.CtxGetter[int64](gormplus.CtxContextKey1)   // 读取 int64 类型操作人 ID
//	gormplus.CtxGetter[string](gormplus.CtxContextKey2)  // 读取 string 类型操作人姓名
//	gormplus.CtxGetter[string]("myCustomKey")            // 读取自定义 key 的值
func CtxGetter[T any](key any) plugin.FieldGetter {
	return plugin.CtxGetter[T](key)
}

// ================== 慢查询监控 ==================

// SlowQueryConfig 慢查询监控配置。
type SlowQueryConfig = query.SlowQueryConfig

// SlowQueryInfo 慢查询详情，包含执行时长、SQL、影响行数等。
type SlowQueryInfo = query.SlowQueryInfo

// RegisterSlowQuery 向指定 DB 注册慢查询监控插件。
//
// 使用示例：
//
//	gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
//	        zap.L().Warn("慢查询",
//	            zap.Duration("cost",  info.Duration),
//	            zap.String("table",   info.Table),
//	            zap.String("sql",     info.SQL),
//	            zap.Int64("rows",     info.RowsAffected),
//	        )
//	    },
//	})
func RegisterSlowQuery(db *gorm.DB, cfg query.SlowQueryConfig) error {
	return query.RegisterSlowQuery(db, cfg)
}

// ================== 代码生成器 ==================

// GeneratorConfig 代码生成器配置。
type GeneratorConfig = generator.Config

// LoadGeneratorConfig 从 YAML 文件加载代码生成器配置。
//
// 使用示例：
//
//	cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
func LoadGeneratorConfig(path string) (*generator.Config, error) {
	return generator.LoadConfig(path)
}

// Generate 执行代码生成，根据数据库表结构生成 Model / Repository / API 文件。
//
// 使用示例：
//
//	cfg, _ := gormplus.LoadGeneratorConfig("./generator.yaml")
//	if err := gormplus.Generate(cfg); err != nil {
//	    log.Fatal(err)
//	}
func Generate(cfg *generator.Config) error {
	return generator.Generate(cfg)
}
