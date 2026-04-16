package query

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ================== IQueryBuilder 接口（原生 gorm） ==================
//
// 链式条件构造器，类似 MyBatis-Plus QueryWrapper，基于字符串列名。
// gorm-gen 场景请使用 GenWrap + IGenWrapper。
//
// 三种条件控制语义：
//   - Eq/Ne/Gt...         无条件生效，值为 0/"" 也拼入 SQL
//   - EqIfNotZero...      零值（0、""、false、nil）时自动跳过
//   - EqIfNotNil...       仅 nil 时跳过，适合指针可选参数（ptr(0) 也生效）
//
// ⚠️ 注意事项：
//   - OR 条件强制通过 OrGroup 使用，禁止裸 OrEq/OrLike 散落在链式调用中
//   - WhereGroup/OrGroup 会产生括号，语义清晰，不会与外层条件产生优先级歧义
//   - Raw SQL 会自动包一层括号，防止运算符优先级问题
//   - 子查询参数传 *gorm.DB，由调用方通过 db.Model(...).Select(...).Where(...) 构造
//
// 快速使用：
//
//	var list []*model.Order
//	total, err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	    EqIfNotZero("user_id", userId).
//	    EqIfNotNil("status", statusPtr).
//	    LikeIfNotEmpty("title", keyword).
//	    BetweenIfNotZero("created_at", startTime, endTime).
//	    OrderByDesc("created_at").
//	    Page(pageNum, pageSize).
//	    FindAndCount(&list)
type IQueryBuilder interface {
	// -------- SELECT 指定字段 --------
	//
	// 支持字符串、gorm-gen field.Expr、CaseWhenBuilder 混用。
	// 示例：
	//   .Select("id", "name")
	//   .Select(dao.Order.ID, dao.Order.Amount)
	//   .Select("id", query.NewCase().When("status=1","'是'").Else("'否'").As("flag"))
	Select(cols ...any) IQueryBuilder

	// -------- 等值条件 --------

	// Eq 精确匹配，零值也生效。
	// 示例：.Eq("status", 0) → WHERE status = 0
	Eq(col string, val any) IQueryBuilder

	// EqIfNotZero 精确匹配，val 为零值时跳过。
	// 示例：
	//   .EqIfNotZero("user_id", 0)   → 跳过
	//   .EqIfNotZero("user_id", 123) → WHERE user_id = 123
	EqIfNotZero(col string, val any) IQueryBuilder

	// EqIfNotNil 精确匹配，val 为 nil 时跳过，ptr(0) 也生效。
	// 示例：
	//   .EqIfNotNil("status", nil)    → 跳过
	//   .EqIfNotNil("status", ptr(0)) → WHERE status = 0
	EqIfNotNil(col string, val any) IQueryBuilder

	// Ne 不等于，零值也生效。
	Ne(col string, val any) IQueryBuilder

	// NeIfNotZero 不等于，val 为零值时跳过。
	NeIfNotZero(col string, val any) IQueryBuilder

	// -------- 模糊匹配 --------

	// Like 全模糊 %val%，val 为空也生效。
	Like(col string, val string) IQueryBuilder

	// LikeIfNotEmpty 全模糊 %val%，val 为空时跳过。
	LikeIfNotEmpty(col string, val string) IQueryBuilder

	// LikeLeft 左模糊 %val。
	LikeLeft(col string, val string) IQueryBuilder

	// LikeLeftIfNotEmpty 左模糊，val 为空时跳过。
	LikeLeftIfNotEmpty(col string, val string) IQueryBuilder

	// LikeRight 右模糊 val%，可走前缀索引。
	LikeRight(col string, val string) IQueryBuilder

	// LikeRightIfNotEmpty 右模糊，val 为空时跳过。
	LikeRightIfNotEmpty(col string, val string) IQueryBuilder

	// NotLike NOT LIKE %val%。
	NotLike(col string, val string) IQueryBuilder

	// -------- 大小比较 --------

	// Gt 大于，零值也生效。
	Gt(col string, val any) IQueryBuilder

	// GtIfNotZero 大于，val 为零值时跳过。
	GtIfNotZero(col string, val any) IQueryBuilder

	// Gte 大于等于，零值也生效。
	Gte(col string, val any) IQueryBuilder

	// GteIfNotZero 大于等于，val 为零值时跳过。
	GteIfNotZero(col string, val any) IQueryBuilder

	// Lt 小于，零值也生效。
	Lt(col string, val any) IQueryBuilder

	// LtIfNotZero 小于，val 为零值时跳过。
	LtIfNotZero(col string, val any) IQueryBuilder

	// Lte 小于等于，零值也生效。
	Lte(col string, val any) IQueryBuilder

	// LteIfNotZero 小于等于，val 为零值时跳过。
	LteIfNotZero(col string, val any) IQueryBuilder

	// -------- 区间条件 --------

	// Between 闭区间，零值也生效。
	Between(col string, min, max any) IQueryBuilder

	// BetweenIfNotZero 闭区间，min 和 max 同时非零才生效。
	BetweenIfNotZero(col string, min, max any) IQueryBuilder

	// NotBetween NOT BETWEEN。
	NotBetween(col string, min, max any) IQueryBuilder

	// -------- IN 条件 --------

	// In IN 查询，vals 传 slice。
	In(col string, vals any) IQueryBuilder

	// InIfNotEmpty IN 查询，vals 为 nil 或空 slice 时跳过。
	InIfNotEmpty(col string, vals any) IQueryBuilder

	// NotIn NOT IN 查询。
	NotIn(col string, vals any) IQueryBuilder

	// NotInIfNotEmpty NOT IN 查询，vals 为空时跳过。
	NotInIfNotEmpty(col string, vals any) IQueryBuilder

	// -------- NULL 判断 --------

	// IsNull 判断字段为 NULL。
	IsNull(col string) IQueryBuilder

	// IsNotNull 判断字段不为 NULL。
	IsNotNull(col string) IQueryBuilder

	// -------- 核心能力 --------

	// WhereIf 条件为 true 时添加 WHERE 条件，支持 ? 占位参数。
	// 示例：
	//   .WhereIf(true, "status = ?", 1) → WHERE status = 1
	WhereIf(condition bool, query string, args ...any) IQueryBuilder

	// -------- 分组括号 --------
	//
	// ⚠️ OR 条件强制通过 OrGroup 使用，禁止裸 OR 散落在链式调用中。
	// 这样可以保证生成的 SQL 括号语义清晰，不会因为优先级产生歧义。

	// WhereGroup 创建一个 AND 组合条件块（括号内 AND 连接）。
	// 示例：
	//   .WhereGroup(func(q IQueryBuilder) {
	//       q.Eq("type", 1).OrGroup(func(q IQueryBuilder) {
	//           q.Eq("type", 2).Eq("source", "web")
	//       })
	//   })
	//   → WHERE (type = 1 OR (type = 2 AND source = 'web'))
	WhereGroup(fn func(IQueryBuilder)) IQueryBuilder

	// OrGroup 创建一个 OR 组合条件块（括号内 AND 连接，整体以 OR 接入父层）。
	// 示例：
	//   .Eq("status", 1).
	//    OrGroup(func(q IQueryBuilder) {
	//        q.Eq("type", 2).Eq("source", "app")
	//    })
	//   → WHERE status = 1 OR (type = 2 AND source = 'app')
	OrGroup(fn func(IQueryBuilder)) IQueryBuilder

	// -------- 子查询 --------
	//
	// 参数使用 *gorm.DB，由调用方构造好后传入。
	// 构造方式：db.Model(&Xxx{}).Select("col").Where(...)
	//
	// 示例：
	//   subDB := db.Model(&Dept{}).Select("id").Where("status = ?", 1)
	//
	//   // IN：WHERE dept_id IN (SELECT id FROM dept WHERE status = 1)
	//   .SubQueryIn("dept_id", subDB)
	//
	//   // NOT IN：WHERE dept_id NOT IN (SELECT id FROM dept WHERE ...)
	//   .SubQueryNotIn("dept_id", subDB)
	//
	//   // EXISTS：WHERE EXISTS (SELECT id FROM orders WHERE ...)
	//   .SubQueryExists(subDB)
	//
	//   // NOT EXISTS：WHERE NOT EXISTS (...)
	//   .SubQueryNotExists(subDB)
	//
	//   // 标量比较：WHERE score = (SELECT MAX(score) FROM exam WHERE ...)
	//   .SubQueryEq("score", subDB)
	//   .SubQueryGt("score", subDB)
	//
	//   // OR 版本：
	//   .OrSubQueryIn("dept_id", subDB)

	// SubQueryIn WHERE col IN (SELECT ...)
	SubQueryIn(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryNotIn WHERE col NOT IN (SELECT ...)
	SubQueryNotIn(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryExists WHERE EXISTS (SELECT ...)
	SubQueryExists(sub *gorm.DB) IQueryBuilder

	// SubQueryNotExists WHERE NOT EXISTS (SELECT ...)
	SubQueryNotExists(sub *gorm.DB) IQueryBuilder

	// SubQueryEq WHERE col = (SELECT 单值)
	SubQueryEq(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryNe WHERE col != (SELECT 单值)
	SubQueryNe(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryGt WHERE col > (SELECT 单值)
	SubQueryGt(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryGte WHERE col >= (SELECT 单值)
	SubQueryGte(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryLt WHERE col < (SELECT 单值)
	SubQueryLt(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryLte WHERE col <= (SELECT 单值)
	SubQueryLte(col string, sub *gorm.DB) IQueryBuilder

	// OrSubQueryIn OR col IN (SELECT ...)
	OrSubQueryIn(col string, sub *gorm.DB) IQueryBuilder

	// OrSubQueryNotIn OR col NOT IN (SELECT ...)
	OrSubQueryNotIn(col string, sub *gorm.DB) IQueryBuilder

	// OrSubQueryExists OR EXISTS (SELECT ...)
	OrSubQueryExists(sub *gorm.DB) IQueryBuilder

	// OrSubQueryNotExists OR NOT EXISTS (SELECT ...)
	OrSubQueryNotExists(sub *gorm.DB) IQueryBuilder

	// -------- 原生 SQL --------
	//
	// ⚠️ Raw SQL 会自动包一层括号，防止运算符优先级歧义。
	// 示例（自动括号效果）：
	//   .RawWhere("a = 1 OR b = 2") → WHERE (a = 1 OR b = 2)

	// RawWhere 无条件加入原生 AND WHERE。
	// 示例：
	//   .RawWhere("DATE(created_at) = ?", "2024-01-01")
	//   .RawWhere("JSON_CONTAINS(tags, ?)", `["go","gorm"]`)
	RawWhere(sql string, args ...any) IQueryBuilder

	// RawOrWhere 无条件加入原生 OR WHERE（自动加括号）。
	// 示例：
	//   .Eq("status", 1).RawOrWhere("amount > ?", 1000)
	//   → WHERE status = 1 OR (amount > 1000)
	RawOrWhere(sql string, args ...any) IQueryBuilder

	// RawWhereIf 条件成立时加入原生 AND WHERE。
	// 示例：
	//   .RawWhereIf(keyword != "", "MATCH(title,body) AGAINST(?)", keyword)
	RawWhereIf(condition bool, sql string, args ...any) IQueryBuilder

	// -------- JOIN 连表 --------

	// Join INNER JOIN，自动检测是否已有 JOIN 前缀，支持 ? 占位参数。
	Join(sql string, args ...any) IQueryBuilder

	// InnerJoin INNER JOIN 显式写法，与 Join 等价。
	InnerJoin(sql string, args ...any) IQueryBuilder

	// LeftJoin LEFT JOIN，保留左表全部行。
	LeftJoin(sql string, args ...any) IQueryBuilder

	// RightJoin RIGHT JOIN，保留右表全部行。
	RightJoin(sql string, args ...any) IQueryBuilder

	// -------- 排序 --------

	// OrderByAsc 升序排列。
	OrderByAsc(col string) IQueryBuilder

	// OrderByDesc 降序排列。
	OrderByDesc(col string) IQueryBuilder

	// OrderByRaw 原始排序表达式，用于复杂场景。
	// 示例：
	//   .OrderByRaw("FIELD(status, 1, 2, 3)")
	//   .OrderByRaw("score DESC, created_at ASC")
	OrderByRaw(expr string) IQueryBuilder

	// -------- 分组 & HAVING --------

	// GroupBy GROUP BY 分组，可传多个字段。
	GroupBy(cols ...string) IQueryBuilder

	// Having HAVING 条件，支持 ? 占位参数。
	Having(sql string, vals ...any) IQueryBuilder

	// -------- 分页 & 限制 --------

	// Page 分页，pageNum 从 1 开始，自动计算 LIMIT 和 OFFSET。
	// 示例：.Page(2, 20) → LIMIT 20 OFFSET 20
	Page(pageNum, pageSize int) IQueryBuilder

	// Limit 直接设置最大返回条数。
	Limit(n int) IQueryBuilder

	// Offset 直接设置跳过条数。
	Offset(n int) IQueryBuilder

	// -------- 执行方法 --------

	// Find 查询列表，dest 传指向 slice 的指针。
	Find(dest any) error

	// First 查询第一条（按主键升序），不存在返回 gorm.ErrRecordNotFound。
	First(dest any) error

	// Last 查询最后一条（按主键降序），不存在返回 gorm.ErrRecordNotFound。
	Last(dest any) error

	// Take 查询一条（不指定排序），不存在返回 gorm.ErrRecordNotFound。
	Take(dest any) error

	// Count 统计数量，不带 LIMIT/OFFSET。
	Count() (int64, error)

	// FindAndCount 同时查询列表和总数，内部执行两次查询。
	FindAndCount(dest any) (int64, error)

	// Pluck 查询单列值到 slice。
	// 示例：
	//   var ids []int64
	//   err := q.EqIfNotZero("status", 1).Pluck("id", &ids)
	Pluck(col string, dest any) error

	// Scan 扫描到任意结构体，适合自定义 SELECT 字段的场景。
	Scan(dest any) error

	// Exists 判断是否存在满足条件的记录。
	Exists() (bool, error)

	// -------- 聚合函数 --------

	// Sum 对指定字段求和，无匹配行时 Valid=false。
	Sum(col string) (AggResult, error)

	// Avg 对指定字段求平均值，无匹配行时 Valid=false。
	Avg(col string) (AggResult, error)

	// Max 对指定字段求最大值，无匹配行时 Valid=false。
	Max(col string) (AggResult, error)

	// Min 对指定字段求最小值，无匹配行时 Valid=false。
	Min(col string) (AggResult, error)

	// CountDistinct 统计指定字段不重复值的数量。
	CountDistinct(col string) (int64, error)

	// SumGroup 按分组字段聚合求和，结果扫描到 dest slice。
	SumGroup(col, alias string, dest any, groupByCols ...string) error

	// AggGroup 通用分组聚合，支持自定义多个聚合表达式。
	AggGroup(aggExpr string, dest any, groupByCols ...string) error
}

// ================== 内部条件节点 ==================

// clause 代表一个 WHERE 条件节点，支持四种模式：
//   - 普通条件：sql + args
//   - 分组：isGroup=true，children 非空
//   - 子查询：isSubQuery=true
//   - 原生 SQL：isRaw=true（自动加括号）
type clause struct {
	sql        string
	args       []any
	children   []*clause
	typ        clauseType // AND / OR
	isGroup    bool
	isRaw      bool
	isSubQuery bool
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
	joins   []joinClause
	orders  []string
	groups  []string
	having  []any
	selects []any
	limit   int
	offset  int
}

type joinClause struct {
	sql  string
	args []any
}

// NewQuery 创建原生 gorm 条件构造器。
//
// 示例：
//
//	query.NewQuery(db.WithContext(ctx).Model(&model.Order{}))
func NewQuery(db *gorm.DB) IQueryBuilder {
	return &Builder{db: db}
}

// ---- 内部工具 ----

func (b *Builder) addClause(c *clause) IQueryBuilder {
	b.clauses = append(b.clauses, c)
	return b
}

func andClause(sql string, args ...any) *clause {
	return &clause{sql: sql, args: args, typ: clauseAnd}
}

func orClause(sql string, args ...any) *clause {
	return &clause{sql: sql, args: args, typ: clauseOr}
}

func rawAndClause(sql string, args ...any) *clause {
	return &clause{sql: "(" + sql + ")", args: args, typ: clauseAnd, isRaw: true}
}

func rawOrClause(sql string, args ...any) *clause {
	return &clause{sql: "(" + sql + ")", args: args, typ: clauseOr, isRaw: true}
}

// ---- SELECT ----

func (b *Builder) Select(cols ...any) IQueryBuilder {
	b.selects = append(b.selects, cols...)
	return b
}

// ---- 等值条件 ----

func (b *Builder) Eq(col string, val any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` = ?", col), val))
}

func (b *Builder) EqIfNotZero(col string, val any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(val), fmt.Sprintf("`%s` = ?", col), val)
}

func (b *Builder) EqIfNotNil(col string, val any) IQueryBuilder {
	return b.WhereIf(!isNilVal(val), fmt.Sprintf("`%s` = ?", col), derefVal(val))
}

func (b *Builder) Ne(col string, val any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` != ?", col), val))
}

func (b *Builder) NeIfNotZero(col string, val any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(val), fmt.Sprintf("`%s` != ?", col), val)
}

// ---- 模糊匹配 ----

func (b *Builder) Like(col string, val string) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` LIKE ?", col), "%"+val+"%"))
}

func (b *Builder) LikeIfNotEmpty(col string, val string) IQueryBuilder {
	return b.WhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), "%"+val+"%")
}

func (b *Builder) LikeLeft(col string, val string) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` LIKE ?", col), "%"+val))
}

func (b *Builder) LikeLeftIfNotEmpty(col string, val string) IQueryBuilder {
	return b.WhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), "%"+val)
}

func (b *Builder) LikeRight(col string, val string) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` LIKE ?", col), val+"%"))
}

func (b *Builder) LikeRightIfNotEmpty(col string, val string) IQueryBuilder {
	return b.WhereIf(val != "", fmt.Sprintf("`%s` LIKE ?", col), val+"%")
}

func (b *Builder) NotLike(col string, val string) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` NOT LIKE ?", col), "%"+val+"%"))
}

// ---- 大小比较 ----

func (b *Builder) Gt(col string, val any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` > ?", col), val))
}

func (b *Builder) GtIfNotZero(col string, val any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(val), fmt.Sprintf("`%s` > ?", col), val)
}

func (b *Builder) Gte(col string, val any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` >= ?", col), val))
}

func (b *Builder) GteIfNotZero(col string, val any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(val), fmt.Sprintf("`%s` >= ?", col), val)
}

func (b *Builder) Lt(col string, val any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` < ?", col), val))
}

func (b *Builder) LtIfNotZero(col string, val any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(val), fmt.Sprintf("`%s` < ?", col), val)
}

func (b *Builder) Lte(col string, val any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` <= ?", col), val))
}

func (b *Builder) LteIfNotZero(col string, val any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(val), fmt.Sprintf("`%s` <= ?", col), val)
}

