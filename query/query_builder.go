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

	"gorm.io/gorm"
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
	db      *gorm.DB
	clauses []*clause
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

func (b *Builder) BetweenIfNotZero(col string, min, max any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(min) && !isZeroVal(max), fmt.Sprintf("`%s` BETWEEN ? AND ?", col), min, max)
}

func (b *Builder) WhereIf(condition bool, query string, args ...any) IQueryBuilder {
	if !condition {
		return b
	}
	return b.add(&clause{sql: query, args: args, typ: clauseAnd})
}

func (b *Builder) WhereGroup(fn func(IQueryBuilder)) IQueryBuilder {
	child := &Builder{db: b.db}
	fn(child)
	if len(child.clauses) == 0 {
		return b
	}
	return b.add(&clause{children: child.clauses, typ: clauseAnd, isGroup: true})
}

func (b *Builder) OrGroup(fn func(IQueryBuilder)) IQueryBuilder {
	child := &Builder{db: b.db}
	fn(child)
	if len(child.clauses) == 0 {
		return b
	}
	return b.add(&clause{children: child.clauses, typ: clauseOr, isGroup: true})
}

func (b *Builder) Build() *gorm.DB {
	db := b.db
	for _, c := range b.clauses {
		db = applyClause(db, c)
	}
	return db
}

// ================== SQL 构建（内部）==================

func applyClause(db *gorm.DB, c *clause) *gorm.DB {
	if c.isGroup {
		scope := func(tx *gorm.DB) *gorm.DB {
			for _, child := range c.children {
				tx = applyClause(tx, child)
			}
			return tx
		}
		if c.typ == clauseOr {
			return db.Or(scope)
		}
		return db.Where(scope)
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
