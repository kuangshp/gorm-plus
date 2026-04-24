// Package plugin 提供 gorm 多租户插件，通过 gorm Callback 钩子自动注入租户条件。
//
// # 工作原理
//
// 插件在 gorm 的 Query / Update / Delete / Create 操作前注册钩子：
//   - Query / Update / Delete：自动追加 WHERE `tenant_id` = ? 条件
//   - Create：通过反射自动填充 struct 的租户字段值
//
// 租户 ID 的流转路径：
//
//	JWT/Redis 解析 → gin 中间件 WithTenantID(ctx) → db.WithContext(ctx) → Callback 自动注入
//
// # 快速接入（三步）
//
// 第一步：程序启动时注册插件（一次）
//
//	func main() {
//	    db, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})
//
//	    // string 类型租户 ID
//	    if err := plugin.RegisterTenant(db, plugin.TenantConfig[string]{
//	        TenantField:   "tenant_id",
//	        ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	    }); err != nil {
//	        log.Fatal(err)
//	    }
//	}
//
// 第二步：gin 中间件解析 JWT/Redis 并写入 ctx
//
//	func TenantMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        // 从 JWT 解析租户 ID
//	        claims, err := jwt.ParseToken(c.GetHeader("Authorization"))
//	        if err != nil {
//	            c.Next()
//	            return
//	        }
//	        // 写入 ctx，后续 db.WithContext(ctx) 自动携带
//	        ctx := plugin.WithTenantID(c.Request.Context(), claims.TenantID)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
//	// 从 Redis 解析租户 ID 的示例
//	func TenantMiddlewareRedis(rdb *redis.Client) gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        token := c.GetHeader("Authorization")
//	        val, err := rdb.Get(c, "session:"+token).Result()
//	        if err != nil {
//	            c.Next()
//	            return
//	        }
//	        var session struct{ TenantID string }
//	        if err := json.Unmarshal([]byte(val), &session); err != nil {
//	            c.Next()
//	            return
//	        }
//	        ctx := plugin.WithTenantID(c.Request.Context(), session.TenantID)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
// 第三步：业务代码正常使用，无需任何改动
//
//	// 查询：自动追加 WHERE tenant_id = 'abc'
//	db.WithContext(ctx).Find(&list)
//
//	// 创建：自动填充 tenant_id 字段
//	db.WithContext(ctx).Create(&order)
//
//	// 更新：自动追加 WHERE tenant_id = 'abc'
//	db.WithContext(ctx).Model(&order).Updates(map[string]any{"status": 1})
//
//	// 删除：自动追加 WHERE tenant_id = 'abc'
//	db.WithContext(ctx).Delete(&order, id)
//
// # 注入方式（InjectMode）
//
// 插件支持两种租户条件注入方式，默认 ModeScopes：
//
//	ModeScopes（默认）和 ModeWhere 在 Callback 中效果相同，均通过 db.Statement.Where 注入。
//	注意：db.Scopes() 在 Callback 中无效，gorm 执行到 Callback 时已跳过 Scopes 处理阶段。
//	两种模式保留仅为兼容旧配置，推荐直接使用默认值 ModeScopes。
//
// # 超管跳过租户过滤
//
//	// 超管需要查看所有租户数据时，用 SkipTenant 跳过过滤
//	ctx = plugin.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&allTenantData) // 无 tenant_id 条件
//
// # 排除表（公共表不参与租户过滤）
//
//	// 注册时静态配置
//	plugin.RegisterTenant(db, plugin.TenantConfig[string]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	})
//
//	// 运行时动态添加排除表
//	plugin.AddExcludeTable[string](db, "log_audit", "sys_trace")
//
//	// 运行时动态移除排除表（重新参与租户过滤）
//	plugin.RemoveExcludeTable[string](db, "sys_dict")
//
//	// 查看当前所有排除表（调试用）
//	tables, _ := plugin.ExcludedTables[string](db)
//
// # 自定义租户 ID 获取函数
//
//	// 默认从 WithTenantID 写入的 ctx 读取，也可自定义（如从 Header 或缓存读取）
//	plugin.RegisterTenant(db, plugin.TenantConfig[string]{
//	    TenantField: "tenant_id",
//	    GetTenantID: func(ctx context.Context) (string, bool) {
//	        // 自定义逻辑：从 ctx 的 gin.Context 里读取
//	        tid, ok := ctx.Value("tenantID").(string)
//	        return tid, ok && tid != ""
//	    },
//	})
//
// # int64 类型租户 ID
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config"},
//	})
//
//	// 写入 ctx
//	ctx = plugin.WithTenantID(ctx, int64(1001))
package plugin

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// ================== 注入方式 ==================

