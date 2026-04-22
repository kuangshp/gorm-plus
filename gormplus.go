package gormplus

import (
	"context"
	"time"

	"gorm.io/gorm"

	"gorm-plus/datasource"
	"gorm-plus/generator"
	"gorm-plus/query"
	"gorm-plus/sf"
	"gorm-plus/tenant"
)

// ================== 统一入口 ==================
//
// 用户只需 import "github.com/.../gorm-plus" 即可使用所有功能。
//
// 导出的模块：
//   - DS           多数据源管理（主从分离、读写分离）
//   - Query        查询构建器（IQueryBuilder 原生 gorm）
//   - GenWrap      类型安全查询构建器（gorm-gen）
//   - SF           SingleFlight + 内存缓存（防击穿）
//   - Tenant       多租户插件
//   - Generator    代码生成器

// -------------------- 数据源管理 --------------------

// DS 多数据源管理器，支持一主多从、读写分离、context 自动切换。
//
// 使用示例：
//
//	// 初始化
//	DS.Register("default", datasource.GroupConfig{
//	    Master: datasource.NodeConfig{DSN: "root:pwd@tcp(localhost:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local"},
//	    Slaves: []datasource.NodeConfig{
//	        {DSN: "root:pwd@tcp(localhost:3307)/mydb?charset=utf8mb4&parseTime=True&loc=Local"},
//	    },
//	})
//

// 数据源管理类型别名，方便用户自定义初始化
type DataSourceManager = datasource.Manager

// DataSourceGroupConfig 数据源组配置（一主多从）
type DataSourceGroupConfig = datasource.GroupConfig

// DataSourceNodeConfig 单个数据源节点配置
type DataSourceNodeConfig = datasource.NodeConfig

// DataSourcePoolConfig 连接池配置
type DataSourcePoolConfig = datasource.PoolConfig

var (
	DS                    = datasource.NewManager() // 业务层自动获取（通过 context 自动选择数据源和主从）
	DataSourceDefaultPool = datasource.DefaultPool  // 默认连接池配置（生产推荐值）
	NewDataSourceManager  = datasource.NewManager   // 创建新的数据源管理器
	DSWithName            = datasource.WithName     // 写入数据源名到 ctx
	DSNameFrom            = datasource.NameFromCtx  // 读取数据源名
	DSWithRead            = datasource.WithRead     // 标记读操作
	DSWithWrite           = datasource.WithWrite    // 标记写操作
	DSIsRead              = datasource.IsRead
	DSIsWrite             = datasource.IsWrite
)

// -------------------- Query 查询构建器 --------------------

// Query 原生 gorm 链式条件构造器入口函数。
//
// 使用示例：
//
//	list, total, err := gormplus.FindByPage[*model.Order](
//	    gormplus.Query(db.WithContext(ctx).Model(&model.Order{})),
//	        EqIfNotZero("user_id", userID).
//	        LikeIfNotEmpty("order_no", keyword).
//	        OrderByDesc("created_at"),
//	    pageNum, pageSize,
//	)
var Query = query.NewQuery

// IQueryBuilder 原生 gorm 条件构造器接口（基于字符串列名）。
//
// 如需使用 gorm-gen 类型安全的链式条件构造器，请直接使用：
//
//	wrapper := query.GenWrap(dao.OrderEntity.WithContext(ctx))
//
// GenWrap 可避免字符串列名拼写错误，实现编译期类型检查。
type IQueryBuilder = query.IQueryBuilder

// FindByPage 泛型分页查询，返回 (数据列表, 总数, error)。
//
// 示例：
//
//	list, total, err := gormplus.FindByPage[*model.Order](
//	    gormplus.Query(db.WithContext(ctx).Model(&model.Order{})),
//	        EqIfNotZero("user_id", userID).
//	        OrderByDesc("created_at"),
//	    pageNum, pageSize,
//	)
func FindByPage[T any](q query.IQueryBuilder, pageNum, pageSize int) ([]T, int64, error) {
	return query.FindByPage[T](q, pageNum, pageSize)
}

// ScanByPage 泛型分页扫描（适合联表查询、自定义 VO）。
//
// 示例：
//
//	type OrderVO struct { ID int64; OrderNo string; Username string }
//	list, total, err := gormplus.ScanByPage[OrderVO](
//	    gormplus.Query(db.WithContext(ctx).Model(&model.Order{})),
//	        Select("o.id", "o.order_no", "u.username").
//	        LeftJoin("sys_user u ON u.id = o.user_id").
//	        EqIfNotZero("o.user_id", userID).
//	        OrderByDesc("o.created_at"),
//	    pageNum, pageSize,
//	)
func ScanByPage[T any](q query.IQueryBuilder, pageNum, pageSize int) ([]T, int64, error) {
	return query.ScanByPage[T](q, pageNum, pageSize)
}

// -------------------- SingleFlight + 缓存 --------------------

// SF 通用 singleflight + 内存缓存查询封装。
//
// 参数：
//   - fn     实际查询函数
//   - fnName 查询唯一标识，建议格式 "表名.方法名"，如 "Order.List"
//   - args   影响查询结果的所有参数（分页、筛选条件等）
//   - ttl    可选，缓存时长；不传时使用 DefaultSFTTL（5分钟）
//
// 示例：
//
//	list, err := gormplus.SF(func() ([]*model.Order, error) {
//	    var result []*model.Order
//	    err := gormplus.Query(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("user_id", userID).
//	        Find(&result)
//	    return result, err
//	}, "Order.List", map[string]any{"user_id": userID, "page": pageNum}, 30*time.Second)
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	return sf.SF(fn, fnName, args, ttl...)
}

