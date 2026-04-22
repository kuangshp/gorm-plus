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
//	err := gormplus.RegisterTenant(db, gormplus.TenantConfig[string]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
// Middleware 注入示例（Gin）：
//
//	func TenantMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        tenantID := c.GetHeader("X-Tenant-ID")
//	        ctx := gormplus.WithTenantID(c.Request.Context(), tenantID)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
// 超管跳过租户：
//
//	ctx = gormplus.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无租户条件
//
// 动态排除表：
//
//	gormplus.AddExcludeTable(db, "log_audit", "sys_trace")
//	gormplus.RemoveExcludeTable(db, "sys_dict")

// TenantConfig 多租户插件配置，T 为租户 ID 类型（string、int64 等）
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

// Name 返回插件名称，包含类型信息避免多次注册冲突
func (p *tenantPlugin[T]) Name() string {
	return fmt.Sprintf("gorm-plus:tenant:%T", *new(T))
}

// Initialize 向 gorm 注册所有钩子
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
		if err := op.reg(p.Name()+":"+op.name, p.injectWhere); err != nil {
			return fmt.Errorf("RegisterTenant: 注册 %s 钩子失败: %w", op.name, err)
		}
	}
	// Create：自动填充 struct 的租户字段值
	if err := db.Callback().Create().Before("gorm:create").Register(
		p.Name()+":create", p.injectCreate,
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
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			elem := rv.Index(i)
			if elem.Kind() == reflect.Ptr {
				if elem.IsNil() {
					continue
				}
				elem = elem.Elem()
			}
			if elem.IsValid() {
				_ = f.Set(db.Statement.Context, elem, tenantID)
			}
		}
	default:
		elem := rv
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if elem.IsValid() {
			_ = f.Set(db.Statement.Context, elem, tenantID)
		}
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

// ---- 插件注册 ----

// RegisterTenant 向指定 DB 注册多租户插件。
// T 为租户 ID 类型，支持 string、int64 等任意可比较类型。
// 整个应用只需注册一次，注册后所有 Query/Update/Delete/Create 自动注入租户条件。
//
// 示例（string 租户）：
//
//	err := gormplus.RegisterTenant(db, gormplus.TenantConfig[string]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
// 示例（int64 租户）：
//
//	err := gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	})
func RegisterTenant[T comparable](db *gorm.DB, cfg TenantConfig[T]) error {
	if cfg.TenantField == "" {
		return fmt.Errorf("RegisterTenant: TenantField 不能为空")
	}
	if cfg.GetTenantID == nil {
		cfg.GetTenantID = DefaultGetTenantID[T]
	}
	excludeSet := make(map[string]struct{}, len(cfg.ExcludeTables))
	for _, t := range cfg.ExcludeTables {
		excludeSet[strings.ToLower(t)] = struct{}{}
	}
	p := &tenantPlugin[T]{cfg: cfg, excludeSet: excludeSet}
	return db.Use(p)
}

// ---- 动态排除表（通过 DB 获取插件实例，避免全局变量类型断言 bug）----

// getPlugin 从 gorm DB 的插件注册表中取出对应类型的租户插件。
func getPlugin[T comparable](db *gorm.DB) (*tenantPlugin[T], error) {
	name := fmt.Sprintf("gorm-plus:tenant:%T", *new(T))
	raw, ok := db.Config.Plugins[name]
	if !ok {
		return nil, fmt.Errorf("tenant: 插件 %q 未注册，请先调用 RegisterTenant", name)
	}
	p, ok := raw.(*tenantPlugin[T])
	if !ok {
		return nil, fmt.Errorf("tenant: 插件 %q 类型断言失败", name)
	}
	return p, nil
}

// AddExcludeTable 运行时动态添加不参与租户过滤的表（线程安全）。
func AddExcludeTable[T comparable](db *gorm.DB, tables ...string) error {
	p, err := getPlugin[T](db)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tables {
		p.excludeSet[strings.ToLower(t)] = struct{}{}
	}
	return nil
}

// RemoveExcludeTable 运行时动态移除排除表，使其重新参与租户过滤（线程安全）。
func RemoveExcludeTable[T comparable](db *gorm.DB, tables ...string) error {
	p, err := getPlugin[T](db)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tables {
		delete(p.excludeSet, strings.ToLower(t))
	}
	return nil
}

// ExcludedTables 返回当前所有排除表的快照（用于调试/查询）。
func ExcludedTables[T comparable](db *gorm.DB) ([]string, error) {
	p, err := getPlugin[T](db)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	tables := make([]string, 0, len(p.excludeSet))
	for t := range p.excludeSet {
		tables = append(tables, t)
	}
	return tables, nil
}

// ---- context 工具 ----

type skipTenantKey struct{}

// tenantIDKey 泛型 context key，不同 T 对应不同 key，互不干扰
type tenantIDKey[T comparable] struct{}

// SkipTenant 返回跳过租户过滤的 context（超管操作、跨租户统计专用）。
//
//	ctx = gormplus.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无 tenant_id 条件
func SkipTenant(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipTenantKey{}, true)
}

// WithTenantID 将租户 ID 写入 context，通常在 Middleware 中调用。
//
//	ctx = gormplus.WithTenantID(ctx, "tenant-abc")  // string 租户
//	ctx = gormplus.WithTenantID(ctx, int64(123))    // int64 租户
func WithTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return context.WithValue(ctx, tenantIDKey[T]{}, tenantID)
}

// TenantIDFromCtx 从 context 读取租户 ID。
// 类型参数 T 必须与 WithTenantID 写入时一致，否则返回零值。
func TenantIDFromCtx[T comparable](ctx context.Context) T {
	var zero T
	if v := ctx.Value(tenantIDKey[T]{}); v != nil {
		if tid, ok := v.(T); ok {
			return tid
		}
	}
	return zero
}

// DefaultGetTenantID 默认租户 ID 获取函数，读取 WithTenantID 写入的值。
func DefaultGetTenantID[T comparable](ctx context.Context) (T, bool) {
	tid := TenantIDFromCtx[T](ctx)
	if reflect.ValueOf(tid).IsZero() {
		var zero T
		return zero, false
	}
	return tid, true
}