// InjectMode 租户条件注入方式。
type InjectMode int

const (
	// ModeScopes 通过 db.Statement.AddClause 注入（默认，推荐）。
	//
	// 直接向 Statement 的 Clauses 写入 WHERE 条件，
	// 与 ModeWhere 的区别是条件会被 gorm 的 clause 系统统一处理，
	// 兼容联表、子查询等复杂场景，不会污染链式调用。
	// 生成的 SQL 示例：SELECT * FROM `order` WHERE `tenant_id` = 'abc'
	//
	// ⚠️ 注意：db.Scopes() 在 Callback 中调用无效（gorm 已过了处理 Scopes 的阶段），
	// 因此 ModeScopes 内部实际使用 Statement.Where 实现，效果与 ModeWhere 相同但更安全。
	ModeScopes InjectMode = iota

	// ModeWhere 直接操作 db.Statement.Where 注入。
	//
	// 与 ModeScopes 效果相同，保留此模式兼容旧配置。
	ModeWhere
)

// ================== 配置 ==================

// TenantConfig 多租户插件配置。
// T 为租户 ID 的类型，支持任意可比较类型（string、int64、uuid.UUID 等）。
//
// 示例：
//
//	// string 类型租户
//	plugin.TenantConfig[string]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	}
//
//	// int64 类型租户
//	plugin.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	}
type TenantConfig[T comparable] struct {
	// TenantField 数据库中的租户字段名（必填）。
	// 示例："tenant_id"、"org_id"
	TenantField string

	// InjectMode 租户条件注入方式，默认 ModeScopes。
	// 联表或子查询场景请保持默认值 ModeScopes。
	InjectMode InjectMode

	// ExcludeTables 不参与租户过滤的表名列表（精确匹配，不含库名前缀，不区分大小写）。
	// 通常用于公共配置表、字典表、菜单表等所有租户共享的数据。
	// 示例：[]string{"sys_config", "sys_dict_data", "sys_menu"}
	ExcludeTables []string

	// GetTenantID 自定义租户 ID 获取函数（可选）。
	// 签名：func(ctx context.Context) (tenantID T, ok bool)
	//   - ok=true：成功获取到租户 ID，插件正常注入条件
	//   - ok=false：未获取到（如未登录），插件跳过注入
	// 为 nil 时使用 DefaultGetTenantID，自动读取 WithTenantID 写入的值。
	//
	// 自定义示例（从自定义 ctx key 读取）：
	//
	//	GetTenantID: func(ctx context.Context) (string, bool) {
	//	    tid, ok := ctx.Value("myTenantKey").(string)
	//	    return tid, ok && tid != ""
	//	},
	GetTenantID func(ctx context.Context) (T, bool)
}

// ================== 插件实现 ==================

// tenantPlugin gorm.Plugin 接口实现，持有配置和排除表集合。
type tenantPlugin[T comparable] struct {
	cfg        TenantConfig[T]
	excludeSet map[string]struct{} // 排除表集合，key 为小写表名
	mu         sync.RWMutex        // 保护 excludeSet 的读写锁
}

// Name 返回插件唯一名称。
// 包含泛型类型信息，确保 string 和 int64 租户可以同时注册互不冲突。
func (p *tenantPlugin[T]) Name() string {
	return fmt.Sprintf("gorm-plus:tenant:%T", *new(T))
}

// Initialize 向 gorm 注册 Query / Update / Delete / Create 四类操作的钩子。
// gorm 框架在 db.Use(plugin) 时自动调用此方法。
func (p *tenantPlugin[T]) Initialize(db *gorm.DB) error {
	// Query / Update / Delete：在 SQL 执行前追加 WHERE 租户条件
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

	// Create：在创建前通过反射填充 struct 的租户字段，支持单条和批量
	if err := db.Callback().Create().Before("gorm:create").Register(
		p.Name()+":create", p.injectCreate,
	); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 create 钩子失败: %w", err)
	}
	return nil
}

// injectWhere 在 Query / Update / Delete 执行前注入租户 WHERE 条件。
// 根据 InjectMode 选择 Scopes 或直接 Where 注入方式。
func (p *tenantPlugin[T]) injectWhere(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	// 检查是否需要跳过（SkipTenant 或排除表）
	if p.shouldSkip(db.Statement.Context, db) {
		return
	}
	tenantID, ok := p.cfg.GetTenantID(db.Statement.Context)
	if !ok || isZero(tenantID) {
		// 未获取到租户 ID（如匿名请求），跳过注入
		return
	}

	sql := fmt.Sprintf("`%s` = ?", p.cfg.TenantField)

	// 两种模式在 Callback 中实现相同：直接操作 Statement.Where。
	// ⚠️ db.Scopes() 在 Callback 里调用无效，gorm 执行到 Callback 时已跳过 Scopes 处理阶段，
	// 因此无论 ModeScopes 还是 ModeWhere 都使用 Statement.Where 注入。
	db.Statement.Where(sql, tenantID)
}

