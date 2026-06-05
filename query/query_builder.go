// Package db 提供基于原生 gorm 的链式查询条件构建工具。
//
// # 快速上手
//
//	built := query.NewQuery[model.Account](gormDB, ctx).
//	    LLike("username", username).
//	    WhereIf(status != 0, "status = ?", status).
//	    BetweenIfNotZero("created_at", start, end).
//	    WhereIf(len(deptIDs) > 0, "dept_id IN ?", deptIDs).
//	    Build()
//
//	// 查列表
//	built.Order("created_at DESC").Limit(pageSize).Offset((page-1)*pageSize).Find(&list)
//	// 查总数
//	built.Count(&total)
//	// 联表查询
//	built.Select("a.id", "d.name AS dept_name").
//	    Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
//	    Scan(&voList)
//
// # 条件分组
//
//	// AND 分组：WHERE (username LIKE '%kw%' OR email LIKE '%kw%')
//	query.NewQuery[model.Account](gormDB, ctx).
//	    WhereGroup(func(q query.IQueryBuilder) {
//	        q.Like("username", keyword).
//	          WhereIf(true, "email LIKE ?", "%"+keyword+"%")
//	    }).Build().Find(&list)
//
//	// OR 分组：WHERE status = 1 OR (role = 99 AND org_id = 10)
//	query.NewQuery[model.Account](gormDB, ctx).
//	    WhereIf(true, "status = ?", 1).
//	    OrGroup(func(q query.IQueryBuilder) {
//	        q.WhereIf(role != 0, "role = ?", role).
//	          WhereIf(orgID != 0, "org_id = ?", orgID)
//	    }).Build().Find(&list)
package query

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	gormclause "gorm.io/gorm/clause"
)

// NewQuery 创建扩展条件构造器。
// db 由调用方传入，支持多数据源、多租户、测试替换等场景；ctx 用于链路追踪、超时控制等。
//
//	// 常规用法
//	query.NewQuery[model.Account](gormDB, ctx).
//	    LLike("username", username).
//	    WhereIf(status != 0, "status = ?", status).
//	    Build().
//	    Order("created_at DESC").Limit(20).Find(&list)
//
//	// 多数据源场景
//	query.NewQuery[model.Account](tenantDB, ctx).
//	    WhereIf(status != 0, "status = ?", status).
//	    Build().Find(&list)
func NewQuery[T any](db *gorm.DB, ctx context.Context) IQueryBuilder {
	return &Builder{
		db: db.WithContext(ctx).Model(new(T)),
	}
}

