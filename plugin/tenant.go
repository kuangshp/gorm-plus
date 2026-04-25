// Package plugin 提供 gorm 多租户插件，通过 gorm Callback 钩子自动注入租户条件。
//
// # 工作原理
//
// 插件在 gorm 的 Query / Update / Delete / Create 操作前注册钩子：
//   - Query / Update / Delete：自动追加 WHERE `tenant_id` = ? 条件
//   - Create：通过反射自动填充 struct 的租户字段值
//   - JOIN 联表：自动解析 JOIN 语句中的关联表，同步注入租户条件（别名自动识别）
//
// 安全保护：
//   - 用户手动写了租户字段条件时，插件自动跳过注入，避免重复条件
//   - 发现 OR 条件中包含租户字段时，直接拒绝执行，防止租户隔离被绕过
//   - 禁止无业务条件的全表 Update / Delete，防止误操作
//
// # 用法一：单字段（最简单，向后兼容）
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	})
//
//	// gin 中间件写入租户 ID
//	func TenantMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        tenantID := int64(1001) // 从 JWT 解析
//	        ctx := plugin.WithTenantID(c.Request.Context(), tenantID)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
//	// 查询自动追加：WHERE `tenant_id` = 1001
//	db.WithContext(ctx).Find(&list)
//	// 创建自动填充：INSERT ... tenant_id = 1001
//	db.WithContext(ctx).Create(&order)
//
// # 用法二：多字段（同一张表注入多个租户字段）
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantFields: []plugin.TenantFieldConfig[int64]{
//	        {Field: "tenant_id"},
//	        {Field: "org_id", GetTenantID: func(ctx context.Context) (int64, bool) {
//	            id, ok := ctx.Value("orgID").(int64)
//	            return id, ok && id != 0
//	        }},
//	    },
//	    ExcludeTables: []string{"sys_config"},
//	})
//
//	// 中间件写入两个值
//	ctx := plugin.WithTenantID(c.Request.Context(), int64(1001))
//	ctx  = context.WithValue(ctx, "orgID", int64(200))
//	// 查询：WHERE `tenant_id` = 1001 AND `org_id` = 200
//
// # 用法三：不同表用不同字段名
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	    TableFields: map[string][]plugin.TenantFieldConfig[int64]{
//	        "sys_contract": {{Field: "company_id"}},
//	        "sys_order": {
//	            {Field: "tenant_id"},
//	            {Field: "org_id", GetTenantID: orgIDGetter},
//	        },
//	        "sys_log": {},
//	    },
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
// # 联表查询（JOIN 自动注入，别名自动识别）
//
//	// 零配置，直接写 JOIN，关联表和别名自动处理
//	db.WithContext(ctx).
//	    Table("sys_order a").
//	    Joins("LEFT JOIN sys_order_item b ON b.order_id = a.id").
//	    Joins("LEFT JOIN sys_user u ON u.id = a.user_id").
//	    Find(&list)
//	// 自动生成：
//	// WHERE `a`.`tenant_id` = 1001
//	//   AND `b`.`tenant_id` = 1001  ← 别名 b 自动识别
//	//   AND `u`.`tenant_id` = 1001  ← 别名 u 自动识别
//
//	// 排除不需要租户过滤的关联表
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField:       "tenant_id",
//	    ExcludeJoinTables: []string{"sys_dict", "sys_config"},
//	})
//
//	// 关联表字段名不同时，通过 JoinTableOverrides 覆盖
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	    JoinTableOverrides: []plugin.JoinTenantConfig[int64]{
//	        {Table: "sys_contract_detail", Field: "company_id"},
//	    },
//	})
//
// # 安全保护
//
//	// ① 重复条件：用户已写租户条件时，插件自动跳过，不会产生重复
//	db.WithContext(ctx).Where("tenant_id = ?", 1001).Find(&list)
//	// 插件检测到已有 tenant_id 条件，跳过注入
//	// 生成：WHERE tenant_id = 1001（只有一个）
//
//	// ② OR 绕过：发现租户字段出现在 OR 中，直接拒绝
//	db.WithContext(ctx).Where("tenant_id = ? OR status = 1", 9999).Find(&list)
//	// Error: tenant: 检测到租户字段 tenant_id 出现在 OR 条件中，禁止执行
//
//	// ③ 全表保护：无业务条件的 Update/Delete 被拒绝
//	db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})
//	// Error: tenant: 禁止无业务条件的全表 Update
//
//	// 临时放开全表保护
//	ctx = plugin.AllowGlobalOperation(ctx)
//	db.WithContext(ctx).Model(&Account{}).Updates(...)
//
// # 覆盖租户 ID（需开启 AllowOverrideTenantID）
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField:           "tenant_id",
//	    AllowOverrideTenantID: true,
//	})
//	ctx = plugin.WithOverrideTenantID(ctx, int64(2002))
//	db.WithContext(ctx).Find(&list) // WHERE tenant_id = 2002
//
// # 超管跳过
//
//	ctx = plugin.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无租户条件
package plugin

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// ================== 注入方式 ==================

// InjectMode 租户条件注入方式。
type InjectMode int

const (
	// ModeScopes 默认推荐（底层与 ModeWhere 相同，保留兼容旧配置）。
	ModeScopes InjectMode = iota
	// ModeWhere 直接操作 db.Statement.Where 注入。
	ModeWhere
)

// ================== 重复租户条件策略 ==================

// DuplicateTenantPolicy 当业务代码已手动写了租户字段条件时，插件的处理策略。
type DuplicateTenantPolicy int

const (
	// PolicySkip 跳过注入（默认）。
	// 发现业务代码已有租户字段的 AND 条件时，插件不再追加，以业务代码为准。
	// 同时检测 OR 危险条件，发现则拒绝执行。
	//
	//   db.WithContext(ctx).Where("tenant_id = ?", 1001).Find(&list)
	//   // 插件发现已有 tenant_id 条件 → 跳过注入
	//   // 生成：WHERE tenant_id = 1001（不重复）
	PolicySkip DuplicateTenantPolicy = iota

	// PolicyReplace 替换注入。
	// 先移除业务代码写的租户条件，再由插件注入 ctx 中的值，强制隔离。
	// 同时检测 OR 危险条件，发现则拒绝执行。
	// 适合不信任业务代码、需要严格强制租户隔离的场景。
	//
	//   db.WithContext(ctx).Where("tenant_id = ?", 9999).Find(&list) // 写了错误的值
	//   // 插件强制替换为 ctx 中的租户 ID
	//   // 生成：WHERE tenant_id = 1001（以 ctx 为准）
	PolicyReplace

	// PolicyAppend 追加注入（不去重）。
	// 不扫描已有条件，直接追加，性能最好，但可能产生重复条件。
	// 适合确定业务代码不会手动写租户条件、追求极致性能的场景。
	//
	//   db.WithContext(ctx).Where("tenant_id = ?", 1001).Find(&list)
	//   // 插件不检查，直接追加
	//   // 生成：WHERE tenant_id = 1001 AND tenant_id = 1001（重复但不影响结果）
	PolicyAppend
)

// ================== 多字段配置 ==================

// TenantFieldConfig 单个租户字段的注入配置。
//
// 示例：
//
//	plugin.TenantFieldConfig[int64]{Field: "tenant_id"}
//
//	plugin.TenantFieldConfig[int64]{
//	    Field: "org_id",
//	    GetTenantID: func(ctx context.Context) (int64, bool) {
//	        id, ok := ctx.Value("orgID").(int64)
//	        return id, ok && id != 0
//	    },
//	}
type TenantFieldConfig[T comparable] struct {
	// Field 数据库列名（必填）。示例："tenant_id"、"org_id"
	Field string

	// GetTenantID 从 context 获取字段值的函数（可选）。
	// 为 nil 时使用 TenantConfig.GetTenantID，最终回退到 DefaultGetTenantID。
	GetTenantID func(ctx context.Context) (T, bool)
}

// ================== 联表配置 ==================

// JoinTenantConfig 联表中特定关联表的租户字段覆盖配置。
//
// 仅在关联表的租户字段名或取值函数与主表不同时才需要配置。
// 默认：所有 JOIN 关联表自动使用主表的字段名和取值函数。
//
// 示例：
//
//	// sys_contract_detail 的租户字段是 company_id
//	plugin.JoinTenantConfig[int64]{
//	    Table: "sys_contract_detail",
//	    Field: "company_id",
//	}
type JoinTenantConfig[T comparable] struct {
	// Table 关联表名（必填，不含库名前缀，不区分大小写）。
	Table string
	// Field 关联表的租户字段名（可选，为空时使用主表默认字段名）。
	Field string
	// GetTenantID 取值函数（可选，为 nil 时使用全局默认）。
	GetTenantID func(ctx context.Context) (T, bool)
}

// ================== 主配置 ==================

// TenantConfig 多租户插件配置。
// T 为租户 ID 的类型，支持任意可比较类型（string、int64、uuid.UUID 等）。
type TenantConfig[T comparable] struct {
	// ── 主表字段配置（优先级：TableFields > TenantFields > TenantField）──────

	// TenantField 单字段快捷配置，所有表统一使用此字段名（用法一）。
	// 示例："tenant_id"
	TenantField string

	// TenantFields 多字段配置，同一张表注入多个租户字段（用法二）。
	TenantFields []TenantFieldConfig[T]

	// TableFields 按表名精确配置，不同表用不同字段（用法三，优先级最高）。
	// key 为小写表名；value 为空 slice 时跳过该表。
	TableFields map[string][]TenantFieldConfig[T]

	// ── 联表配置 ─────────────────────────────────────────────────────────────

	// AutoInjectJoinTables 是否自动为所有 JOIN 关联表注入租户条件，默认 true（nil 视为 true）。
	//
	// true：所有 Joins("LEFT JOIN xxx") 关联表自动注入，别名从语句中自动解析。
	//   生成格式：`alias`.`field` = ?（有别名）或 `table`.`field` = ?（无别名）
	//
	//   db.WithContext(ctx).
	//       Table("sys_order a").
	//       Joins("LEFT JOIN sys_order_item b ON b.order_id = a.id").
	//       Find(&list)
	//   // WHERE `a`.`tenant_id` = 1001 AND `b`.`tenant_id` = 1001
	//
	// false：关闭自动注入，需手动在业务代码中加条件。
	AutoInjectJoinTables *bool

	// ExcludeJoinTables 联表时不注入租户条件的关联表名（公共字典表、配置表等）。
	//
	//   ExcludeJoinTables: []string{"sys_dict", "sys_config"},
	//
	//   db.WithContext(ctx).Table("sys_order a").
	//       Joins("LEFT JOIN sys_dict d ON d.code = a.status_code"). // 不注入
	//       Find(&list)
	//   // WHERE `a`.`tenant_id` = 1001（sys_dict 不注入）
	ExcludeJoinTables []string

	// JoinTableOverrides 特定关联表的字段覆盖配置（通常不需要配置）。
	// 仅当某张关联表的字段名与主表不同时才需要。
	//
	//   JoinTableOverrides: []plugin.JoinTenantConfig[int64]{
	//       {Table: "sys_contract_detail", Field: "company_id"},
	//   }
	JoinTableOverrides []JoinTenantConfig[T]

	// ── 安全配置 ─────────────────────────────────────────────────────────────

	// AllowGlobalUpdate 允许无业务条件的全表 Update，默认 false（禁止）。
	// 临时放开：ctx = plugin.AllowGlobalOperation(ctx)
	AllowGlobalUpdate bool

	// AllowGlobalDelete 允许无业务条件的全表 Delete，默认 false（禁止）。
	AllowGlobalDelete bool

	// AllowOverrideTenantID 允许业务代码通过 WithOverrideTenantID 覆盖中间件注入的租户 ID。
	// 默认 false（不允许），防止租户隔离被绕过。
	// true 时适合超管管理后台、数据迁移等需要切换租户的特殊场景。
	AllowOverrideTenantID bool

	// DuplicatePolicy 当业务代码已手动写了租户字段条件时的处理策略，默认 PolicySkip。
	//
	// PolicySkip（默认）：发现已有租户 AND 条件时跳过注入，以业务代码为准。
	//
	//   db.WithContext(ctx).Where("tenant_id = ?", 1001).Find(&list)
	//   // 生成：WHERE tenant_id = 1001（不重复）
	//
	// PolicyReplace：始终以 ctx 中的租户 ID 覆盖业务代码写的值，强制隔离。
	//
	//   db.WithContext(ctx).Where("tenant_id = ?", 9999).Find(&list)
	//   // 生成：WHERE tenant_id = 1001（强制替换为 ctx 中的值）
	//
	// PolicyAppend：不检查直接追加，性能最好，但可能产生重复条件。
	//
	//   db.WithContext(ctx).Where("tenant_id = ?", 1001).Find(&list)
	//   // 生成：WHERE tenant_id = 1001 AND tenant_id = 1001（重复但不影响结果）
	//
	// ⚠️ 安全提示：无论哪种策略，租户字段出现在 OR 条件中时均会被拒绝执行，
	// 防止 WHERE (tenant_id = 9999 OR status = 1) 绕过租户隔离。
	DuplicatePolicy DuplicateTenantPolicy

	// ── 其他 ─────────────────────────────────────────────────────────────────

	// InjectMode 注入方式，默认 ModeScopes（与 ModeWhere 效果相同，保留兼容）。
	InjectMode InjectMode

	// ExcludeTables 主表排除列表（公共表不参与租户过滤）。
	// 示例：[]string{"sys_config", "sys_dict_data", "sys_menu"}
	ExcludeTables []string

	// GetTenantID 全局默认取值函数（可选）。
	// 为 nil 时使用 DefaultGetTenantID（读取 WithTenantID 写入的值）。
	GetTenantID func(ctx context.Context) (T, bool)
}

// ================== 插件实现 ==================

type tenantPlugin[T comparable] struct {
	cfg             TenantConfig[T]
	defaultField    []TenantFieldConfig[T]
	tableFields     map[string][]TenantFieldConfig[T]
	autoInjectJoin  bool
	excludeJoinSet  map[string]struct{}
	joinOverrideMap map[string]JoinTenantConfig[T]
	excludeSet      map[string]struct{}
	mu              sync.RWMutex
}

func (p *tenantPlugin[T]) Name() string {
	return fmt.Sprintf("gorm-plus:tenant:%T", *new(T))
}

func (p *tenantPlugin[T]) Initialize(db *gorm.DB) error {
	if err := db.Callback().Query().Before("gorm:query").
		Register(p.Name()+":query", p.injectWhere); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 query 钩子失败: %w", err)
	}
	if err := db.Callback().Update().Before("gorm:update").
		Register(p.Name()+":update_check", p.checkGlobalUpdate); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 update_check 钩子失败: %w", err)
	}
	if err := db.Callback().Update().After(p.Name()+":update_check").
		Register(p.Name()+":update", p.injectWhere); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 update 钩子失败: %w", err)
	}
	if err := db.Callback().Delete().Before("gorm:delete").
		Register(p.Name()+":delete_check", p.checkGlobalDelete); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 delete_check 钩子失败: %w", err)
	}
	if err := db.Callback().Delete().After(p.Name()+":delete_check").
		Register(p.Name()+":delete", p.injectWhere); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 delete 钩子失败: %w", err)
	}
	if err := db.Callback().Create().Before("gorm:create").
		Register(p.Name()+":create", p.injectCreate); err != nil {
		return fmt.Errorf("RegisterTenant: 注册 create 钩子失败: %w", err)
	}
	return nil
}

