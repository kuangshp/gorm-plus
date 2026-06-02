package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/plugin"
	"gorm.io/gorm"
)

// ================== 多租户插件 ==================

// TenantConfig 多租户插件配置。
// T 为租户 ID 类型，支持 string、int64 等任意可比较类型。
// 字段优先级：TableFields > TenantFields > TenantField。
type TenantConfig[T comparable] struct {
	TenantField           string
	TenantFields          []TenantFieldConfig[T]
	TableFields           map[string][]TenantFieldConfig[T]
	AutoInjectJoinTables  *bool
	ExcludeJoinTables     []string
	JoinTableOverrides    []JoinTenantConfig[T]
	AllowGlobalUpdate     bool
	AllowGlobalDelete     bool
	AllowOverrideTenantID bool
	DuplicatePolicy       DuplicateTenantPolicy
	InjectMode            InjectMode
	ExcludeTables         []string
	GetTenantID           func(ctx context.Context) (T, bool)
}

// TenantFieldConfig 单个租户字段的注入配置，支持独立指定字段名和取值函数。
type TenantFieldConfig[T comparable] struct {
	Field       string
	GetTenantID func(ctx context.Context) (T, bool)
}

// JoinTenantConfig 联表中特定关联表的租户字段覆盖配置。
// 默认所有 JOIN 关联表自动注入租户条件、别名自动识别；
// 仅当关联表的租户字段名或取值函数与主表不同时才需要配置。
type JoinTenantConfig[T comparable] struct {
	Table       string
	Field       string
	GetTenantID func(ctx context.Context) (T, bool)
}

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
func RegisterTenant[T comparable](db *gorm.DB, cfg TenantConfig[T]) error {
	return plugin.RegisterTenant[T](db, toPluginTenantConfig(cfg))
}

// NewTenantPlugin 工厂函数，返回多租户插件实例供手动 db.Use() 注册。
//
//	p, err := gormplus.NewTenantPlugin(gormplus.TenantConfig[int64]{TenantField: "tenant_id"})
//	if err != nil { log.Fatal(err) }
//	db.Use(p)
func NewTenantPlugin[T comparable](cfg TenantConfig[T]) (gorm.Plugin, error) {
	return plugin.NewTenantPlugin[T](toPluginTenantConfig(cfg))
}

func toPluginTenantConfig[T comparable](cfg TenantConfig[T]) plugin.TenantConfig[T] {
	return plugin.TenantConfig[T]{
		TenantField:           cfg.TenantField,
		TenantFields:          toPluginTenantFields(cfg.TenantFields),
		TableFields:           toPluginTableFields(cfg.TableFields),
		AutoInjectJoinTables:  cfg.AutoInjectJoinTables,
		ExcludeJoinTables:     cfg.ExcludeJoinTables,
		JoinTableOverrides:    toPluginJoinTenantConfigs(cfg.JoinTableOverrides),
		AllowGlobalUpdate:     cfg.AllowGlobalUpdate,
		AllowGlobalDelete:     cfg.AllowGlobalDelete,
		AllowOverrideTenantID: cfg.AllowOverrideTenantID,
		DuplicatePolicy:       cfg.DuplicatePolicy,
		InjectMode:            cfg.InjectMode,
		ExcludeTables:         cfg.ExcludeTables,
		GetTenantID:           cfg.GetTenantID,
	}
}

func toPluginTenantFields[T comparable](fields []TenantFieldConfig[T]) []plugin.TenantFieldConfig[T] {
	if fields == nil {
		return nil
	}
	out := make([]plugin.TenantFieldConfig[T], len(fields))
	for i, field := range fields {
		out[i] = plugin.TenantFieldConfig[T]{
			Field:       field.Field,
			GetTenantID: field.GetTenantID,
		}
	}
	return out
}

func toPluginTableFields[T comparable](tableFields map[string][]TenantFieldConfig[T]) map[string][]plugin.TenantFieldConfig[T] {
	if tableFields == nil {
		return nil
	}
	out := make(map[string][]plugin.TenantFieldConfig[T], len(tableFields))
	for table, fields := range tableFields {
		out[table] = toPluginTenantFields(fields)
	}
	return out
}

func toPluginJoinTenantConfigs[T comparable](configs []JoinTenantConfig[T]) []plugin.JoinTenantConfig[T] {
	if configs == nil {
		return nil
	}
	out := make([]plugin.JoinTenantConfig[T], len(configs))
	for i, config := range configs {
		out[i] = plugin.JoinTenantConfig[T]{
			Table:       config.Table,
			Field:       config.Field,
			GetTenantID: config.GetTenantID,
		}
	}
	return out
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