// IQueryBuilder 原生 gorm 扩展条件构造器。
// 链式拼装扩展条件后调用 Build() 返回原生 *gorm.DB，继续使用所有 gorm 原生方法。
type IQueryBuilder interface {
	// -------- 模糊查询（值为空字符串时自动跳过） --------

	// Like 双侧模糊：WHERE col LIKE '%val%'，val 为空时跳过。
	//
	//   .Like("username", "admin") => WHERE username LIKE '%admin%'
	Like(col string, val string) IQueryBuilder

	// LLike 左侧模糊：WHERE col LIKE '%val'，val 为空时跳过。
	//
	//   .LLike("username", "admin") => WHERE username LIKE '%admin'
	LLike(col string, val string) IQueryBuilder

	// RLike 右侧模糊：WHERE col LIKE 'val%'，val 为空时跳过。
	// 可利用前缀索引，性能优于全模糊。
	//
	//   .RLike("order_no", "ORD2024") => WHERE order_no LIKE 'ORD2024%'
	RLike(col string, val string) IQueryBuilder

	// OrLike 双侧模糊，以 OR 追加；val 为空时跳过。
	OrLike(col string, val string) IQueryBuilder

	// OrLLike 左侧模糊，以 OR 追加；val 为空时跳过。
	OrLLike(col string, val string) IQueryBuilder

	// OrRLike 右侧模糊，以 OR 追加；val 为空时跳过。
	OrRLike(col string, val string) IQueryBuilder

	// -------- 范围查询 --------

	// BetweenIfNotZero 闭区间 [min, max]，min 和 max 同时非零才生效。
	// 适合时间区间、金额区间等前端可选筛选场景。
	//
	//   .BetweenIfNotZero("created_at", startTime, endTime)
	//   => WHERE created_at BETWEEN '2024-01-01' AND '2024-12-31'
	//   （任一为零值时跳过）
	BetweenIfNotZero(col string, min, max any) IQueryBuilder

	// -------- 条件开关 --------

	// WhereIf condition 为 true 时追加 AND 条件，支持 ? 占位参数，false 时整体跳过。
	// 是替代手动 if 判断的核心方法，适合所有可选筛选条件。
	//
	//   .WhereIf(status != 0, "status = ?", status)
	//   .WhereIf(len(ids) > 0, "id IN ?", ids)
	//   .WhereIf(minScore > 0, "score >= ?", minScore)
	//   .WhereIf(onlyActive, "deleted_at IS NULL")
	//   .WhereIf(onlyVip, "id IN (SELECT account_id FROM vip WHERE level > ?)", 2)
	WhereIf(condition bool, query string, args ...any) IQueryBuilder

	// OrWhereIf condition 为 true 时追加 OR 条件，false 时整体跳过。
	OrWhereIf(condition bool, query string, args ...any) IQueryBuilder

	// WithDeleted 查询时包含主表逻辑删除数据。
	WithDeleted() IQueryBuilder

	// WithUnscoped 是 WithDeleted 的 GORM 语义别名。
	WithUnscoped() IQueryBuilder

	// WhereNotDeleted 追加指定表/别名的未删除条件，适合手写 JOIN 的从表过滤。
	WhereNotDeleted(tableOrAlias string) IQueryBuilder

	// WhereDeleted 追加指定表/别名的已删除条件，适合查询 JOIN 从表的逻辑删除数据。
	WhereDeleted(tableOrAlias string) IQueryBuilder

	// Clauses 追加 GORM clause。
	Clauses(clauses ...gormclause.Expression) IQueryBuilder

	// -------- 条件分组（保证括号语义正确） --------

	// WhereGroup 将 fn 内的条件用括号包裹后以 AND 连接到主查询。
	// 组内可使用完整 IQueryBuilder 能力：WhereIf / Like / LLike 等。
	//
	//   // WHERE org_id = 1 AND (username LIKE '%admin' AND status = 1)
	//   .WhereIf(orgID != 0, "org_id = ?", orgID).
	//    WhereGroup(func(q db.IQueryBuilder) {
	//        q.LLike("username", username).
	//          WhereIf(status != 0, "status = ?", status)
	//    })
	WhereGroup(fn func(IQueryBuilder)) IQueryBuilder

	// WhereGroupIf condition 为 true 时追加 AND 函数分组，否则整体跳过。
	WhereGroupIf(condition bool, fn func(IQueryBuilder)) IQueryBuilder

	// WhereOrGroup 将 fn 内条件用括号包裹后以 AND 连接到主查询，组内默认以 OR 连接。
	// 适合“多个可选搜索字段命中任一即可”的场景。
	//
	//   // WHERE status = 1 AND (username LIKE '%kw%' OR mobile LIKE '%kw%')
	//   .WhereIf(true, "status = ?", 1).
	//    WhereOrGroup(func(q db.IQueryBuilder) {
	//        q.Like("username", keyword).
	//          Like("mobile", keyword)
	//    })
	WhereOrGroup(fn func(IQueryBuilder)) IQueryBuilder

	// WhereOrGroupIf condition 为 true 时追加 AND 接入的 OR 分组，否则整体跳过。
	WhereOrGroupIf(condition bool, fn func(IQueryBuilder)) IQueryBuilder

	// OrGroup 将 fn 内的条件用括号包裹后以 OR 连接到主查询。
	// 组内可使用完整 IQueryBuilder 能力。
	//
	//   // WHERE status = 1 OR (role = 99 AND org_id = 10)
	//   .WhereIf(true, "status = ?", 1).
	//    OrGroup(func(q db.IQueryBuilder) {
	//        q.WhereIf(role != 0, "role = ?", role).
	//          WhereIf(orgID != 0, "org_id = ?", orgID)
	//    })
	OrGroup(fn func(IQueryBuilder)) IQueryBuilder

	// OrGroupIf condition 为 true 时追加 OR 函数分组，否则整体跳过。
	OrGroupIf(condition bool, fn func(IQueryBuilder)) IQueryBuilder

	// -------- 出口 --------

	// Build 结束扩展条件构建，返回已注入所有条件的原生 *gorm.DB。
	// 之后可继续调用所有 gorm 原生方法：Select / Joins / Order / Limit / Find / Count 等。
	//
	//   built := query.NewQuery[model.Account](gormDB, ctx).
	//       LLike("username", username).
	//       WhereIf(status != 0, "status = ?", status).
	//       Build()
	//   built.Count(&total)
	//   built.Select("id", "username").Order("created_at DESC").Limit(20).Find(&list)
	Build() *gorm.DB

	// -------- 调试 --------

	// PrintSQL 立即把当前 builder 的最终 SQL(含填好的参数值)打印到 stdout,
	// 并继续返回 builder 用于链式调用,不影响实际查询执行。
	//
	// 不需要任何 logger 配置,纯粹排查用。
	//
	//   query.Query[*User](db, ctx).
	//       LLike("username", "admin").
	//       PrintSQL().                     // ← 打印 SELECT * FROM users WHERE username LIKE '%admin' ...
	//       Build().Find(&users)
	PrintSQL() IQueryBuilder

	// ToSQL 返回当前查询的最终 SQL 字符串(含填好的参数值),不真正执行 SQL。
	//
	// 内部用 gorm 的 DryRun Session 渲染,常用于单测断言、日志记录、调试展示。
	//
	// ⚠️ 安全提示:返回的 SQL 已经把参数填进了占位符,不再提供 SQL 注入保护。
	// 仅用于调试,不要把它喂回 db.Exec / db.Raw 执行。
	//
	//   sql := query.Query[*User](db, ctx).
	//       LLike("username", username).
	//       ToSQL()
	//   // sql == "SELECT * FROM users WHERE username LIKE '%admin'"
	ToSQL() string

	// Explain 执行 EXPLAIN <SQL> 拿到数据库的执行计划,扫描到 target。
	//
	// target 通常是 []map[string]any 或自定义 struct slice,字段名匹配 EXPLAIN 输出列。
	// 不同数据库的 EXPLAIN 输出格式不同(MySQL 字段:id/select_type/table/type/key/rows 等),
	// 业务方根据所用数据库选合适的接收类型。
	//
	//   var plan []map[string]any
	//   err := query.Query[*User](db, ctx).
	//       Where("status = ?", 1).
	//       Explain(&plan)
	//   for _, row := range plan {
	//       fmt.Printf("%+v\n", row)
	//   }
	Explain(target any) error
}

