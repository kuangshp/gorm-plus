package tenant

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// ================== 多租户插件 ==================
//
// 功能：
//   - 注册时指定租户字段名（如 "tenant_id"）和排除表集合
//   - 通过 context 传递租户 ID，Query/Update/Delete/Create 全自动注入租户条件
//   - 排除表不追加任何租户条件（公共字典、系统配置等）
//   - SkipTenant(ctx) 跳过租户过滤（超管、跨租户统计）
//   - 运行时动态添加 / 移除排除表
//
// 注册示例：
//
//	err := query.RegisterTenant(db, query.TenantConfig{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	    // GetTenantID 留空自动使用 DefaultGetTenantID
//	})
//
// Middleware 注入示例（Gin）：
//
//	func TenantMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        tenantID := c.GetHeader("X-Tenant-ID")  // 或从 JWT Claims 读
//	        ctx := query.WithTenantID(c.Request.Context(), tenantID)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
// 业务层无需改动，所有 SQL 自动携带租户条件：
//
//	db.WithContext(ctx).Find(&orders)
//	// → SELECT * FROM biz_order WHERE tenant_id = 'T001' AND deleted_at IS NULL
//
//	db.WithContext(ctx).Create(&order)
//	// → INSERT INTO biz_order (tenant_id, ...) VALUES ('T001', ...)
//
//	db.WithContext(ctx).Save(&order)
//	// → UPDATE biz_order SET ... WHERE tenant_id = 'T001' AND id = ?
//
// 超管跳过租户：
//
//	ctx = query.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无租户条件
//
// 动态排除表：
//
//	query.AddExcludeTable("log_audit", "sys_trace")
//	query.RemoveExcludeTable("sys_dict") // 重新参与租户过滤

// TenantConfig 多租户插件配置
type TenantConfig[T comparable] struct {
	// TenantField 租户字段名，如 "tenant_id"（必填）
	TenantField string

	// ExcludeTables 不参与租户过滤的表名列表（精确匹配，不含库名前缀）
	// 示例：[]string{"sys_config", "sys_dict_data"}
	ExcludeTables []string

	// GetTenantID 从 context 获取租户 ID 的函数（可选）。
	// 返回 (tenantID, ok)，ok=false 时跳过租户注入。
	// 为 nil 时自动使用 DefaultGetTenantID（读取 WithTenantID 写入的值）。
	GetTenantID func(ctx context.Context) (T, bool)
}

// tenantPlugin 实现 gorm.Plugin 接口
type tenantPlugin[T comparable] struct {
	cfg        TenantConfig[T]
	excludeSet map[string]struct{}
	mu         sync.RWMutex
}

var globalTenantPlugin any // 泛型不支持直接声明变量，用 any 存储

// RegisterTenant 向指定 DB 注册多租户插件。
// 必须在 gorm.Open 之后调用；整个应用只需注册一次。
// 注册后所有经过该 DB 的 Query/Update/Delete/Create 均自动注入租户条件。
//
// T 是租户 ID 类型，支持 int64 或 string。
//
// 示例（int64 租户）：
//
//	err := query.RegisterTenant[int64](db, query.TenantConfig[int64]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
// 示例（string 租户）：
//
//	err := query.RegisterTenant[string](db, query.TenantConfig[string]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
func RegisterTenant[T comparable](db *gorm.DB, cfg TenantConfig[T]) error {
	if cfg.TenantField == "" {
		return fmt.Errorf("query.RegisterTenant: TenantField 不能为空")
	}
	if cfg.GetTenantID == nil {
		cfg.GetTenantID = DefaultGetTenantID[T]
	}
	excludeSet := make(map[string]struct{}, len(cfg.ExcludeTables))
	for _, t := range cfg.ExcludeTables {
		excludeSet[strings.ToLower(t)] = struct{}{}
	}
	p := &tenantPlugin[T]{cfg: cfg, excludeSet: excludeSet}
	globalTenantPlugin = p
	return db.Use(p)
}

func (p *tenantPlugin[T]) Name() string { return "gorm-plus:tenant" }

func (p *tenantPlugin[T]) Initialize(db *gorm.DB) error {
	// Query / Update / Delete：WHERE 前注入租户条件
	for _, op := range []struct {
		name string
		reg  func(string, func(*gorm.DB)) error
	}{
		{"query", func(n string, fn func(*gorm.DB)) error {
			return db.Callback().Query().Before("gorm:query").Register(n, fn)
		}},
		{"update", func(n string, fn func(*gorm.DB)) error {
			return db.Callback().Update().Before("gorm:update").Register(n, fn)
		}},
		{"delete", func(n string, fn func(*gorm.DB)) error {
			return db.Callback().Delete().Before("gorm:delete").Register(n, fn)
		}},
	} {
		if err := op.reg("gorm-plus:tenant:"+op.name, p.injectWhere); err != nil {
			return fmt.Errorf("RegisterTenant: 注册 %s 钩子失败: %w", op.name, err)
		}
	}
	// Create：自动填充 struct 的租户字段值
	if err := db.Callback().Create().Before("gorm:create").Register(
		"gorm-plus:tenant:create", p.injectCreate,
	); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 create 钩子失败: %w", err)
	}
	return nil
}