// ---- 区间 ----

func (b *Builder) Between(col string, min, max any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` BETWEEN ? AND ?", col), min, max))
}

func (b *Builder) BetweenIfNotZero(col string, min, max any) IQueryBuilder {
	return b.WhereIf(!isZeroVal(min) && !isZeroVal(max), fmt.Sprintf("`%s` BETWEEN ? AND ?", col), min, max)
}

func (b *Builder) NotBetween(col string, min, max any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` NOT BETWEEN ? AND ?", col), min, max))
}

// ---- IN ----

func (b *Builder) In(col string, vals any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` IN ?", col), vals))
}

func (b *Builder) InIfNotEmpty(col string, vals any) IQueryBuilder {
	return b.WhereIf(!isEmptyVal(vals), fmt.Sprintf("`%s` IN ?", col), vals)
}

func (b *Builder) NotIn(col string, vals any) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` NOT IN ?", col), vals))
}

func (b *Builder) NotInIfNotEmpty(col string, vals any) IQueryBuilder {
	return b.WhereIf(!isEmptyVal(vals), fmt.Sprintf("`%s` NOT IN ?", col), vals)
}

// ---- NULL ----

func (b *Builder) IsNull(col string) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` IS NULL", col)))
}

func (b *Builder) IsNotNull(col string) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` IS NOT NULL", col)))
}