// ================== 内部条件节点 ==================

type clause struct {
	sql      string
	args     []any
	children []*clause
	typ      clauseType
	isGroup  bool
}

type clauseType int

const (
	clauseAnd clauseType = iota
	clauseOr
)

// ================== Builder 实现 ==================

type Builder struct {
	db         *gorm.DB
	clauses    []*clause
	defaultTyp clauseType
}

func (b *Builder) add(c *clause) IQueryBuilder {
	b.clauses = append(b.clauses, c)
	return b
}

func (b *Builder) Like(col string, val string) IQueryBuilder {
	return b.WhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), "%"+val+"%")
}
func (b *Builder) LLike(col string, val string) IQueryBuilder {
	return b.WhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), "%"+val)
}
func (b *Builder) RLike(col string, val string) IQueryBuilder {
	return b.WhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), val+"%")
}
func (b *Builder) OrLike(col string, val string) IQueryBuilder {
	return b.OrWhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), "%"+val+"%")
}
func (b *Builder) OrLLike(col string, val string) IQueryBuilder {
	return b.OrWhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), "%"+val)
}
func (b *Builder) OrRLike(col string, val string) IQueryBuilder {
	return b.OrWhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), val+"%")
}

func (b *Builder) BetweenIfNotZero(col string, min, max any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(min) && !isZeroVal(max), fmt.Sprintf("`%s` BETWEEN ? AND ?", col), min, max)
}

func (b *Builder) WhereIf(condition bool, query string, args ...any) IQueryBuilder {
	if !condition {
		return b
	}
	return b.add(&clause{sql: query, args: args, typ: b.defaultClauseType()})
}

func (b *Builder) OrWhereIf(condition bool, query string, args ...any) IQueryBuilder {
	if !condition {
		return b
	}
	return b.add(&clause{sql: query, args: args, typ: clauseOr})
}

func (b *Builder) WithDeleted() IQueryBuilder {
	b.db = b.db.Unscoped()
	return b
}

func (b *Builder) WithUnscoped() IQueryBuilder {
	return b.WithDeleted()
}

func (b *Builder) WhereNotDeleted(tableOrAlias string) IQueryBuilder {
	tableOrAlias = strings.TrimSpace(tableOrAlias)
	if tableOrAlias == "" {
		return b
	}
	return b.WhereIf(true, fmt.Sprintf("%s.deleted_at IS NULL", tableOrAlias))
}

func (b *Builder) WhereDeleted(tableOrAlias string) IQueryBuilder {
	tableOrAlias = strings.TrimSpace(tableOrAlias)
	if tableOrAlias == "" {
		return b
	}
	return b.WhereIf(true, fmt.Sprintf("%s.deleted_at IS NOT NULL", tableOrAlias))
}

func (b *Builder) Clauses(clauses ...gormclause.Expression) IQueryBuilder {
	if len(clauses) > 0 {
		b.db = b.db.Clauses(clauses...)
	}
	return b
}

func (b *Builder) defaultClauseType() clauseType {
	if b.defaultTyp == clauseOr {
		return clauseOr
	}
	return clauseAnd
}

func (b *Builder) addGroup(fn func(IQueryBuilder), outerTyp, innerTyp clauseType) IQueryBuilder {
	if fn == nil {
		return b
	}
	child := &Builder{db: b.db, defaultTyp: innerTyp}
	fn(child)
	if len(child.clauses) == 0 {
		return b
	}
	return b.add(&clause{children: child.clauses, typ: outerTyp, isGroup: true})
}