// ================== 安全检查 ==================

// checkTenantFieldSafety 在注入前检查用户已有 WHERE 条件的安全性。
//
// 两类问题：
//  1. 重复条件：用户已写了租户字段条件，插件无需再注入（直接跳过）
//  2. OR 绕过：租户字段出现在 OR 条件中，可能绕过隔离，直接拒绝
//
// 返回值：
//   - skip=true：用户已有该字段的 AND 条件，插件跳过注入（安全）
//   - skip=false, err=nil：未发现该字段，插件正常注入
//   - skip=false, err!=nil：发现危险的 OR 条件，拒绝执行
func (p *tenantPlugin[T]) checkTenantFieldSafety(db *gorm.DB, field string) (skip bool, err error) {
	if db.Statement == nil {
		return false, nil
	}
	whereClause, ok := db.Statement.Clauses["WHERE"]
	if !ok || whereClause.Expression == nil {
		return false, nil
	}

	// 遍历所有 WHERE 条件，检查租户字段出现情况
	switch expr := whereClause.Expression.(type) {
	case clause.Where:
		for _, cond := range expr.Exprs {
			s, e := checkExprForTenantField(cond, field)
			if e != nil {
				return false, e
			}
			if s {
				return true, nil
			}
		}
	}
	return false, nil
}