// ---- 核心能力 ----

func (b *Builder) WhereIf(condition bool, query string, args ...any) IQueryBuilder {
	if !condition {
		return b
	}
	return b.addClause(andClause(query, args...))
}

// ---- 分组括号 ----

func (b *Builder) WhereGroup(fn func(IQueryBuilder)) IQueryBuilder {
	child := &Builder{db: b.db}
	fn(child)
	if len(child.clauses) == 0 {
		return b
	}
	return b.addClause(&clause{children: child.clauses, typ: clauseAnd, isGroup: true})
}

func (b *Builder) OrGroup(fn func(IQueryBuilder)) IQueryBuilder {
	child := &Builder{db: b.db}
	fn(child)
	if len(child.clauses) == 0 {
		return b
	}
	return b.addClause(&clause{children: child.clauses, typ: clauseOr, isGroup: true})
}

// ---- 子查询 ----

func (b *Builder) SubQueryIn(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` IN (?)", col), sub))
}

func (b *Builder) SubQueryNotIn(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` NOT IN (?)", col), sub))
}

func (b *Builder) SubQueryExists(sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause("EXISTS (?)", sub))
}

func (b *Builder) SubQueryNotExists(sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause("NOT EXISTS (?)", sub))
}

func (b *Builder) SubQueryEq(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` = (?)", col), sub))
}

func (b *Builder) SubQueryNe(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` != (?)", col), sub))
}