func (b *Builder) WhereGroup(fn func(IQueryBuilder)) IQueryBuilder {
	return b.addGroup(fn, clauseAnd, clauseAnd)
}

func (b *Builder) WhereGroupIf(condition bool, fn func(IQueryBuilder)) IQueryBuilder {
	if condition {
		return b.WhereGroup(fn)
	}
	return b
}

func (b *Builder) WhereOrGroup(fn func(IQueryBuilder)) IQueryBuilder {
	return b.addGroup(fn, clauseAnd, clauseOr)
}

func (b *Builder) WhereOrGroupIf(condition bool, fn func(IQueryBuilder)) IQueryBuilder {
	if condition {
		return b.WhereOrGroup(fn)
	}
	return b
}

func (b *Builder) OrGroup(fn func(IQueryBuilder)) IQueryBuilder {
	return b.addGroup(fn, clauseOr, clauseAnd)
}

func (b *Builder) OrGroupIf(condition bool, fn func(IQueryBuilder)) IQueryBuilder {
	if condition {
		return b.OrGroup(fn)
	}
	return b
}

func (b *Builder) Build() *gorm.DB {
	db := b.db
	for _, c := range b.clauses {
		db = applyClause(db, c)
	}
	return db
}

// ──── 调试方法 ──────────────────────────────────────────────────────────────

func (b *Builder) PrintSQL() IQueryBuilder {
	fmt.Println(b.ToSQL())
	return b
}

func (b *Builder) ToSQL() string {
	return b.db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		built := tx
		for _, c := range b.clauses {
			built = applyClause(built, c)
		}
		// 必须调用一个终结方法(Find/First/Count 等),否则 gorm 不会生成完整 SQL。
		// 这里用通用的 Find 触发 SELECT;调用方真实意图(Update/Delete/Insert)
		// 应该用 ToSQL 之外的工具,本方法仅服务于查询场景的调试。
		return built.Find(&struct{}{})
	})
}

func (b *Builder) Explain(target any) error {
	sql := b.ToSQL()
	if sql == "" {
		return fmt.Errorf("ToSQL returned empty,无法 EXPLAIN")
	}
	// gorm 的 ToSQL 输出末尾不带分号,直接前缀 EXPLAIN 即可
	return b.db.Raw("EXPLAIN " + sql).Scan(target).Error
}

// ================== SQL 构建（内部）==================

func applyClause(db *gorm.DB, c *clause) *gorm.DB {
	if c.isGroup {
		subDB := db.Session(&gorm.Session{NewDB: true})
		for _, child := range c.children {
			subDB = applyClause(subDB, child)
		}
		if c.typ == clauseOr {
			return db.Or(subDB)
		}
		return db.Where(subDB)
	}
	if c.typ == clauseOr {
		return db.Or(c.sql, c.args...)
	}
	return db.Where(c.sql, c.args...)
}

// ================== 泛型分页函数 ==================

// FindByPage 泛型分页查询，返回 (数据列表, 总数, error)。
// 适合结果直接映射到 model struct 的简单列表查询。
//
//	list, total, err := query.FindByPage[model.Account](
//	    query.NewQuery[model.Account](gormDB, ctx).
//	        LLike("username", username).
//	        WhereIf(status != 0, "status = ?", status).
//	        Build().
//	        Order("created_at DESC"),
//	    pageNum, pageSize,
//	)
func FindByPage[T any](q *gorm.DB, pageNum, pageSize int) ([]T, int64, error) {
	if pageNum < 1 {
		pageNum = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []T
	err := q.Limit(pageSize).Offset((pageNum - 1) * pageSize).Find(&list).Error
	return list, total, err
}

// ScanByPage 泛型分页扫描，返回 (数据列表, 总数, error)。
// 与 FindByPage 的区别：使用 Scan，适合联表查询、自定义 SELECT 字段映射到 VO 的场景。
//
//	type AccountVO struct {
//	    ID       int64  `json:"id"`
//	    Username string `json:"username"`
//	    DeptName string `json:"deptName"` // 来自 join
//	}
//
//	list, total, err := query.ScanByPage[AccountVO](
//	    query.NewQuery[model.Account](gormDB, ctx).
//	        LLike("a.username", username).
//	        WhereIf(status != 0, "a.status = ?", status).
//	        Build().
//	        Select("a.id", "a.username", "d.name AS dept_name").
//	        Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
//	        Order("a.created_at DESC"),
//	    pageNum, pageSize,
//	)
func ScanByPage[T any](q *gorm.DB, pageNum, pageSize int) ([]T, int64, error) {
	if pageNum < 1 {
		pageNum = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []T
	err := q.Limit(pageSize).Offset((pageNum - 1) * pageSize).Scan(&list).Error
	return list, total, err
}