// injectCreate 在 Create 执行前通过 gorm schema 反射为 struct 填充租户字段。
// 支持：
//   - 单条创建：db.Create(&order)
//   - 批量创建：db.Create(&orders)  // orders 为 slice
//   - 指针 slice：db.Create(&[]*Order{...})
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
	// Schema 或 ReflectValue 未初始化时跳过（如 db.Exec 等原生场景）
	if db.Statement.Schema == nil || !db.Statement.ReflectValue.IsValid() {
		return
	}
	// 查找租户字段，字段不存在时跳过（该表无租户字段）
	f := db.Statement.Schema.LookUpField(p.cfg.TenantField)
	if f == nil {
		return
	}

	rv := db.Statement.ReflectValue
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		// 批量创建：遍历每个元素逐一填充
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
		// 单条创建
		elem := rv
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if elem.IsValid() {
			_ = f.Set(db.Statement.Context, elem, tenantID)
		}
	}
}

// shouldSkip 判断当前操作是否应跳过租户过滤。
// 以下情况跳过：
//  1. ctx 中设置了 SkipTenant
//  2. 操作的表在排除表列表中
func (p *tenantPlugin[T]) shouldSkip(ctx context.Context, db *gorm.DB) bool {
	// 先解析 ctx，兼容 *gin.Context 等框架特定类型
	ctx = resolveCtx(ctx)
	// 检查是否显式跳过（超管场景）
	if skip, ok := ctx.Value(skipTenantKey{}).(bool); ok && skip {
		return true
	}
	// 检查是否为排除表（公共表）
	return p.isExcluded(p.tableName(db))
}

// tableName 从 gorm Statement 中提取纯表名（去掉库名前缀和反引号，转小写）。
// 示例："mydb.`order`" → "order"
func (p *tenantPlugin[T]) tableName(db *gorm.DB) string {
	if db.Statement == nil {
		return ""
	}
	name := db.Statement.Table
	// 去掉库名前缀（如 "mydb.order" → "order"）
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(strings.Trim(name, "`"))
}

// isExcluded 判断表名是否在排除列表中（读写锁保护，线程安全）。
func (p *tenantPlugin[T]) isExcluded(table string) bool {
	if table == "" {
		// 表名为空时跳过，避免对无 Model 的原生查询误注入
		return true
	}
	p.mu.RLock()
	_, ok := p.excludeSet[table]
	p.mu.RUnlock()
	return ok
}

// isZero 判断泛型值是否为零值（空字符串、0、nil 等）。
func isZero[T comparable](v T) bool {
	return reflect.ValueOf(v).IsZero()
}

// ================== 插件注册 ==================

// RegisterTenant 向指定 DB 注册多租户插件，整个应用只需调用一次。
//
// T 为租户 ID 类型，支持 string、int64 等任意可比较类型。
// 注册后所有经过 db.WithContext(ctx) 的 Query / Update / Delete / Create 操作
// 均自动注入租户条件，业务代码无需任何改动。
//
// 示例（string 租户，默认 ModeScopes）：
//
//	if err := plugin.RegisterTenant(db, plugin.TenantConfig[string]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	}); err != nil {
//	    log.Fatalf("注册多租户插件失败: %v", err)
//	}
//
// 示例（int64 租户）：
//
//	if err := plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	}); err != nil {
//	    log.Fatalf("注册多租户插件失败: %v", err)
//	}
//
// 示例（指定 ModeWhere 注入）：
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[string]{
//	    TenantField: "tenant_id",
//	    InjectMode:  plugin.ModeWhere,
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
	return db.Use(&tenantPlugin[T]{cfg: cfg, excludeSet: excludeSet})
}

