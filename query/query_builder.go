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
// gorm-gen 场景（类型安全、IDE 可跳转）请使用 GenWrap + IGenWrapper。
//
// ── 三种条件控制语义 ──────────────────────────────────────────
//
//	方法后缀          生效时机               典型使用场景
//	无后缀            无条件生效             固定过滤条件（如 status=1 的业务逻辑）
//	IfNotZero         值非零时生效           可选筛选字段（int/string/time）
//	IfNotNil          值非 nil 时生效        可选筛选字段（指针类型，区分"未传"和"传了 0"）
//	IfNotEmpty        slice/map 非空时生效   IN 查询的 ID 列表等
//
// ── 条件分组规则（重要）──────────────────────────────────────
//
//	⚠️ OR 条件必须通过 OrGroup 使用，禁止将 OR 条件直接散落在链式调用中。
//	这样可以保证生成的 SQL 括号语义清晰，不会因运算符优先级产生歧义。
//
//	正确：.Eq("a",1).OrGroup(func(q){ q.Eq("b",2).Eq("c",3) })
//	      → WHERE a = 1 OR (b = 2 AND c = 3)
//
//	错误（不要这样写）：直接调用 db.Or(...) 再接链式条件
//
// ── 快速上手 ────────────────────────────────────────────────
//
//	// 分页列表查询
//	var list []*model.Order
//	total, err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	    EqIfNotZero("user_id", userID).       // userID=0 时跳过
//	    EqIfNotNil("status", statusPtr).      // statusPtr=nil 时跳过，ptr(0) 也生效
//	    LikeIfNotEmpty("order_no", keyword).  // keyword="" 时跳过
//	    BetweenIfNotZero("amount", min, max). // min 或 max 为 0 时跳过
//	    InIfNotEmpty("dept_id", deptIDs).     // deptIDs 为空时跳过
//	    OrderByDesc("created_at").
//	    Page(pageNum, pageSize).
//	    FindAndCount(&list)
//
//	// 联表查询扫描到自定义 VO
//	var voList []OrderWithUserVO
//	total, err := query.ScanByPage[OrderWithUserVO](
//	    query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        Select("o.id", "o.order_no", "u.username").
//	        LeftJoin("sys_user u ON u.id = o.user_id").
//	        EqIfNotZero("o.user_id", userID).
//	        OrderByDesc("o.created_at"),
//	    pageNum, pageSize,
//	)
//
//	// 聚合查询
//	result, err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	    Eq("status", 2).
//	    Sum("amount")
//	if result.Valid {
//	    fmt.Println("总金额:", result.Value)
//	}
type IQueryBuilder interface {
	// -------- SELECT 指定查询字段 --------
	//
	// 不调用 Select 时默认 SELECT *，建议大表场景指定字段减少带宽消耗。
	// 支持字符串、gorm-gen field.Expr（已修复 %v 格式化乱码问题）、fmt.Stringer 混用。
	//
	// 示例：
	//   .Select("id", "order_no", "amount")
	//   .Select(dao.Order.ID, dao.Order.Amount)
	//   .Select("id", query.NewCase().When("status=1","'待审'").Else("'其他'").As("status_name"))
	//   .Select(field.NewUnsafeFieldRaw("COUNT(*) AS total"))
	Select(cols ...any) IQueryBuilder

	// -------- 等值条件 --------

	// Eq 精确匹配，零值也生效。适合固定过滤条件。
	// 示例：.Eq("status", 1) → WHERE `status` = 1
	Eq(col string, val any) IQueryBuilder

	// EqIfNotZero 精确匹配，val 为零值（0、""、false、nil）时跳过。
	// ⚠️ bool 字段慎用：false 会被当零值跳过，应使用 EqIfNotNil（传 *bool）。
	// 示例：
	//   .EqIfNotZero("user_id", 0)   → 跳过
	//   .EqIfNotZero("user_id", 123) → WHERE `user_id` = 123
	EqIfNotZero(col string, val any) IQueryBuilder

	// EqIfNotNil 精确匹配，val 为 nil 时跳过，ptr(0) 也生效。
	// 适合用指针区分"未传该参数"和"明确传了 0/false/空字符串"的场景。
	// 示例：
	//   status := int8(0)
	//   .EqIfNotNil("status", nil)     → 跳过
	//   .EqIfNotNil("status", &status) → WHERE `status` = 0
	EqIfNotNil(col string, val any) IQueryBuilder

	// Ne 不等于，零值也生效。
	// 示例：.Ne("status", 0) → WHERE `status` != 0
	Ne(col string, val any) IQueryBuilder

	// NeIfNotZero 不等于，val 为零值时跳过。
	NeIfNotZero(col string, val any) IQueryBuilder

	// -------- 模糊匹配 --------

	// Like 全模糊 %val%，val 为空也生效。
	Like(col string, val string) IQueryBuilder

	// LikeIfNotEmpty 全模糊 %val%，val 为空字符串时跳过。
	// 示例：.LikeIfNotEmpty("username", keyword) → keyword="" 时跳过
	LikeIfNotEmpty(col string, val string) IQueryBuilder

	// LikeLeft 左模糊 %val（以 val 结尾）。
	LikeLeft(col string, val string) IQueryBuilder

	// LikeLeftIfNotEmpty 左模糊，val 为空时跳过。
	LikeLeftIfNotEmpty(col string, val string) IQueryBuilder

	// LikeRight 右模糊 val%（以 val 开头），可利用前缀索引，性能优于全模糊。
	// 示例：.LikeRight("order_no", "ORD2024") → WHERE `order_no` LIKE 'ORD2024%'
	LikeRight(col string, val string) IQueryBuilder

	// LikeRightIfNotEmpty 右模糊，val 为空时跳过。
	LikeRightIfNotEmpty(col string, val string) IQueryBuilder

	// NotLike NOT LIKE %val%。
	NotLike(col string, val string) IQueryBuilder

	// -------- 大小比较 --------

	// Gt 大于，零值也生效。示例：.Gt("score", 0) → WHERE `score` > 0
	Gt(col string, val any) IQueryBuilder

	// GtIfNotZero 大于，val 为零值时跳过。
	GtIfNotZero(col string, val any) IQueryBuilder

	// Gte 大于等于，零值也生效。
	Gte(col string, val any) IQueryBuilder

	// GteIfNotZero 大于等于，val 为零值时跳过。
	// 示例：.GteIfNotZero("created_at", startTime) → startTime 非零时 WHERE `created_at` >= ?
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

	// Between 闭区间 [min, max]，零值也生效。
	// 示例：.Between("age", 18, 60) → WHERE `age` BETWEEN 18 AND 60
	Between(col string, min, max any) IQueryBuilder

	// BetweenIfNotZero 闭区间，min 和 max 同时非零才生效。
	// 适合金额区间、时间区间等可选筛选场景。
	// 示例：.BetweenIfNotZero("amount", minAmt, maxAmt) → 两者均非零时生效
	BetweenIfNotZero(col string, min, max any) IQueryBuilder

	// NotBetween NOT BETWEEN min AND max。
	NotBetween(col string, min, max any) IQueryBuilder

	// -------- IN 条件 --------

	// In IN 查询，vals 传 slice。空 slice 会生成 IN () 导致语法错误，请用 InIfNotEmpty。
	// 示例：.In("status", []int{1,2,3}) → WHERE `status` IN (1,2,3)
	In(col string, vals any) IQueryBuilder

	// InIfNotEmpty IN 查询，vals 为 nil 或空 slice 时自动跳过，不会生成 IN () 错误。
	// 示例：.InIfNotEmpty("id", ids) → ids 非空时 WHERE `id` IN (?)
	InIfNotEmpty(col string, vals any) IQueryBuilder

	// NotIn NOT IN 查询，空 slice 同样有语法错误风险，建议用 NotInIfNotEmpty。
	NotIn(col string, vals any) IQueryBuilder

	// NotInIfNotEmpty NOT IN 查询，vals 为空时跳过。
	NotInIfNotEmpty(col string, vals any) IQueryBuilder

	// -------- NULL 判断 --------

	// IsNull 判断字段为 NULL。示例：.IsNull("deleted_at") → WHERE `deleted_at` IS NULL
	IsNull(col string) IQueryBuilder

	// IsNotNull 判断字段不为 NULL。
	IsNotNull(col string) IQueryBuilder

	// -------- 条件开关 --------

	// WhereIf 条件 condition 为 true 时添加 WHERE，支持 ? 占位参数。
	// 适合根据业务逻辑动态决定是否添加条件的场景。
	// 示例：
	//   .WhereIf(isAdmin, "dept_id IS NOT NULL") → 管理员才过滤有部门的用户
	//   .WhereIf(len(ids)>0, "id IN ?", ids)
	WhereIf(condition bool, query string, args ...any) IQueryBuilder

	// -------- 分组括号（OR 条件必须走这里）--------
	//
	// ⚠️ OR 条件必须通过 OrGroup 使用，禁止裸调用 db.Or() 散落在链式调用中。
	//    这样保证生成的 SQL 括号语义明确，不会因 AND/OR 优先级产生歧义。

	// WhereGroup 创建 AND 括号条件组（括号内 AND 连接）。
	// 示例：
	//   .WhereGroup(func(q IQueryBuilder) {
	//       q.Eq("type", 1).OrGroup(func(q IQueryBuilder) {
	//           q.Eq("type", 2).Eq("source", "web")
	//       })
	//   })
	//   → WHERE (type = 1 OR (type = 2 AND source = 'web'))
	WhereGroup(fn func(IQueryBuilder)) IQueryBuilder

	// OrGroup 创建 OR 括号条件组（括号内 AND 连接，整体以 OR 接入父层）。
	// 示例：
	//   .Eq("status", 1).
	//    OrGroup(func(q IQueryBuilder) {
	//        q.Eq("type", 2).Eq("source", "app")
	//    })
	//   → WHERE `status` = 1 OR (`type` = 2 AND `source` = 'app')
	OrGroup(fn func(IQueryBuilder)) IQueryBuilder

	// -------- 子查询 --------
	//
	// 参数传 *gorm.DB，由调用方通过 db.Model(...).Select(...).Where(...) 构造好后传入。
	//
	// 示例：
	//   // 构造子查询
	//   subDB := db.Model(&Dept{}).Select("id").Where("status = ?", 1)
	//
	//   // IN 子查询：WHERE `dept_id` IN (SELECT id FROM dept WHERE status = 1)
	//   .SubQueryIn("dept_id", subDB)
	//
	//   // NOT IN 子查询
	//   .SubQueryNotIn("dept_id", subDB)
	//
	//   // EXISTS：WHERE EXISTS (SELECT id FROM orders WHERE ...)
	//   .SubQueryExists(subDB)
	//
	//   // 标量比较：WHERE `score` = (SELECT MAX(score) FROM exam WHERE ...)
	//   .SubQueryEq("score", subDB)
	//
	//   // OR 版本
	//   .OrSubQueryIn("dept_id", subDB)

	// SubQueryIn WHERE col IN (SELECT ...)
	SubQueryIn(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryNotIn WHERE col NOT IN (SELECT ...)
	SubQueryNotIn(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryExists WHERE EXISTS (SELECT ...)
	SubQueryExists(sub *gorm.DB) IQueryBuilder

	// SubQueryNotExists WHERE NOT EXISTS (SELECT ...)
	SubQueryNotExists(sub *gorm.DB) IQueryBuilder

	// SubQueryEq WHERE col = (SELECT 单值标量)
	SubQueryEq(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryNe WHERE col != (SELECT 单值标量)
	SubQueryNe(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryGt WHERE col > (SELECT 单值标量)
	SubQueryGt(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryGte WHERE col >= (SELECT 单值标量)
	SubQueryGte(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryLt WHERE col < (SELECT 单值标量)
	SubQueryLt(col string, sub *gorm.DB) IQueryBuilder

	// SubQueryLte WHERE col <= (SELECT 单值标量)
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
	//    .RawWhere("a = 1 OR b = 2") → WHERE (a = 1 OR b = 2)
	//
	// 适用于 Builder 方法无法表达的复杂场景：JSON 函数、全文检索、窗口函数等。

	// RawWhere 无条件加入原生 AND WHERE（自动包括括号）。
	// 示例：
	//   .RawWhere("DATE(created_at) = ?", "2024-01-01")
	//   .RawWhere("JSON_CONTAINS(tags, ?)", `["go","gorm"]`)
	//   .RawWhere("MATCH(title, body) AGAINST(? IN BOOLEAN MODE)", keyword)
	RawWhere(sql string, args ...any) IQueryBuilder

	// RawOrWhere 无条件加入原生 OR WHERE（自动包括括号）。
	// 示例：
	//   .Eq("status", 1).RawOrWhere("amount > ?", 1000)
	//   → WHERE `status` = 1 OR (amount > 1000)
	RawOrWhere(sql string, args ...any) IQueryBuilder

	// RawWhereIf condition 为 true 时加入原生 AND WHERE。
	// 示例：
	//   .RawWhereIf(keyword != "", "MATCH(title, body) AGAINST(?)", keyword)
	RawWhereIf(condition bool, sql string, args ...any) IQueryBuilder

	// -------- JOIN 连表 --------

	// Join INNER JOIN，自动补全 "JOIN " 前缀，支持 ? 占位参数。
	// 示例：.Join("sys_dept d ON d.id = o.dept_id")
	Join(sql string, args ...any) IQueryBuilder

	// InnerJoin INNER JOIN 显式写法，与 Join 完全等价。
	InnerJoin(sql string, args ...any) IQueryBuilder

	// LeftJoin LEFT JOIN，保留左表全部行（右表无匹配时字段为 NULL）。
	// 示例：.LeftJoin("sys_user u ON u.id = o.user_id AND u.status = ?", 1)
	LeftJoin(sql string, args ...any) IQueryBuilder

	// RightJoin RIGHT JOIN，保留右表全部行。
	RightJoin(sql string, args ...any) IQueryBuilder

	// -------- 排序 --------

	// OrderByAsc 升序排列。示例：.OrderByAsc("created_at") → ORDER BY `created_at` ASC
	OrderByAsc(col string) IQueryBuilder

	// OrderByDesc 降序排列。示例：.OrderByDesc("created_at") → ORDER BY `created_at` DESC
	OrderByDesc(col string) IQueryBuilder

	// OrderByRaw 原始排序表达式，用于复杂场景（枚举排序、多字段等）。
	// 示例：
	//   .OrderByRaw("FIELD(status, 1, 3, 2)") → 按枚举值自定义顺序
	//   .OrderByRaw("score DESC, created_at ASC")
	OrderByRaw(expr string) IQueryBuilder

	// -------- 分组 & HAVING --------

	// GroupBy GROUP BY 分组，可传多个字段。
	// 示例：.GroupBy("dept_id", "status") → GROUP BY `dept_id`, `status`
	GroupBy(cols ...string) IQueryBuilder

	// Having HAVING 条件，支持 ? 占位参数，通常与 GroupBy 配合使用。
	// 示例：.GroupBy("dept_id").Having("COUNT(*) > ?", 5)
	Having(sql string, vals ...any) IQueryBuilder

	// -------- 分页 & 限制 --------

	// Page 分页，pageNum 从 1 开始，自动计算 LIMIT 和 OFFSET。
	// 会对 pageNum < 1 和 pageSize < 1 做保护（最小值为 1 和 10）。
	// 示例：.Page(2, 20) → LIMIT 20 OFFSET 20（第 2 页，每页 20 条）
	Page(pageNum, pageSize int) IQueryBuilder

	// Limit 直接设置最大返回条数，与 Page 互斥（Page 优先）。
	// 示例：.Limit(100) → LIMIT 100
	Limit(n int) IQueryBuilder

	// Offset 直接设置跳过条数，与 Page 互斥（Page 优先）。
	// 示例：.Offset(50) → OFFSET 50
	Offset(n int) IQueryBuilder

	// -------- 执行方法 --------

	// Find 查询列表，dest 传指向 slice 的指针。无结果时返回空 slice（非 error）。
	// 示例：
	//   var list []*model.Order
	//   err := q.Find(&list)
	Find(dest any) error

	// First 查询第一条（按主键升序），无记录时返回 gorm.ErrRecordNotFound。
	// 示例：
	//   var order model.Order
	//   err := q.Eq("id", id).First(&order)
	//   if errors.Is(err, gorm.ErrRecordNotFound) { ... }
	First(dest any) error

	// Last 查询最后一条（按主键降序），无记录时返回 gorm.ErrRecordNotFound。
	Last(dest any) error

	// Take 查询一条（不指定排序），无记录时返回 gorm.ErrRecordNotFound。
	Take(dest any) error

	// Count 统计满足条件的记录数，不带 LIMIT/OFFSET。
	// 示例：
	//   total, err := q.Eq("status", 1).Count()
	Count() (int64, error)

	// FindAndCount 同时查询列表和总数，内部执行两次 SQL（一次带分页的 SELECT，一次 COUNT）。
	// 通常配合 Page 使用，是分页列表接口的首选方法。
	// 示例：
	//   var list []*model.Order
	//   total, err := q.Page(1, 10).FindAndCount(&list)
	FindAndCount(dest any) (int64, error)

	// Pluck 查询单列值到 slice，适合只需要 ID 列表等场景。
	// 示例：
	//   var ids []int64
	//   err := q.Eq("status", 1).Pluck("id", &ids)
	Pluck(col string, dest any) error

	// Scan 扫描到任意结构体，适合自定义 SELECT 字段、联表查询结果的场景。
	// 示例：
	//   var vo []OrderVO
	//   err := q.Select("o.id", "u.username").LeftJoin("...").Scan(&vo)
	Scan(dest any) error

	// Exists 判断是否存在满足条件的记录（内部执行 LIMIT 1 的 COUNT）。
	// 示例：
	//   ok, err := q.Eq("username", "admin").Exists()
	Exists() (bool, error)

	// -------- 聚合函数 --------
	//
	// 所有聚合函数返回 AggResult，Valid=false 表示无匹配行（SQL 返回 NULL）。
	// 使用 decimal.Decimal 存储结果，避免 float64 精度丢失（适合金额计算）。

	// Sum 对指定字段求和。无匹配行时 Valid=false，Value 为零值。
	// 示例：
	//   result, err := q.Eq("status", 2).Sum("amount")
	//   if result.Valid { fmt.Println(result.Value) }
	Sum(col string) (AggResult, error)

	// Avg 对指定字段求平均值。无匹配行时 Valid=false。
	Avg(col string) (AggResult, error)

	// Max 对指定字段求最大值。无匹配行时 Valid=false。
	Max(col string) (AggResult, error)

	// Min 对指定字段求最小值。无匹配行时 Valid=false。
	Min(col string) (AggResult, error)

	// CountDistinct 统计指定字段不重复值的数量。
	// 示例：q.CountDistinct("user_id") → SELECT COUNT(DISTINCT `user_id`)
	CountDistinct(col string) (int64, error)

	// SumGroup 按分组字段聚合求和，结果扫描到 dest slice（dest 需包含分组字段和 alias 字段）。
	// 示例：
	//   type DeptSum struct { DeptID int64; Total decimal.Decimal }
	//   var result []DeptSum
	//   err := q.SumGroup("amount", "total", &result, "dept_id")
	//   // → SELECT SUM(`amount`) AS total, `dept_id` FROM ... GROUP BY `dept_id`
	SumGroup(col, alias string, dest any, groupByCols ...string) error

	// AggGroup 通用分组聚合，支持自定义聚合表达式（比 SumGroup 更灵活）。
	// 示例：
	//   err := q.AggGroup("SUM(amount) AS total, COUNT(*) AS cnt", &result, "dept_id", "status")
	AggGroup(aggExpr string, dest any, groupByCols ...string) error
}

// ================== 内部条件节点 ==================

// clause 代表一个 WHERE 条件节点，支持四种模式：
//   - 普通条件：sql + args（最常见）
//   - 分组：isGroup=true，children 非空（WhereGroup/OrGroup）
//   - 子查询：sub 指向 *gorm.DB（SubQueryIn 等）
//   - 原生 SQL：isRaw=true（RawWhere，自动包括括号）
type clause struct {
	sql      string
	args     []any
	children []*clause
	typ      clauseType // AND / OR
	isGroup  bool
	isRaw    bool
}

type clauseType int

const (
	clauseAnd clauseType = iota
	clauseOr
)

// ================== Builder 实现 ==================

// Builder IQueryBuilder 的具体实现，持有所有查询条件并在执行时组装为 SQL
type Builder struct {
	db      *gorm.DB  // 来自 db.WithContext(ctx).Model(&Xxx{}) 的基础 DB
	clauses []*clause // WHERE 条件树
	joins   []joinClause
	orders  []string
	groups  []string
	having  []any // [havingSQL, arg1, arg2, ...]
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
// db 参数应已通过 WithContext 注入 context，并通过 Model 指定操作的表：
//
//	query.NewQuery(db.WithContext(ctx).Model(&model.Order{}))
//
// 如果已注册多租户插件，context 中的租户 ID 会自动注入到查询条件中。
// 如果使用多数据源管理器，建议通过 DS.Auto(ctx) 获取 db：
//
//	db, err := DS.Auto(ctx)
//	query.NewQuery(db.Model(&model.Order{}))
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

// rawAndClause 原生 SQL 自动包括括号，防止 OR 优先级问题
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

// ---- 条件开关 ----

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

// ================== SQL 构建（内部）==================

// applyClause 将单个 clause 节点递归应用到 *gorm.DB
func applyClause(db *gorm.DB, c *clause) *gorm.DB {
	if c.isGroup {
		// 分组括号：通过传 func(*gorm.DB)*gorm.DB 让 gorm 自动加括号
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

// applyCommon 将所有构造条件组装到 gorm.DB
// withPage=true 时附加 LIMIT/OFFSET（用于实际数据查询）
// withPage=false 时不附加分页（用于 COUNT 查询，COUNT 不需要 LIMIT/OFFSET）
func (b *Builder) applyCommon(db *gorm.DB, withPage bool) *gorm.DB {
	// SELECT 字段（COUNT 查询不需要，由 buildCountDB 直接跳过 SELECT 拼接）
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
	// WHERE
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
	// LIMIT / OFFSET（仅数据查询时附加）
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

// buildDB 构建带分页的查询 DB（用于 Find/First/Scan 等）
func (b *Builder) buildDB() *gorm.DB { return b.applyCommon(b.db, true) }

// buildCountDB 构建不带分页的查询 DB（用于 Count/Exists/聚合，COUNT 不需要 LIMIT）
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
	// 先执行 COUNT（不带 LIMIT/OFFSET），再执行带分页的 SELECT
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
//
// Valid=false 表示无匹配行（SQL 返回 NULL），此时 Value 为零值，
// 调用方应在使用 Value 前检查 Valid。
//
// 使用 decimal.Decimal 存储，避免 float64 精度丢失，特别适合金额计算场景。
//
// 示例：
//
//	result, err := q.Eq("status", 2).Sum("amount")
//	if err != nil { ... }
//	if !result.Valid {
//	    fmt.Println("无数据")
//	    return
//	}
//	fmt.Println("总金额:", result.Value.String())
type AggResult struct {
	Value decimal.Decimal
	Valid bool
}

// aggQuery 内部聚合查询，执行 SELECT {expr} AS val 并将结果解析为 AggResult
func (b *Builder) aggQuery(expr string) (AggResult, error) {
	var result struct{ Val *string }
	err := b.buildCountDB().Select(expr + " as val").Scan(&result).Error
	if err != nil {
		return AggResult{}, err
	}
	if result.Val == nil {
		return AggResult{Valid: false}, nil // 无匹配行
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
// 内部执行两次 SQL：COUNT 查询 + 带 LIMIT/OFFSET 的 SELECT 查询。
// 适合简单列表查询（无联表，结果直接映射到 model struct）。
//
// 示例：
//
//	list, total, err := query.FindByPage[*model.Order](
//	    query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("user_id", userID).
//	        LikeIfNotEmpty("order_no", keyword).
//	        OrderByDesc("created_at"),
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
//
// 与 FindByPage 的区别：使用 Scan 而非 Find，适合联表查询、自定义 SELECT 字段等
// 需要将结果映射到自定义 VO 结构体的场景。
//
// 示例：
//
//	type OrderVO struct {
//	    ID       int64  `json:"id"`
//	    OrderNo  string `json:"orderNo"`
//	    Username string `json:"username"` // 来自 join 的字段
//	}
//
//	list, total, err := query.ScanByPage[OrderVO](
//	    query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        Select("o.id", "o.order_no", "u.username").
//	        LeftJoin("sys_user u ON u.id = o.user_id").
//	        EqIfNotZero("o.user_id", userID).
//	        OrderByDesc("o.created_at"),
//	    pageNum, pageSize,
//	)
func ScanByPage[T any](q IQueryBuilder, pageNum, pageSize int) ([]T, int64, error) {
	// 先 COUNT（不带分页）
	total, err := q.Count()
	if err != nil {
		return nil, 0, err
	}
	// 再 Scan（带分页）
	var list []T
	if err := q.Page(pageNum, pageSize).Scan(&list); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