// checkExprForTenantField 递归检查单个 clause 表达式中的租户字段安全性。
//
// 返回：
//   - (true, nil)：在 AND 条件中找到租户字段 → 插件跳过注入
//   - (false, error)：在 OR 条件中发现租户字段 → 危险，拒绝执行
//   - (false, nil)：未发现租户字段 → 正常注入
func checkExprForTenantField(expr clause.Expression, field string) (skip bool, err error) {
	switch e := expr.(type) {
	case clause.Eq:
		// 普通 AND 等值条件：col = ?
		if colStr, ok := e.Column.(string); ok && colMatchesField(colStr, field) {
			return true, nil // 已有该字段的 AND 条件，跳过注入
		}
		if col, ok := e.Column.(clause.Column); ok && colMatchesField(col.Name, field) {
			return true, nil
		}

	case clause.AndConditions:
		// AND 分组，递归检查每个子条件
		for _, sub := range e.Exprs {
			s, e := checkExprForTenantField(sub, field)
			if e != nil {
				return false, e
			}
			if s {
				return true, nil
			}
		}

	case clause.OrConditions:
		// OR 分组：只要 OR 中涉及租户字段，立即拒绝
		// 因为 WHERE (tenant_id = 9999 OR status = 1) 会绕过租户隔离
		for _, sub := range e.Exprs {
			if containsTenantField(sub, field) {
				return false, fmt.Errorf(
					"tenant: 检测到租户字段 %q 出现在 OR 条件中，"+
						"可能绕过租户隔离，已拒绝执行。"+
						"如需跨租户查询请使用 plugin.SkipTenant(ctx)",
					field,
				)
			}
		}

	case clause.Expr:
		// 原生 SQL 字符串：扫描是否包含租户字段
		sql := strings.ToLower(e.SQL)
		fieldLower := strings.ToLower(field)
		if strings.Contains(sql, fieldLower) {
			// 包含 OR 关键字时视为危险
			if strings.Contains(sql, " or ") {
				return false, fmt.Errorf(
					"tenant: 原生 SQL 条件中检测到租户字段 %q 与 OR 同时出现，"+
						"可能绕过租户隔离，已拒绝执行。"+
						"如需跨租户查询请使用 plugin.SkipTenant(ctx)",
					field,
				)
			}
			// 纯 AND 的原生 SQL，视为用户已主动指定，跳过注入
			return true, nil
		}
	}
	return false, nil
}