// injectWhere 在 Query / Update / Delete 前追加 WHERE `tenant_id` = ?
func (p *tenantPlugin[T]) injectWhere(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	if p.shouldSkip(db.Statement.Context, db) {
		return
	}
	tenantID, ok := p.cfg.GetTenantID(db.Statement.Context)
	if !ok || isZero(tenantID) {
		return
	}
	db.Statement.Where(fmt.Sprintf("`%s` = ?", p.cfg.TenantField), tenantID)
}

// injectCreate 在 Create 前通过 gorm schema 反射给 struct 字段赋值租户 ID。
// 支持单个 struct 和 slice 批量创建。
func (p *tenantPlugin[T]) injectCreate(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	if p.shouldSkip(db.Statement.Context, db) {
		return
	}
	tenantID, ok := p.cfg.GetTenantID(db.Statement.Context)
	if !ok || isZero(tenantID) {
		return
	}
	if db.Statement.Schema == nil || !db.Statement.ReflectValue.IsValid() {
		return
	}
	f := db.Statement.Schema.LookUpField(p.cfg.TenantField)
	if f == nil {
		return
	}
	rv := db.Statement.ReflectValue
	if rv.Kind() == reflect.Slice {
		for i := 0; i < rv.Len(); i++ {
			elem := rv.Index(i)
			if elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			_ = f.Set(db.Statement.Context, elem, tenantID)
		}
	} else {
		elem := rv
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		_ = f.Set(db.Statement.Context, elem, tenantID)
	}
}

func (p *tenantPlugin[T]) shouldSkip(ctx context.Context, db *gorm.DB) bool {
	if skip, ok := ctx.Value(skipTenantKey{}).(bool); ok && skip {
		return true
	}
	return p.isExcluded(p.tableName(db))
}

func (p *tenantPlugin[T]) tableName(db *gorm.DB) string {
	if db.Statement == nil {
		return ""
	}
	name := db.Statement.Table
	// 去掉库名前缀（"mydb.orders" → "orders"）
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(strings.Trim(name, "`"))
}

func (p *tenantPlugin[T]) isExcluded(table string) bool {
	if table == "" {
		return true
	}
	p.mu.RLock()
	_, ok := p.excludeSet[table]
	p.mu.RUnlock()
	return ok
}

// isZero 判断泛型值是否为零值
func isZero[T comparable](v T) bool {
	return reflect.ValueOf(v).IsZero()
}

// ---- 动态排除表 ----

// AddExcludeTable 运行时动态添加不参与租户过滤的表（线程安全）。
func AddExcludeTable(tables ...string) {
	if globalTenantPlugin == nil {
		return
	}
	plugin, ok := globalTenantPlugin.(*tenantPlugin[any])
	if !ok {
		return
	}
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	for _, t := range tables {
		plugin.excludeSet[strings.ToLower(t)] = struct{}{}
	}
}

// RemoveExcludeTable 运行时动态移除排除表，使其重新参与租户过滤（线程安全）。
func RemoveExcludeTable(tables ...string) {
	if globalTenantPlugin == nil {
		return
	}
	plugin, ok := globalTenantPlugin.(*tenantPlugin[any])
	if !ok {
		return
	}
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	for _, t := range tables {
		delete(plugin.excludeSet, strings.ToLower(t))
	}
}

// ExcludedTables 返回当前所有排除表的快照（用于调试/查询）。
func ExcludedTables() []string {
	if globalTenantPlugin == nil {
		return nil
	}
	plugin, ok := globalTenantPlugin.(*tenantPlugin[any])
	if !ok {
		return nil
	}
	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	tables := make([]string, 0, len(plugin.excludeSet))
	for t := range plugin.excludeSet {
		tables = append(tables, t)
	}
	return tables
}

// ---- context 工具 ----

type skipTenantKey struct{}

// TenantIDWrapper 包装泛型租户 ID 值
type TenantIDWrapper[T any] struct {
	Value T
}

type tenantIDKey[T any] struct{}

// SkipTenant 返回跳过租户过滤的 context（超管操作、跨租户统计专用）。
//
//	ctx = query.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无 tenant_id 条件
func SkipTenant(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipTenantKey{}, true)
}

// WithTenantID 将租户 ID 写入 context，配合 DefaultGetTenantID 使用。
// 若项目已有自定义 ctx key，在 TenantConfig.GetTenantID 里读取即可，无需此函数。
//
//	// int64 租户
//	ctx = query.WithTenantID(ctx, int64(123))
//	// string 租户
//	ctx = query.WithTenantID(ctx, "t001")
func WithTenantID[T any](ctx context.Context, tenantID T) context.Context {
	return context.WithValue(ctx, tenantIDKey[T]{}, TenantIDWrapper[T]{Value: tenantID})
}

// TenantIDFromCtx 从 context 读取租户 ID，业务层可直接调用。
func TenantIDFromCtx[T any](ctx context.Context) T {
	var zero T
	if v := ctx.Value(tenantIDKey[T]{}); v != nil {
		if wrapper, ok := v.(TenantIDWrapper[T]); ok {
			return wrapper.Value
		}
	}
	return zero
}

// DefaultGetTenantID 默认租户 ID 获取函数，读取 WithTenantID 写入的值。
func DefaultGetTenantID[T any](ctx context.Context) (T, bool) {
	tid := TenantIDFromCtx[T](ctx)
	if reflect.ValueOf(tid).IsZero() {
		var zero T
		return zero, false
	}
	return tid, true
}