func (b *Builder) SubQueryGt(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` > (?)", col), sub))
}

func (b *Builder) SubQueryGte(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` >= (?)", col), sub))
}

func (b *Builder) SubQueryLt(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` < (?)", col), sub))
}

func (b *Builder) SubQueryLte(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(andClause(fmt.Sprintf("`%s` <= (?)", col), sub))
}

func (b *Builder) OrSubQueryIn(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(orClause(fmt.Sprintf("`%s` IN (?)", col), sub))
}

func (b *Builder) OrSubQueryNotIn(col string, sub *gorm.DB) IQueryBuilder {
	return b.addClause(orClause(fmt.Sprintf("`%s` NOT IN (?)", col), sub))
}

func (b *Builder) OrSubQueryExists(sub *gorm.DB) IQueryBuilder {
	return b.addClause(orClause("EXISTS (?)", sub))
}

func (b *Builder) OrSubQueryNotExists(sub *gorm.DB) IQueryBuilder {
	return b.addClause(orClause("NOT EXISTS (?)", sub))
}

// ---- 原生 SQL ----

func (b *Builder) RawWhere(sql string, args ...any) IQueryBuilder {
	if sql == "" {
		return b
	}
	return b.addClause(rawAndClause(sql, args...))
}

func (b *Builder) RawOrWhere(sql string, args ...any) IQueryBuilder {
	if sql == "" {
		return b
	}
	return b.addClause(rawOrClause(sql, args...))
}

func (b *Builder) RawWhereIf(condition bool, sql string, args ...any) IQueryBuilder {
	if !condition || sql == "" {
		return b
	}
	return b.addClause(rawAndClause(sql, args...))
}

// ---- JOIN ----

func (b *Builder) Join(sql string, args ...any) IQueryBuilder {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "JOIN") {
		b.joins = append(b.joins, joinClause{sql, args})
	} else {
		b.joins = append(b.joins, joinClause{"JOIN " + sql, args})
	}
	return b
}

func (b *Builder) InnerJoin(sql string, args ...any) IQueryBuilder {
	return b.Join(sql, args...)
}

func (b *Builder) LeftJoin(sql string, args ...any) IQueryBuilder {
	b.joins = append(b.joins, joinClause{"LEFT JOIN " + sql, args})
	return b
}

func (b *Builder) RightJoin(sql string, args ...any) IQueryBuilder {
	b.joins = append(b.joins, joinClause{"RIGHT JOIN " + sql, args})
	return b
}

// ---- 排序 ----

func (b *Builder) OrderByAsc(col string) IQueryBuilder {
	b.orders = append(b.orders, fmt.Sprintf("`%s` ASC", col))
	return b
}

func (b *Builder) OrderByDesc(col string) IQueryBuilder {
	b.orders = append(b.orders, fmt.Sprintf("`%s` DESC", col))
	return b
}

func (b *Builder) OrderByRaw(expr string) IQueryBuilder {
	b.orders = append(b.orders, expr)
	return b
}

// ---- 分组 & HAVING ----

func (b *Builder) GroupBy(cols ...string) IQueryBuilder {
	for _, col := range cols {
		b.groups = append(b.groups, fmt.Sprintf("`%s`", col))
	}
	return b
}

func (b *Builder) Having(sql string, vals ...any) IQueryBuilder {
	b.having = append(b.having, sql)
	b.having = append(b.having, vals...)
	return b
}

// ---- 分页 ----

func (b *Builder) Page(pageNum, pageSize int) IQueryBuilder {
	if pageNum < 1 {
		pageNum = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	b.limit = pageSize
	b.offset = (pageNum - 1) * pageSize
	return b
}

func (b *Builder) Limit(n int) IQueryBuilder {
	b.limit = n
	return b
}

func (b *Builder) Offset(n int) IQueryBuilder {
	b.offset = n
	return b
}

// ================== SQL 构建 ==================

// applyClause 将单个 clause 节点递归应用到 *gorm.DB
func applyClause(db *gorm.DB, c *clause) *gorm.DB {
	if c.isGroup {
		// 分组括号：递归构建子条件，通过 func(*gorm.DB)*gorm.DB 产生括号
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

	// 普通条件 / Raw SQL / 子查询（统一走 Where/Or + sql + args）
	if c.typ == clauseOr {
		return db.Or(c.sql, c.args...)
	}
	return db.Where(c.sql, c.args...)
}

func (b *Builder) applyCommon(db *gorm.DB, withPage bool) *gorm.DB {
	// SELECT
	if len(b.selects) > 0 {
		db = db.Select(resolveSelects(b.selects))
	}

	// JOIN
	for _, j := range b.joins {
		if len(j.args) > 0 {
			db = db.Joins(j.sql, j.args...)
		} else {
			db = db.Joins(j.sql)
		}
	}

	// WHERE（统一走新 clause 树）
	for _, c := range b.clauses {
		db = applyClause(db, c)
	}

	// GROUP BY
	for _, g := range b.groups {
		db = db.Group(g)
	}

	// HAVING
	if len(b.having) > 0 {
		if havingSQL, ok := b.having[0].(string); ok {
			db = db.Having(havingSQL, b.having[1:]...)
		}
	}

	// ORDER BY
	for _, o := range b.orders {
		db = db.Order(o)
	}

	// 分页
	if withPage {
		if b.limit > 0 {
			db = db.Limit(b.limit)
		}
		if b.offset > 0 {
			db = db.Offset(b.offset)
		}
	}
	return db
}

func (b *Builder) buildDB() *gorm.DB      { return b.applyCommon(b.db, true) }
func (b *Builder) buildCountDB() *gorm.DB { return b.applyCommon(b.db, false) }

// ================== 执行方法 ==================

func (b *Builder) Find(dest any) error  { return b.buildDB().Find(dest).Error }
func (b *Builder) First(dest any) error { return b.buildDB().First(dest).Error }
func (b *Builder) Last(dest any) error  { return b.buildDB().Last(dest).Error }
func (b *Builder) Take(dest any) error  { return b.buildDB().Take(dest).Error }
func (b *Builder) Scan(dest any) error  { return b.buildDB().Scan(dest).Error }
func (b *Builder) Pluck(col string, dest any) error {
	return b.buildDB().Pluck(col, dest).Error
}

func (b *Builder) Count() (int64, error) {
	var total int64
	return total, b.buildCountDB().Count(&total).Error
}

func (b *Builder) FindAndCount(dest any) (int64, error) {
	total, err := b.Count()
	if err != nil {
		return 0, err
	}
	return total, b.buildDB().Find(dest).Error
}

func (b *Builder) Exists() (bool, error) {
	count, err := b.Limit(1).Count()
	return count > 0, err
}

// ================== 聚合函数 ==================

// AggResult 聚合查询结果。
// Valid=false 表示无匹配行（SQL 返回 NULL），此时 Value 为零值。
// 用 decimal.Decimal 避免 float64 精度丢失。
type AggResult struct {
	Value decimal.Decimal
	Valid bool
}

func (b *Builder) aggQuery(expr string) (AggResult, error) {
	var result struct{ Val *string }
	err := b.buildCountDB().Select(expr + " as val").Scan(&result).Error
	if err != nil {
		return AggResult{}, err
	}
	if result.Val == nil {
		return AggResult{Valid: false}, nil
	}
	d, err := decimal.NewFromString(*result.Val)
	if err != nil {
		return AggResult{}, fmt.Errorf("aggQuery decimal parse: %w", err)
	}
	return AggResult{Value: d, Valid: true}, nil
}

func (b *Builder) Sum(col string) (AggResult, error) {
	return b.aggQuery(fmt.Sprintf("SUM(`%s`)", col))
}

func (b *Builder) Avg(col string) (AggResult, error) {
	return b.aggQuery(fmt.Sprintf("AVG(`%s`)", col))
}

func (b *Builder) Max(col string) (AggResult, error) {
	return b.aggQuery(fmt.Sprintf("MAX(`%s`)", col))
}

func (b *Builder) Min(col string) (AggResult, error) {
	return b.aggQuery(fmt.Sprintf("MIN(`%s`)", col))
}

func (b *Builder) CountDistinct(col string) (int64, error) {
	var result struct{ Val *int64 }
	err := b.buildCountDB().
		Select(fmt.Sprintf("COUNT(DISTINCT `%s`) as val", col)).
		Scan(&result).Error
	if err != nil {
		return 0, err
	}
	if result.Val == nil {
		return 0, nil
	}
	return *result.Val, nil
}

func (b *Builder) SumGroup(col, alias string, dest any, groupByCols ...string) error {
	selectExprs := []string{fmt.Sprintf("SUM(`%s`) as %s", col, alias)}
	for _, g := range groupByCols {
		selectExprs = append(selectExprs, fmt.Sprintf("`%s`", g))
	}
	db := b.buildCountDB().Select(selectExprs)
	for _, g := range groupByCols {
		db = db.Group(fmt.Sprintf("`%s`", g))
	}
	return db.Scan(dest).Error
}

func (b *Builder) AggGroup(aggExpr string, dest any, groupByCols ...string) error {
	groupCols := make([]string, 0, len(groupByCols))
	for _, g := range groupByCols {
		groupCols = append(groupCols, fmt.Sprintf("`%s`", g))
	}
	db := b.buildCountDB().Select(append(groupCols, aggExpr))
	for _, g := range groupCols {
		db = db.Group(g)
	}
	return db.Scan(dest).Error
}

// ================== 泛型分页函数 ==================

// FindByPage 泛型分页查询，返回 (数据列表, 总数, error)。
//
// 示例：
//
//	list, total, err := query.FindByPage[model.SysUserEntity](
//	    query.NewQuery(db.WithContext(ctx).Model(&model.SysUserEntity{})).
//	        EqIfNotZero("dept_id", deptId).
//	        LikeIfNotEmpty("username", keyword).
//	        OrderByDesc("create_time"),
//	    pageNum, pageSize,
//	)
func FindByPage[T any](q IQueryBuilder, pageNum, pageSize int) ([]T, int64, error) {
	var list []T
	total, err := q.Page(pageNum, pageSize).FindAndCount(&list)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ScanByPage 泛型分页扫描，返回 (数据列表, 总数, error)。
// 适合自定义 SELECT 字段、连表查询等需要扫描到自定义结构体的场景。
//
// 示例：
//
//	list, total, err := query.ScanByPage[UserWithDept](
//	    query.NewQuery(db.WithContext(ctx).Model(&model.SysUserEntity{})).
//	        Select("u.user_id", "u.username as user_name", "d.dept_name").
//	        LeftJoin("sys_dept d ON d.dept_id = u.dept_id").
//	        EqIfNotZero("u.dept_id", deptId).
//	        OrderByDesc("u.create_time"),
//	    pageNum, pageSize,
//	)
func ScanByPage[T any](q IQueryBuilder, pageNum, pageSize int) ([]T, int64, error) {
	total, err := q.Count()
	if err != nil {
		return nil, 0, err
	}
	var list []T
	if err := q.Page(pageNum, pageSize).Scan(&list); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