// containsTenantField 判断表达式中是否包含指定字段（用于 OR 危险检测）。
func containsTenantField(expr clause.Expression, field string) bool {
	switch e := expr.(type) {
	case clause.Eq:
		if colStr, ok := e.Column.(string); ok {
			return colMatchesField(colStr, field)
		}
		if col, ok := e.Column.(clause.Column); ok {
			return colMatchesField(col.Name, field)
		}
	case clause.Expr:
		return strings.Contains(strings.ToLower(e.SQL), strings.ToLower(field))
	case clause.AndConditions:
		for _, sub := range e.Exprs {
			if containsTenantField(sub, field) {
				return true
			}
		}
	case clause.OrConditions:
		for _, sub := range e.Exprs {
			if containsTenantField(sub, field) {
				return true
			}
		}
	}
	return false
}

// colMatchesField 判断列名字符串是否匹配字段名（忽略表前缀和反引号）。
//
//	colMatchesField("a.tenant_id", "tenant_id") → true
//	colMatchesField("`tenant_id`", "tenant_id") → true
//	colMatchesField("tenant_id", "tenant_id")   → true
func colMatchesField(col, field string) bool {
	col = strings.ToLower(strings.Trim(col, "`"))
	field = strings.ToLower(field)
	// 去掉表前缀（如 "a.tenant_id" → "tenant_id"）
	if idx := strings.LastIndex(col, "."); idx >= 0 {
		col = col[idx+1:]
	}
	return col == field
}

