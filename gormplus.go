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

// ================== 多数据源管理 ==================

// DataSourceManager 多数据源管理器类型别名。
type DataSourceManager = datasource.Manager

// DataSourceGroupConfig 数据源组配置（一主多从）。
type DataSourceGroupConfig = datasource.GroupConfig

// DataSourceNodeConfig 单个数据源节点配置。
// 通过 Dialector 字段外部传入驱动，不内置任何数据库依赖：
//
//	// MySQL
//	import "gorm.io/driver/mysql"
//	DataSourceNodeConfig{Dialector: mysql.Open(dsn)}
//
//	// PostgreSQL
//	import "gorm.io/driver/postgres"
//	DataSourceNodeConfig{Dialector: postgres.Open(dsn)}
//
//	// SQLite（测试场景）
//	import "gorm.io/driver/sqlite"
//	DataSourceNodeConfig{Dialector: sqlite.Open(":memory:")}
type DataSourceNodeConfig = datasource.NodeConfig

// DataSourcePoolConfig 连接池配置。
// 零值字段自动使用 DataSourceDefaultPool（MaxOpen=50, MaxIdle=10, MaxLifetime=30min）。
type DataSourcePoolConfig = datasource.PoolConfig

var (
	// DS 全局多数据源管理器，支持一主多从、读写分离、context 自动切换。
	// 通过 Dialector 字段传入驱动，支持 MySQL / PostgreSQL / SQLite 等任意 gorm 驱动。
	//
	// MySQL 一主两从：
	//
	//   import "gorm.io/driver/mysql"
	//
	//   gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
	//       Master: gormplus.DataSourceNodeConfig{
	//           Dialector: mysql.Open("root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True"),
	//           Pool:      gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
	//       },
	//       Slaves: []gormplus.DataSourceNodeConfig{
	//           {Dialector: mysql.Open("root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True")},
	//           {Dialector: mysql.Open("root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True")},
	//       },
	//   })
	//
	// PostgreSQL：
	//
	//   import "gorm.io/driver/postgres"
	//   gormplus.DS.Register("pg", gormplus.DataSourceGroupConfig{
	//       Master: gormplus.DataSourceNodeConfig{
	//           Dialector: postgres.Open("host=localhost user=root password=pwd dbname=mydb port=5432 sslmode=disable"),
	//       },
	//   })
	//
	// Repository 层获取 DB（读走从库，写走主库）：
	//
	//   db, err := gormplus.DS.Auto(ctx)
	DS = datasource.NewManager()

	// DataSourceDefaultPool 默认连接池配置（生产推荐值：MaxOpen=50, MaxIdle=10, MaxLifetime=30min）。
	DataSourceDefaultPool = datasource.DefaultPool

	// NewDataSourceManager 创建独立的数据源管理器（多实例场景使用）。
	NewDataSourceManager = datasource.NewManager

	// DSWithName 将数据源名写入 ctx，DS.Auto(ctx) 会读取它选择对应数据源。
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
//	    LLike("username", username).                        // 空时自动跳过
//	    WhereIf(status != 0, "status = ?", status).         // false 时跳过
//	    BetweenIfNotZero("created_at", startTime, endTime). // 任一零值时跳过
//	    WhereIf(len(ids) > 0, "dept_id IN ?", ids).
//	    Build()
//	var total int64
//	built.Count(&total)
//	built.Order("created_at DESC").Limit(pageSize).Offset((page-1)*pageSize).Find(&list)
//
//	// OR 分组：WHERE status = 1 OR (role = 99 AND org_id = 10)
//	gormplus.Query[*model.Account](db, ctx).
//	    WhereIf(true, "status = ?", 1).
//	    OrGroup(func(q gormplus.IQueryBuilder) {
//	        q.WhereIf(role != 0, "role = ?", role).
//	          WhereIf(orgID != 0, "org_id = ?", orgID)
//	    }).Build().Find(&list)
var Query = query.NewQuery

// FindByPage 泛型分页查询，返回 (数据列表, 总数, error)。
// 适合结果直接映射到 model struct 的列表查询，内部 Count 时自动去掉 ORDER BY。
//
// 使用示例：
//
//	list, total, err := gormplus.FindByPage[*model.Account](
//	    gormplus.Query[*model.Account](db, ctx).
//	        LLike("username", username).
//	        WhereIf(status != 0, "status = ?", status).
//	        Build().Order("created_at DESC"),
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
//	    DeptName string `json:"deptName"` // 来自 JOIN
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
//	// 联表查询（别名 + 原生 SQL 条件）
//	list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    As("a").
//	    RawWhere("a.username LIKE ?", "%"+username+"%").
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Select(dao.AccountEntity.ID, dao.AccountEntity.Username).
//	    Find()
//
//	// AND 简单分组：WHERE (status = 1 AND role = 2)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroup(dao.AccountEntity.Status.Eq(1), dao.AccountEntity.Role.Eq(2)).
//	    Apply().Find()
//
//	// AND 函数分组（组内可用 WhereIf / Like 等）
//	// => WHERE (username LIKE '%admin' AND status = 1)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
//	    }).Apply().Find()
//
//	// OR 函数分组：WHERE status = 1 OR (username LIKE '%admin' AND role = 99)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereIf(true, dao.AccountEntity.Status.Eq(1)).
//	    OrGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
//	    }).Apply().Find()
func GenWrap[D query.GenDo[D]](do D) query.IGenWrapper[D] {
	return query.Wrap(do)
}

// ================== SingleFlight + 可插拔缓存 ==================

// SFCache 可插拔缓存接口，实现后通过 RegisterCache 注入替换默认内存缓存。
//
// Redis 实现示例：
//
//	type RedisSFCache struct {
//	    rdb    *redis.Client
//	    prefix string // 建议加前缀避免 key 冲突，如 "myapp:sf:"
//	}
//
//	func (c *RedisSFCache) Get(key string) (any, bool) {
//	    val, err := c.rdb.Get(context.Background(), c.prefix+key).Bytes()
//	    if err != nil { return nil, false }
//	    var result any
//	    if err := json.Unmarshal(val, &result); err != nil { return nil, false }
//	    return result, true
//	}
//	func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
//	    b, _ := json.Marshal(val)
//	    c.rdb.Set(context.Background(), c.prefix+key, b, ttl)
//	}
//	func (c *RedisSFCache) Del(key string) {
//	    c.rdb.Del(context.Background(), c.prefix+key)
//	}
//
//	// 启动时注册（必须在第一次调用 SF 之前）
//	gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})
type SFCache = sf.SFCache

// MemoryCache 内置内存缓存，实现 SFCache 接口。
// 不调用 RegisterCache 时，SF 自动懒初始化此类型，无需手动创建。
type MemoryCache = sf.MemoryCache

// DefaultSFTTL SF 不传 ttl 时的默认缓存时长（5 分钟）。
var DefaultSFTTL = sf.DefaultSFTTL

// RegisterCache 注册自定义缓存实现，程序启动时调用一次。
// 注册后所有 SF / SFWithTTL / SFInvalidate 均使用此缓存，替代默认内存缓存。
//
// ⚠️ 必须在第一次调用 SF 之前注册，否则内存缓存已懒初始化，注册无效。
//
// 方式一：内存缓存（默认，零配置）：
//
//	// 不调用 RegisterCache，SF 自动使用内存缓存
//	defer gormplus.StopSFCache() // 退出时停止后台清理 goroutine
//
// 方式二：Redis 缓存（多实例部署推荐）：
//
//	gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})
//	// Redis 模式下无需调用 StopSFCache，连接生命周期由用户自行管理
func RegisterCache(c sf.SFCache) {
	sf.RegisterCache(c)
}

// NewMemoryCache 显式创建内存缓存实例，适合单元测试替换默认缓存。
func NewMemoryCache() *sf.MemoryCache {
	return sf.NewMemoryCache()
}

// SF 通用 singleflight + 缓存查询封装，防止缓存击穿。
//
// 参数：
//   - fn:     实际查询函数，闭包原封不动放入，类型安全
//   - fnName: 查询唯一标识，建议格式 "表名.方法名"，如 "Account.List"
//   - args:   影响查询结果的所有参数；map key 自动排序后哈希，顺序无关
//   - ttl:    可选，缓存时长；不传时使用 DefaultSFTTL（5 分钟）；传 0 等价于 SFNoCache
//
// 使用示例：
//
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

// SFWithTTL 与 SF 相同，但 ttl 为必填参数，语义更明确，避免误用可变参默认值。
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	return sf.SFWithTTL(fn, fnName, args, ttl)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，不缓存结果。
// 适合详情接口、余额查询等对实时性要求高、不允许读到旧数据的场景。
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
// args 须与查询时传入的完全一致（key-value 相同，顺序无关）。
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

// StopSFCache 停止内置内存缓存的后台过期清理 goroutine，应在应用退出时调用。
// 使用自定义缓存（Redis 等）时无需调用，连接由用户自行管理。
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
// 字段优先级：TableFields > TenantFields > TenantField。
type TenantConfig[T comparable] = plugin.TenantConfig[T]

// TenantFieldConfig 单个租户字段的注入配置，支持独立指定字段名和取值函数。
type TenantFieldConfig[T comparable] = plugin.TenantFieldConfig[T]

// JoinTenantConfig 联表中特定关联表的租户字段覆盖配置。
// 默认所有 JOIN 关联表自动注入租户条件、别名自动识别；
// 仅当关联表的租户字段名或取值函数与主表不同时才需要配置。
type JoinTenantConfig[T comparable] = plugin.JoinTenantConfig[T]

// InjectMode 租户条件注入方式。ModeScopes 和 ModeWhere 底层效果相同，保留兼容旧配置。
type InjectMode = plugin.InjectMode

// DuplicateTenantPolicy 当业务代码已手动写了租户字段条件时，插件的处理策略。
type DuplicateTenantPolicy = plugin.DuplicateTenantPolicy

var (
	// ModeScopes 默认注入方式（推荐）。
	ModeScopes = plugin.ModeScopes
	// ModeWhere 与 ModeScopes 底层效果相同，保留兼容旧配置。
	ModeWhere = plugin.ModeWhere

	// PolicySkip 发现已有租户 AND 条件时跳过注入（默认）。
	// 同时检测 OR 危险条件，发现则拒绝执行，防止绕过隔离。
	PolicySkip = plugin.PolicySkip
	// PolicyReplace 强制以 ctx 中的值替换业务代码写的租户条件。
	// 同时检测 OR 危险条件，发现则拒绝执行。
	PolicyReplace = plugin.PolicyReplace
	// PolicyAppend 不检查直接追加，性能最好，但可能产生重复条件。
	PolicyAppend = plugin.PolicyAppend
)

// RegisterTenant 向指定 DB 注册多租户插件，整个应用只需调用一次。
//
// 注册后所有 db.WithContext(ctx) 的 Query / Update / Delete / Create 操作
// 均自动注入租户条件，业务代码无需任何改动。
//
// 用法一：单字段（最简单，向后兼容）：
//
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	})
//
// 用法二：同一张表注入多个租户字段：
//
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantFields: []gormplus.TenantFieldConfig[int64]{
//	        {Field: "tenant_id"}, // 使用默认 WithTenantID 写入的值
//	        {Field: "org_id", GetTenantID: func(ctx context.Context) (int64, bool) {
//	            id, ok := ctx.Value("orgID").(int64)
//	            return id, ok && id != 0
//	        }},
//	    },
//	})
//	// 生成：WHERE `tenant_id` = 1001 AND `org_id` = 200
//
// 用法三：不同表用不同字段名：
//
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField: "tenant_id", // 兜底字段
//	    TableFields: map[string][]gormplus.TenantFieldConfig[int64]{
//	        "sys_contract": {{Field: "company_id"}},                          // 改用 company_id
//	        "sys_order":    {{Field: "tenant_id"}, {Field: "org_id", GetTenantID: orgGetter}}, // 两个字段
//	        "sys_log":      {},                                                // 空 slice = 跳过该表
//	    },
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
// 联表查询（JOIN 关联表自动注入，别名自动识别，零配置）：
//
//	db.WithContext(ctx).
//	    Table("sys_order a").
//	    Joins("LEFT JOIN sys_order_item b ON b.order_id = a.id").
//	    Find(&list)
//	// 自动生成：WHERE `a`.`tenant_id` = 1001 AND `b`.`tenant_id` = 1001
//
//	// 排除公共关联表
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField:       "tenant_id",
//	    ExcludeJoinTables: []string{"sys_dict", "sys_config"},
//	})
//
//	// 关联表字段名不同时覆盖
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	    JoinTableOverrides: []gormplus.JoinTenantConfig[int64]{
//	        {Table: "sys_contract_detail", Field: "company_id"},
//	    },
//	})
func RegisterTenant[T comparable](db *gorm.DB, cfg plugin.TenantConfig[T]) error {
	return plugin.RegisterTenant[T](db, cfg)
}

// NewTenantPlugin 工厂函数，返回多租户插件实例供手动 db.Use() 注册。
//
//	p, err := gormplus.NewTenantPlugin(gormplus.TenantConfig[int64]{TenantField: "tenant_id"})
//	if err != nil { log.Fatal(err) }
//	db.Use(p)
func NewTenantPlugin[T comparable](cfg plugin.TenantConfig[T]) (gorm.Plugin, error) {
	return plugin.NewTenantPlugin[T](cfg)
}

// WithTenantID 将租户 ID 写入 context，通常在中间件中调用。
//
//	// gin 中间件示例
//	func TenantMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        tenantID := int64(1001) // 从 JWT 解析
//	        ctx := gormplus.WithTenantID(c.Request.Context(), tenantID)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
func WithTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return plugin.WithTenantID(ctx, tenantID)
}

// TenantIDFromCtx 从 context 读取租户 ID，类型参数须与 WithTenantID 写入时一致。
func TenantIDFromCtx[T comparable](ctx context.Context) T {
	return plugin.TenantIDFromCtx[T](ctx)
}

// SkipTenant 返回跳过租户过滤的 context（超管操作、跨租户统计专用）。
// ⚠️ 应仅在受控的特权接口中使用。
//
//	ctx = gormplus.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无任何租户条件
func SkipTenant(ctx context.Context) context.Context {
	return plugin.SkipTenant(ctx)
}

// AllowGlobalOperation 返回临时允许无业务条件全表 Update / Delete 的新 context。
// 适合批量任务、数据迁移等需要整租户操作的内部场景。
//
// 默认情况下，无业务 WHERE 条件的全表操作会被拒绝（防止误操作）：
//
//	db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})
//	// Error: tenant: 禁止无业务条件的全表 Update（表: account）
//
//	// 临时放开（当前请求生效）
//	ctx = gormplus.AllowGlobalOperation(ctx)
//	db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0}) // 成功
func AllowGlobalOperation(ctx context.Context) context.Context {
	return plugin.AllowGlobalOperation(ctx)
}

// WithOverrideTenantID 将覆盖租户 ID 写入 context，切换到指定租户查询。
// 仅在 TenantConfig.AllowOverrideTenantID=true 时生效，默认关闭。
// 与 SkipTenant 的区别：仍受租户隔离保护，只是切换到目标租户，不是完全跳过。
//
//	// 注册时开启
//	gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField:           "tenant_id",
//	    AllowOverrideTenantID: true,
//	})
//
//	// 超管管理后台：查看租户 2002 的数据
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
// 参数 db 用于追加条件，tableName 为当前表名（小写，已去掉库名前缀和反引号）。
type DataPermissionInjectFn = plugin.DataPermissionInjectFn

// RegisterDataPermission 向指定 DB 注册数据权限插件，整个应用只需调用一次。
//
// 注册后所有 db.WithContext(ctx) 的 Query / Update / Delete 操作，
// 若 ctx 中存在通过 WithDataPermission 写入的注入函数，则自动调用注入数据权限条件。
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
//	        claims, err := jwt.ParseToken(c.GetHeader("Authorization"))
//	        if err != nil { c.Next(); return }
//	        injectFn := func(db *gorm.DB, tableName string) {
//	            switch claims.DataScope {
//	            case "2": // 本角色相关部门
//	                db.Where(tableName+".create_by IN (SELECT sys_user.user_id FROM sys_role_dept LEFT JOIN sys_user ON sys_user.dept_id = sys_role_dept.dept_id WHERE sys_role_dept.role_id = ?)", claims.RoleId)
//	            case "3": // 本部门
//	                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id = ?)", claims.DeptId)
//	            case "4": // 本部门及子部门
//	                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id IN (SELECT dept_id FROM sys_dept WHERE dept_path LIKE ?))", "%/"+strconv.FormatInt(claims.DeptId, 10)+"/%")
//	            case "5": // 仅本人
//	                db.Where(tableName+".create_by = ?", claims.UserId)
//	            // default: 全部数据，不加任何条件
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

// DataPermissionFromCtx 从 context 中读取数据权限注入函数，不存在时返回 nil。
func DataPermissionFromCtx(ctx context.Context) plugin.DataPermissionInjectFn {
	return plugin.DataPermissionFromCtx(ctx)
}

// SkipDataPermission 返回跳过数据权限过滤的 context（超管、定时任务、内部统计专用）。
//
//	ctx = gormplus.SkipDataPermission(ctx)
//	db.WithContext(ctx).Find(&allData) // 无数据权限条件
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
// Name 填 Go 结构体字段名（如 "UpdatedBy"）或数据库列名（如 "updated_by"）均可，
// 插件通过 gorm schema 自动解析实际列名。
type FieldConfig = plugin.FieldConfig

// FieldGetter 从 context 中获取字段值的函数类型，返回 any 支持 int64 / string 等任意类型。
type FieldGetter = plugin.FieldGetter

// context key 常量，用于在中间件和自动填充插件之间传递字段值。
// 支持同时传递最多 10 个字段，按用途建议如下：
//
//	// 中间件写入示例
//	ctx := context.WithValue(c.Request.Context(), gormplus.CtxContextKey1, claims.UserID)   // 操作人 ID
//	ctx  = context.WithValue(ctx,                 gormplus.CtxContextKey2, claims.Username) // 操作人姓名
//	c.Request = c.Request.WithContext(ctx)
var (
	CtxContextKey1  = plugin.CtxOperatorKey1  // 建议存操作人 ID
	CtxContextKey2  = plugin.CtxOperatorKey2  // 建议存操作人姓名
	CtxContextKey3  = plugin.CtxOperatorKey3  // 建议存部门 ID
	CtxContextKey4  = plugin.CtxOperatorKey4  // 自定义
	CtxContextKey5  = plugin.CtxOperatorKey5  // 自定义
	CtxContextKey6  = plugin.CtxOperatorKey6  // 自定义
	CtxContextKey7  = plugin.CtxOperatorKey7  // 自定义
	CtxContextKey8  = plugin.CtxOperatorKey8  // 自定义
	CtxContextKey9  = plugin.CtxOperatorKey9  // 自定义
	CtxContextKey10 = plugin.CtxOperatorKey10 // 自定义
)

// NewAutoFillPlugin 创建自动填充插件实例，通过 db.Use() 注册。
//
// 使用示例：
//
//	// 基础：操作人 ID（int64）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
//	// 进阶：操作人 ID + 操作人姓名（多字段）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true},
//	        {Name: "UpdatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true, OnUpdate: true},
//	        {Name: "CreatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true},
//	        {Name: "UpdatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
//	// UUID 操作人（string 类型）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey1), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	    },
//	}))
func NewAutoFillPlugin(cfg plugin.AutoFillConfig) *plugin.AutoFillPlugin {
	return plugin.NewAutoFillPlugin(cfg)
}

// CtxGetter 从 context 读取指定 key 的值，T 为期望类型。
// 类型不匹配或 key 不存在时返回 T 的零值。
// 内部自动调用 resolveCtx，注册 RegisterCtxResolver 后直接传 *gin.Context 也可生效。
//
// 使用示例：
//
//	gormplus.CtxGetter[int64](gormplus.CtxContextKey1)   // 读取 int64 类型操作人 ID
//	gormplus.CtxGetter[string](gormplus.CtxContextKey2)  // 读取 string 类型操作人姓名
//	gormplus.CtxGetter[string]("myKey")                  // 读取自定义 key 的值
func CtxGetter[T any](key any) plugin.FieldGetter {
	return plugin.CtxGetter[T](key)
}

// ================== 慢查询监控 ==================

// SlowQueryConfig 慢查询监控配置。
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

// ================== 代码生成器 ==================

// GeneratorConfig 代码生成器配置，通过 YAML 文件加载或直接构造。
type GeneratorConfig = generator.Config

// LoadGeneratorConfig 从 YAML 文件加载代码生成器配置。
//
// 使用示例：
//
//	cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
//	if err != nil { log.Fatal(err) }
func LoadGeneratorConfig(path string) (*generator.Config, error) {
	return generator.LoadConfig(path)
}

// Generate 执行代码生成，根据数据库表结构生成 Model / Repository / API 文件。
// 运行时会提示输入表名，直接回车则生成所有表的 Model（其他文件不生成）。
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