// NewTenantPlugin 工厂函数，返回插件实例，适合需要手动管理插件生命周期的场景。
//
// 与 RegisterTenant 的区别：NewTenantPlugin 只创建实例，不自动注册到 DB；
// 需要手动调用 db.Use(p) 注册。
//
// 示例：
//
//	p, err := plugin.NewTenantPlugin(plugin.TenantConfig[string]{
//	    TenantField: "tenant_id",
//	})
//	if err != nil { ... }
//	db.Use(p)
func NewTenantPlugin[T comparable](cfg TenantConfig[T]) (gorm.Plugin, error) {
	if cfg.TenantField == "" {
		return nil, fmt.Errorf("NewTenantPlugin: TenantField 不能为空")
	}
	if cfg.GetTenantID == nil {
		cfg.GetTenantID = DefaultGetTenantID[T]
	}
	excludeSet := make(map[string]struct{}, len(cfg.ExcludeTables))
	for _, t := range cfg.ExcludeTables {
		excludeSet[strings.ToLower(t)] = struct{}{}
	}
	return &tenantPlugin[T]{cfg: cfg, excludeSet: excludeSet}, nil
}

// ================== 动态排除表 ==================

// getPlugin 从 gorm DB 的插件注册表中取出对应类型的租户插件实例。
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

// AddExcludeTable 运行时动态添加不参与租户过滤的表，线程安全。
// 适合运行时根据业务动态调整哪些表需要租户隔离。
//
// 示例：
//
//	// 将 log_audit 和 sys_trace 加入排除列表
//	if err := plugin.AddExcludeTable[string](db, "log_audit", "sys_trace"); err != nil {
//	    log.Println(err)
//	}
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

// RemoveExcludeTable 运行时动态移除排除表，使其重新参与租户过滤，线程安全。
//
// 示例：
//
//	// 让 sys_dict 重新参与租户过滤
//	if err := plugin.RemoveExcludeTable[string](db, "sys_dict"); err != nil {
//	    log.Println(err)
//	}
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

// ExcludedTables 返回当前所有排除表的名称列表快照，主要用于调试和运维查询。
//
// 示例：
//
//	tables, err := plugin.ExcludedTables[string](db)
//	if err == nil {
//	    fmt.Println("当前排除表:", tables)
//	}
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

// skipTenantKey 用于在 context 中标记跳过租户过滤，使用私有类型避免外部包 key 冲突。
type skipTenantKey struct{}

// tenantIDKey 泛型 context key。
// 不同 T 对应不同的 key 类型，string 和 int64 租户可以共存于同一 ctx 互不干扰。
type tenantIDKey[T comparable] struct{}

// SkipTenant 返回一个标记了跳过租户过滤的新 context。
// 用于超管查看所有租户数据、跨租户统计等特权场景。
//
// ⚠️ 注意：此 ctx 应仅在受控的超管接口中使用，避免在普通业务逻辑中误用。
//
// 示例：
//
//	// 超管查询所有租户的订单
//	ctx = plugin.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&allOrders) // SELECT * FROM order（无 tenant_id 条件）
func SkipTenant(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipTenantKey{}, true)
}

// WithTenantID 将租户 ID 写入 context，通常在 gin 中间件中调用。
// 后续所有经过 db.WithContext(ctx) 的数据库操作均自动携带该租户 ID。
//
// 示例：
//
//	// string 类型
//	ctx = plugin.WithTenantID(ctx, "tenant-abc")
//
//	// int64 类型
//	ctx = plugin.WithTenantID(ctx, int64(1001))
func WithTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return context.WithValue(ctx, tenantIDKey[T]{}, tenantID)
}

// TenantIDFromCtx 从 context 中读取租户 ID。
// 类型参数 T 必须与 WithTenantID 写入时一致，不一致时返回零值。
//
// 示例：
//
//	tenantID := plugin.TenantIDFromCtx[string](ctx)
//	if tenantID == "" {
//	    // 未登录或未设置租户
//	}
func TenantIDFromCtx[T comparable](ctx context.Context) T {
	var zero T
	// 先通过解析器转换 ctx，兼容 *gin.Context 等框架特定类型
	// 确保直接传 *gin.Context 也能读取到中间件写入 Request.Context() 的数据
	ctx = resolveCtx(ctx)
	if v := ctx.Value(tenantIDKey[T]{}); v != nil {
		if tid, ok := v.(T); ok {
			return tid
		}
	}
	return zero
}

// DefaultGetTenantID 默认租户 ID 获取函数，从 WithTenantID 写入的 ctx 中读取。
// 在 TenantConfig.GetTenantID 为 nil 时自动使用。
//
// 返回值：
//   - (tenantID, true)：成功读取到非零值的租户 ID
//   - (zero, false)：ctx 中无租户 ID 或为零值（如匿名请求），插件跳过注入
func DefaultGetTenantID[T comparable](ctx context.Context) (T, bool) {
	tid := TenantIDFromCtx[T](ctx)
	if reflect.ValueOf(tid).IsZero() {
		var zero T
		return zero, false
	}
	return tid, true
}