// ================== 核心注入 ==================

// injectWhere 在 Query / Update / Delete 执行前注入租户 WHERE 条件。
func (p *tenantPlugin[T]) injectWhere(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	ctx := resolveCtx(db.Statement.Context)
	if p.shouldSkip(ctx, db) {
		return
	}

	tableName := p.tableName(db)
	fields, hitTable := p.fieldsFor(tableName)
	if hitTable && len(fields) == 0 {
		return
	}

	prefix := p.fieldPrefix(db)

	for _, f := range fields {
		// 根据 DuplicatePolicy 决定如何处理已有租户条件
		switch p.cfg.DuplicatePolicy {
		case PolicyAppend:
			// 直接追加，不扫描已有条件，性能最优
			// 适合确定业务代码不会手动写租户条件的场景

		case PolicyReplace:
			// 先移除业务代码写的租户字段条件，再由插件注入正确值
			// 适合需要强制隔离、不信任业务代码的严格模式
			// ⚠️ 仍会检测 OR 危险条件，发现则拒绝执行
			_, err := p.checkTenantFieldSafety(db, f.Field)
			if err != nil {
				_ = db.AddError(err)
				return
			}
			removeWhereField(db, f.Field)

		default: // PolicySkip（默认）
			// 扫描已有条件：
			//   - 发现 AND 条件中已有该字段 → 跳过注入（以业务代码为准）
			//   - 发现 OR 条件中有该字段 → 危险，拒绝执行
			//   - 未发现 → 正常注入
			skip, err := p.checkTenantFieldSafety(db, f.Field)
			if err != nil {
				_ = db.AddError(err)
				return
			}
			if skip {
				continue
			}
		}

		tenantID, ok := p.resolveTenantID(ctx, f.GetTenantID)
		if !ok {
			continue
		}

		var sql string
		if prefix != "" {
			sql = fmt.Sprintf("`%s`.`%s` = ?", prefix, f.Field)
		} else {
			sql = fmt.Sprintf("`%s` = ?", f.Field)
		}
		db.Statement.Where(sql, tenantID)
	}

	p.injectJoinWhere(db, ctx)
}

// removeWhereField 从 Statement.Clauses["WHERE"] 中移除指定字段的所有 AND 条件。
// 供 PolicyReplace 使用：先清除业务代码写的租户条件，再由插件重新注入正确值。
func removeWhereField(db *gorm.DB, field string) {
	if db.Statement == nil {
		return
	}
	whereClause, ok := db.Statement.Clauses["WHERE"]
	if !ok || whereClause.Expression == nil {
		return
	}
	w, ok := whereClause.Expression.(clause.Where)
	if !ok {
		return
	}
	filtered := w.Exprs[:0]
	for _, expr := range w.Exprs {
		if !exprContainsField(expr, field) {
			filtered = append(filtered, expr)
		}
	}
	w.Exprs = filtered
	whereClause.Expression = w
	db.Statement.Clauses["WHERE"] = whereClause
}

// exprContainsField 判断 clause 表达式是否包含指定字段（供 removeWhereField 使用）。
func exprContainsField(expr clause.Expression, field string) bool {
	switch e := expr.(type) {
	case clause.Eq:
		if colStr, ok := e.Column.(string); ok {
			return colMatchesField(colStr, field)
		}
		if col, ok := e.Column.(clause.Column); ok {
			return colMatchesField(col.Name, field)
		}
	case clause.Expr:
		return strings.Contains(strings.ToLower(e.SQL), strings.ToLower(field))
	case clause.AndConditions:
		for _, sub := range e.Exprs {
			if exprContainsField(sub, field) {
				return true
			}
		}
	}
	return false
}

// injectJoinWhere 自动为所有 JOIN 关联表注入租户条件。
//
// 流程：
//  1. AutoInjectJoinTables=false 时直接返回
//  2. 从 Statement.Joins 解析关联表名和别名（自动识别，无需配置）
//  3. 跳过 ExcludeJoinTables 中的表
//  4. 查找 JoinTableOverrides 中的覆盖配置
//  5. 安全检查（重复条件跳过，OR 危险拒绝）
//  6. 生成 `alias`.`field` = ? 条件
//
// 别名解析示例：
//
//	"LEFT JOIN sys_order_item b ON b.order_id = a.id"  → alias=b
//	"JOIN sys_user AS u ON u.id = a.user_id"           → alias=u
//	"LEFT JOIN sys_dept ON sys_dept.id = a.dept_id"    → 无别名，用表名
func (p *tenantPlugin[T]) injectJoinWhere(db *gorm.DB, ctx context.Context) {
	if !p.autoInjectJoin || db.Statement == nil || len(db.Statement.Joins) == 0 {
		return
	}

	defaultField := ""
	if len(p.defaultField) > 0 {
		defaultField = p.defaultField[0].Field
	}
	if defaultField == "" {
		return
	}

	for _, join := range db.Statement.Joins {
		tableName, alias := parseJoinTable(join.Name)
		if tableName == "" {
			continue
		}
		lowerTable := strings.ToLower(tableName)

		if _, excluded := p.excludeJoinSet[lowerTable]; excluded {
			continue
		}

		field := defaultField
		var overrideGetter func(context.Context) (T, bool)
		if override, ok := p.joinOverrideMap[lowerTable]; ok {
			if override.Field != "" {
				field = override.Field
			}
			overrideGetter = override.GetTenantID
		}

		// 安全检查
		skip, err := p.checkTenantFieldSafety(db, field)
		if err != nil {
			_ = db.AddError(err)
			return
		}
		if skip {
			continue
		}

		tenantID, ok := p.resolveTenantID(ctx, overrideGetter)
		if !ok {
			continue
		}

		prefix := alias
		if prefix == "" {
			prefix = tableName
		}
		db.Statement.Where(fmt.Sprintf("`%s`.`%s` = ?", prefix, field), tenantID)
	}
}