// SFWithTTL 与 SF 相同，但 ttl 为必填参数，语义更明确。
// 适合不希望误用可变参默认值的场景。
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	return sf.SFWithTTL(fn, fnName, args, ttl)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，不缓存结果。
//
// 适合详情接口、用户余额等对实时性要求高的场景。
//
// 示例：
//
//	order, err := gormplus.SFNoCache(func() (*model.Order, error) {
//	    var o model.Order
//	    err := gormplus.Query(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("id", orderID).
//	        First(&o)
//	    return &o, err
//	}, "Order.Detail", map[string]any{"id": orderID})
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return sf.SFNoCache(fn, fnName, args)
}

// SFInvalidate 主动使指定查询的缓存立即失效（写操作后调用）。
//
// 示例：
//
//	func UpdateOrder(ctx context.Context, userID int64, ...) error {
//	    if err := repo.Update(ctx, ...); err != nil { return err }
//	    gormplus.SFInvalidate("Order.List", map[string]any{"user_id": userID})
//	    return nil
//	}
func SFInvalidate(fnName string, args map[string]any) {
	sf.SFInvalidate(fnName, args)
}

// StopSFCache 停止后台缓存清理 goroutine，应用退出时调用。
//
// 示例：
//
//	func main() {
//	    defer gormplus.StopSFCache()
//	    // ... 启动服务
//	}
func StopSFCache() {
	sf.StopSFCache()
}

// DefaultSFTTL 默认缓存时长（5分钟）
var DefaultSFTTL = sf.DefaultSFTTL

// -------------------- 多租户插件 --------------------

// TenantConfig 多租户插件配置（租户 ID 类型为 string）
type TenantConfig[T comparable] = tenant.TenantConfig[T]

// WithTenantID 将租户 ID 写入 context。
// 示例：
//
//	gormplus.WithTenantID(ctx, "tenant-abc")         // string 租户
//	gormplus.WithTenantID(ctx, int64(123))            // int64 租户
func WithTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return tenant.WithTenantID(ctx, tenantID)
}

// TenantIDFromCtx 从 context 读取租户 ID。
// 类型参数必须与写入时一致，否则返回零值。
func TenantIDFromCtx[T comparable](ctx context.Context) T {
	return tenant.TenantIDFromCtx[T](ctx)
}

// RegisterTenant 向指定 DB 注册多租户插件。
//
// 示例：
//
//	err := gormplus.RegisterTenant(db, gormplus.TenantConfig{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
func RegisterTenant[T comparable](db *gorm.DB, cfg tenant.TenantConfig[T]) error {
	return tenant.RegisterTenant[T](db, cfg)
}

// AddExcludeTable 运行时动态添加不参与租户过滤的表。
func AddExcludeTable[T comparable](db *gorm.DB, tables ...string) error {
	return tenant.AddExcludeTable[T](db, tables...)
}

// RemoveExcludeTable 运行时动态移除排除表。
func RemoveExcludeTable[T comparable](db *gorm.DB, tables ...string) error {
	return tenant.RemoveExcludeTable[T](db, tables...)
}

// ExcludedTables 返回当前所有排除表的快照。
func ExcludedTables[T comparable](db *gorm.DB) ([]string, error) {
	return tenant.ExcludedTables[T](db)
}

// SkipTenant 返回跳过租户过滤的 context（超管操作、跨租户统计专用）。
func SkipTenant(ctx context.Context) context.Context {
	return tenant.SkipTenant(ctx)
}

// -------------------- 慢查询监控 --------------------

// SlowQueryConfig 慢查询监控配置
type SlowQueryConfig = query.SlowQueryConfig

// SlowQueryInfo 慢查询详情
type SlowQueryInfo = query.SlowQueryInfo

// RegisterSlowQuery 向指定 DB 注册慢查询监控插件。
//
// 示例：
//
//	err := gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
//	    Threshold: 200 * time.Millisecond,
//	    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
//	        zap.L().Warn("慢查询", zap.Duration("cost", info.Duration), zap.String("sql", info.SQL))
//	    },
//	})
func RegisterSlowQuery(db *gorm.DB, cfg query.SlowQueryConfig) error {
	return query.RegisterSlowQuery(db, cfg)
}

// -------------------- 代码生成器 --------------------

// GeneratorConfig 代码生成器配置
type GeneratorConfig = generator.Config

// LoadGeneratorConfig 从 YAML 文件加载代码生成器配置。
//
// 示例：
//
//	cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
func LoadGeneratorConfig(path string) (*generator.Config, error) {
	return generator.LoadConfig(path)
}

// Generate 执行代码生成。
//
// 示例：
//
//	cfg, _ := gormplus.LoadGeneratorConfig("./generator.yaml")
//	if err := gormplus.Generate(cfg); err != nil {
//	    log.Fatal(err)
//	}
func Generate(cfg *generator.Config) error {
	return generator.Generate(cfg)
}

// GenWrap 从干净 Do 对象创建 GenWrapper。
func GenWrap[D query.GenDao[D]](do D) query.IGenWrapper[D] {
	return query.GenWrap(do)
}

type IGenWrapper[D query.GenDao[D]] = query.IGenWrapper[D]

// type GenDao[D any] = query.GenDao[D]
type AggResult = query.AggResult