// parseJoinTable 从 JOIN 子句字符串中解析出表名和别名。
//
// 支持格式：
//
//	"LEFT JOIN sys_order_item b ON b.order_id = a.id"       → ("sys_order_item", "b")
//	"JOIN sys_user AS u ON u.id = a.user_id"                → ("sys_user", "u")
//	"LEFT JOIN sys_dept ON sys_dept.id = a.dept_id"         → ("sys_dept", "")
//	"INNER JOIN `sys_role` r ON r.id = a.role_id"           → ("sys_role", "r")
func parseJoinTable(joinStr string) (table, alias string) {
	upper := strings.ToUpper(joinStr)
	joinIdx := strings.Index(upper, "JOIN")
	if joinIdx < 0 {
		return "", ""
	}
	rest := strings.TrimSpace(joinStr[joinIdx+4:])
	if onIdx := strings.Index(strings.ToUpper(rest), " ON "); onIdx > 0 {
		rest = strings.TrimSpace(rest[:onIdx])
	}
	parts := strings.Fields(rest)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return strings.Trim(parts[0], "`"), ""
	case 2:
		return strings.Trim(parts[0], "`"), strings.Trim(parts[1], "`")
	default:
		if strings.EqualFold(parts[1], "AS") && len(parts) >= 3 {
			return strings.Trim(parts[0], "`"), strings.Trim(parts[2], "`")
		}
		return strings.Trim(parts[0], "`"), strings.Trim(parts[1], "`")
	}
}

// injectCreate 在 Create 前为 struct 填充租户字段值。
func (p *tenantPlugin[T]) injectCreate(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	ctx := resolveCtx(db.Statement.Context)
	if p.shouldSkip(ctx, db) {
		return
	}
	if db.Statement.Schema == nil || !db.Statement.ReflectValue.IsValid() {
		return
	}

	tableName := p.tableName(db)
	fields, hitTable := p.fieldsFor(tableName)
	if hitTable && len(fields) == 0 {
		return
	}

	for _, f := range fields {
		sf := db.Statement.Schema.LookUpField(f.Field)
		if sf == nil {
			continue
		}
		tenantID, ok := p.resolveTenantID(ctx, f.GetTenantID)
		if !ok {
			continue
		}
		p.fillField(db, sf, tenantID)
	}
}

// fillField 对 struct 或 slice 的每个元素填充字段值。
func (p *tenantPlugin[T]) fillField(db *gorm.DB, f *schema.Field, val T) {
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
				_ = f.Set(db.Statement.Context, elem, val)
			}
		}
	default:
		elem := rv
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if elem.IsValid() {
			_ = f.Set(db.Statement.Context, elem, val)
		}
	}
}

// ================== 全表保护 ==================

// hasBusinessWhere 判断是否存在业务层主动加入的 WHERE 条件（租户条件不算）。
func (p *tenantPlugin[T]) hasBusinessWhere(db *gorm.DB) bool {
	if db.Statement == nil {
		return false
	}
	whereClause, ok := db.Statement.Clauses["WHERE"]
	if !ok {
		return false
	}
	return whereClause.Expression != nil
}

// checkGlobalUpdate 禁止无业务条件的全表 Update。
func (p *tenantPlugin[T]) checkGlobalUpdate(db *gorm.DB) {
	if p.cfg.AllowGlobalUpdate {
		return
	}
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	ctx := resolveCtx(db.Statement.Context)
	if p.shouldSkip(ctx, db) || isAllowGlobalOperation(ctx) {
		return
	}
	if !p.hasBusinessWhere(db) {
		_ = db.AddError(fmt.Errorf(
			"tenant: 禁止无业务条件的全表 Update（表: %s），"+
				"如需执行请使用 plugin.AllowGlobalOperation(ctx) 临时放开，"+
				"或在 TenantConfig 中设置 AllowGlobalUpdate: true",
			p.tableName(db),
		))
	}
}

// checkGlobalDelete 禁止无业务条件的全表 Delete。
func (p *tenantPlugin[T]) checkGlobalDelete(db *gorm.DB) {
	if p.cfg.AllowGlobalDelete {
		return
	}
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	ctx := resolveCtx(db.Statement.Context)
	if p.shouldSkip(ctx, db) || isAllowGlobalOperation(ctx) {
		return
	}
	if !p.hasBusinessWhere(db) {
		_ = db.AddError(fmt.Errorf(
			"tenant: 禁止无业务条件的全表 Delete（表: %s），"+
				"如需执行请使用 plugin.AllowGlobalOperation(ctx) 临时放开，"+
				"或在 TenantConfig 中设置 AllowGlobalDelete: true",
			p.tableName(db),
		))
	}
}

// ================== 辅助方法 ==================

func (p *tenantPlugin[T]) fieldsFor(tableName string) ([]TenantFieldConfig[T], bool) {
	if len(p.tableFields) > 0 {
		if fields, ok := p.tableFields[tableName]; ok {
			return fields, true
		}
	}
	return p.defaultField, false
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
	name = strings.Trim(name, "`")
	if db.Statement.Schema != nil && db.Statement.Schema.Table != "" {
		return strings.ToLower(db.Statement.Schema.Table)
	}
	if idx := strings.IndexByte(name, ' '); idx >= 0 {
		name = name[:idx]
	}
	return strings.ToLower(strings.Trim(name, "`"))
}

func (p *tenantPlugin[T]) tableAlias(db *gorm.DB) string {
	if db.Statement == nil {
		return ""
	}
	name := strings.TrimSpace(strings.Trim(db.Statement.Table, "`"))
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	parts := strings.Fields(name)
	switch len(parts) {
	case 2:
		return parts[1]
	case 3:
		if strings.EqualFold(parts[1], "AS") {
			return parts[2]
		}
	}
	return ""
}

func (p *tenantPlugin[T]) fieldPrefix(db *gorm.DB) string {
	if alias := p.tableAlias(db); alias != "" {
		return alias
	}
	return ""
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

// resolveTenantID 获取最终租户 ID，处理覆盖逻辑。
// AllowOverrideTenantID=true 时优先使用 WithOverrideTenantID 写入的值。
func (p *tenantPlugin[T]) resolveTenantID(ctx context.Context, fieldGetter func(context.Context) (T, bool)) (T, bool) {
	if p.cfg.AllowOverrideTenantID {
		if overrideID, ok := overrideTenantIDFromCtx[T](ctx); ok && !isZero(overrideID) {
			return overrideID, true
		}
	}
	getter := fieldGetter
	if getter == nil {
		getter = p.cfg.GetTenantID
	}
	return getter(ctx)
}

func isZero[T comparable](v T) bool {
	return reflect.ValueOf(v).IsZero()
}

// ================== 构建插件 ==================

func buildPlugin[T comparable](cfg TenantConfig[T]) (*tenantPlugin[T], error) {
	if cfg.GetTenantID == nil {
		cfg.GetTenantID = DefaultGetTenantID[T]
	}

	var defaultFields []TenantFieldConfig[T]
	if len(cfg.TenantFields) > 0 {
		defaultFields = make([]TenantFieldConfig[T], len(cfg.TenantFields))
		copy(defaultFields, cfg.TenantFields)
	} else if cfg.TenantField != "" {
		defaultFields = []TenantFieldConfig[T]{{Field: cfg.TenantField}}
	} else {
		return nil, fmt.Errorf("RegisterTenant: TenantField 和 TenantFields 不能同时为空")
	}
	for i := range defaultFields {
		if defaultFields[i].GetTenantID == nil {
			defaultFields[i].GetTenantID = cfg.GetTenantID
		}
	}

	tableFields := make(map[string][]TenantFieldConfig[T], len(cfg.TableFields))
	for table, fields := range cfg.TableFields {
		copied := make([]TenantFieldConfig[T], len(fields))
		copy(copied, fields)
		for i := range copied {
			if copied[i].GetTenantID == nil {
				copied[i].GetTenantID = cfg.GetTenantID
			}
		}
		tableFields[strings.ToLower(table)] = copied
	}

	autoInjectJoin := true
	if cfg.AutoInjectJoinTables != nil {
		autoInjectJoin = *cfg.AutoInjectJoinTables
	}

	excludeJoinSet := make(map[string]struct{}, len(cfg.ExcludeJoinTables))
	for _, t := range cfg.ExcludeJoinTables {
		excludeJoinSet[strings.ToLower(t)] = struct{}{}
	}

	joinOverrideMap := make(map[string]JoinTenantConfig[T], len(cfg.JoinTableOverrides))
	for _, jt := range cfg.JoinTableOverrides {
		key := strings.ToLower(jt.Table)
		if jt.GetTenantID == nil {
			jt.GetTenantID = cfg.GetTenantID
		}
		joinOverrideMap[key] = jt
	}

	excludeSet := make(map[string]struct{}, len(cfg.ExcludeTables))
	for _, t := range cfg.ExcludeTables {
		excludeSet[strings.ToLower(t)] = struct{}{}
	}

	return &tenantPlugin[T]{
		cfg:             cfg,
		defaultField:    defaultFields,
		tableFields:     tableFields,
		autoInjectJoin:  autoInjectJoin,
		excludeJoinSet:  excludeJoinSet,
		joinOverrideMap: joinOverrideMap,
		excludeSet:      excludeSet,
	}, nil
}

// ================== 注册函数 ==================

// RegisterTenant 向指定 DB 注册多租户插件，整个应用只需调用一次。
//
// 用法一（单字段）：
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField:   "tenant_id",
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
// 用法二（多字段）：
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantFields: []plugin.TenantFieldConfig[int64]{
//	        {Field: "tenant_id"},
//	        {Field: "org_id", GetTenantID: orgIDGetter},
//	    },
//	})
//
// 用法三（不同表不同字段）：
//
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField: "tenant_id",
//	    TableFields: map[string][]plugin.TenantFieldConfig[int64]{
//	        "sys_contract": {{Field: "company_id"}},
//	        "sys_log":      {},
//	    },
//	})
func RegisterTenant[T comparable](db *gorm.DB, cfg TenantConfig[T]) error {
	p, err := buildPlugin(cfg)
	if err != nil {
		return err
	}
	return db.Use(p)
}

// NewTenantPlugin 工厂函数，返回插件实例供手动 db.Use() 注册。
func NewTenantPlugin[T comparable](cfg TenantConfig[T]) (gorm.Plugin, error) {
	return buildPlugin(cfg)
}

// ================== 动态排除表 ==================

func getPlugin[T comparable](db *gorm.DB) (*tenantPlugin[T], error) {
	name := fmt.Sprintf("gorm-plus:tenant:%T", *new(T))
	raw, ok := db.Config.Plugins[name]
	if !ok {
		return nil, fmt.Errorf("tenant: 插件 %q 未注册", name)
	}
	p, ok := raw.(*tenantPlugin[T])
	if !ok {
		return nil, fmt.Errorf("tenant: 插件 %q 类型断言失败", name)
	}
	return p, nil
}

// AddExcludeTable 运行时动态添加主表排除表（线程安全）。
//
//	plugin.AddExcludeTable[int64](db, "log_audit", "sys_trace")
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

// RemoveExcludeTable 运行时动态移除排除表（线程安全）。
//
//	plugin.RemoveExcludeTable[int64](db, "sys_dict")
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

// ExcludedTables 返回当前主表排除表快照（调试用）。
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

// ================== context 工具 ==================

type skipTenantKey struct{}
type allowGlobalKey struct{}
type tenantIDKey[T comparable] struct{}
type overrideTenantIDKey[T comparable] struct{}

// SkipTenant 跳过所有租户过滤（超管、跨租户统计专用）。
//
//	ctx = plugin.SkipTenant(ctx)
//	db.WithContext(ctx).Find(&all) // 无任何租户条件
func SkipTenant(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipTenantKey{}, true)
}

// AllowGlobalOperation 临时允许无业务条件的全表 Update / Delete。
//
//	ctx = plugin.AllowGlobalOperation(ctx)
//	db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})
func AllowGlobalOperation(ctx context.Context) context.Context {
	return context.WithValue(ctx, allowGlobalKey{}, true)
}

func isAllowGlobalOperation(ctx context.Context) bool {
	ok, _ := ctx.Value(allowGlobalKey{}).(bool)
	return ok
}

// WithTenantID 将租户 ID 写入 context（通常在中间件中调用）。
//
//	// gin 中间件
//	ctx := plugin.WithTenantID(c.Request.Context(), int64(1001))
//	c.Request = c.Request.WithContext(ctx)
//
//	// go-zero 中间件
//	ctx := plugin.WithTenantID(r.Context(), int64(1001))
//	r = r.WithContext(ctx)
func WithTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return context.WithValue(ctx, tenantIDKey[T]{}, tenantID)
}

// TenantIDFromCtx 从 context 读取租户 ID（类型参数须与写入时一致）。
func TenantIDFromCtx[T comparable](ctx context.Context) T {
	var zero T
	ctx = resolveCtx(ctx)
	if v := ctx.Value(tenantIDKey[T]{}); v != nil {
		if tid, ok := v.(T); ok {
			return tid
		}
	}
	return zero
}

// DefaultGetTenantID 默认取值函数，读取 WithTenantID 写入的值。
// 返回零值时 ok=false，插件跳过注入。
func DefaultGetTenantID[T comparable](ctx context.Context) (T, bool) {
	ctx = resolveCtx(ctx)
	tid := TenantIDFromCtx[T](ctx)
	if reflect.ValueOf(tid).IsZero() {
		var zero T
		return zero, false
	}
	return tid, true
}

// WithOverrideTenantID 临时覆盖租户 ID（需 AllowOverrideTenantID=true 才生效）。
//
// 适合超管管理后台查看指定租户数据、数据迁移等场景。
//
//	// 注册时开启
//	plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
//	    TenantField:           "tenant_id",
//	    AllowOverrideTenantID: true,
//	})
//
//	// 超管查看租户 2002 的数据（中间件注入的是 1001）
//	ctx = plugin.WithOverrideTenantID(ctx, int64(2002))
//	db.WithContext(ctx).Find(&list) // WHERE tenant_id = 2002
func WithOverrideTenantID[T comparable](ctx context.Context, tenantID T) context.Context {
	return context.WithValue(ctx, overrideTenantIDKey[T]{}, tenantID)
}

func overrideTenantIDFromCtx[T comparable](ctx context.Context) (T, bool) {
	var zero T
	v := ctx.Value(overrideTenantIDKey[T]{})
	if v == nil {
		return zero, false
	}
	id, ok := v.(T)
	return id, ok
}
