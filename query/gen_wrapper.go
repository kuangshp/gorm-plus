package query

import (
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// ================== 条件结构 ==================

// condType 条件类型：AND 或 OR
type condType int

const (
	condAnd condType = iota
	condOr
)

// condItem 单个条件项，支持四种模式：
//   - 普通表达式：expr 非 nil，isRaw=false，isNested=false
//   - 嵌套分组：isNested=true，subCond 非 nil
//   - 原生 SQL：isRaw=true，subDB=nil
//   - 子查询：isRaw=true，subDB 非 nil，rawSQL 含占位符
type condItem struct {
	expr     field.Expr
	subCond  *condGroup
	subDB    *gorm.DB // 子查询，非 nil 时走子查询渲染路径
	rawSQL   string
	rawArgs  []any
	typ      condType
	isNested bool
	isRaw    bool
}

// condGroup 条件组
type condGroup struct {
	conds []*condItem
	typ   condType
}

func newCondGroup(typ condType) *condGroup {
	return &condGroup{conds: make([]*condItem, 0), typ: typ}
}

func (g *condGroup) add(item *condItem) { g.conds = append(g.conds, item) }
func (g *condGroup) isEmpty() bool      { return len(g.conds) == 0 }

// ================== GenDao 约束 ==================

// GenDao 对 gorm-gen Do 对象的完整约束。
type GenDao[D any] interface {
	gen.Dao
	UnderlyingDB() *gorm.DB
	ReplaceDB(*gorm.DB) D
}

// ================== IGenWrapper 接口 ==================
//
// gorm-gen 链式条件构造器，基于字段对象（类型安全，IDE 可跳转，编译期检查）。
//
// ⚠️ 注意事项：
//   - bool 字段请勿用 EqIfNotZero（false 会被跳过），改用 If(expr, true)
//   - OR 条件强制通过 OrGroup 使用，避免与前置条件产生歧义
//   - Page() 与 Limit()/Offset() 互斥，Page 优先
//   - 所有条件最终会包在一层括号内（防止与已有 Do 条件产生 OR 污染）
//   - Raw SQL 会自动包括一层括号，防止优先级歧义
//
// 快速使用：
//
//	entities, err := query.GenWrap(dao.InterviewEntity.WithContext(ctx)).
//	    Always(dao.InterviewEntity.Status.Eq(int64(global.InterviewStatusSuccess))).
//	    EqIfNotZero(dao.InterviewEntity.DeptID.Eq(deptId), deptId).
//	    GteIfNotZero(dao.InterviewEntity.StartTime.Gte(startDate), startDate).
//	    LteIfNotZero(dao.InterviewEntity.StartTime.Lte(endDate), endDate).
//	    InIfNotEmpty(dao.InterviewEntity.ID.In(ids...), ids).
//	    Apply().
//	    LeftJoin(dao.ContractEntity, dao.InterviewEntity.ContractID.EqCol(dao.ContractEntity.ID)).
//	    Select(dao.InterviewEntity.ID, dao.ContractEntity.PaymentType).
//	    Order(dao.InterviewEntity.StartTime.Desc()).
//	    Scan(&result)
type IGenWrapper[D GenDao[D]] interface {
	// -------- SELECT 指定字段 --------
	//
	// 支持字符串、field.Expr（gorm-gen 字段对象）、CaseWhenBuilder 混用。
	// Apply() 会在 ReplaceDB 前将 Select 应用到底层 DB。
	//
	// 示例：
	//   // 纯字段对象
	//   .Select(dao.Order.ID, dao.Order.Amount)
	//
	//   // 混用字符串 + 字段对象 + CASE WHEN
	//   .Select(
	//       dao.Order.ID,
	//       "dept_name",
	//       query.NewCase().When("status=1", "'待审核'").Else("'其他'").As("status_name"),
	//   )
	//
	//   // 原生表达式（用 field.NewUnsafeFieldRaw）
	//   .Select(dao.Order.ID, field.NewUnsafeFieldRaw("COUNT(*) AS total"))
	Select(cols ...any) IGenWrapper[D]

	// -------- 基础条件 --------

	// Always 无条件加入 WHERE，也是传入子查询表达式的入口。
	Always(expr field.Expr) IGenWrapper[D]

	// If 根据 bool 决定是否加入 WHERE。
	If(expr field.Expr, condition bool) IGenWrapper[D]

	// -------- 语义条件 --------

	// EqIfNotZero 等于，val 为零值时跳过。⚠️ 不适用于 bool 字段。
	EqIfNotZero(expr field.Expr, val any) IGenWrapper[D]

	// EqIfNotNil 等于，val 为 nil 时跳过。
	EqIfNotNil(expr field.Expr, val any) IGenWrapper[D]

	// NeIfNotZero 不等于，val 为零值时跳过。
	NeIfNotZero(expr field.Expr, val any) IGenWrapper[D]

	// GtIfNotZero 大于，val 为零值时跳过。
	GtIfNotZero(expr field.Expr, val any) IGenWrapper[D]

	// GteIfNotZero 大于等于，val 为零值时跳过。
	GteIfNotZero(expr field.Expr, val any) IGenWrapper[D]

	// LtIfNotZero 小于，val 为零值时跳过。
	LtIfNotZero(expr field.Expr, val any) IGenWrapper[D]

	// LteIfNotZero 小于等于，val 为零值时跳过。
	LteIfNotZero(expr field.Expr, val any) IGenWrapper[D]

	// LikeIfNotEmpty LIKE %val%，val 为空时跳过。
	LikeIfNotEmpty(expr field.Expr, val string) IGenWrapper[D]

	// InIfNotEmpty IN，vals 为空时跳过。
	InIfNotEmpty(expr field.Expr, val any) IGenWrapper[D]

	// NotInIfNotEmpty NOT IN，vals 为空时跳过。
	NotInIfNotEmpty(expr field.Expr, val any) IGenWrapper[D]

	// BetweenIfNotZero BETWEEN，min 和 max 同时非零才生效。
	BetweenIfNotZero(expr field.Expr, min, max any) IGenWrapper[D]

	// -------- 核心能力 --------

	// WhereIf 条件为 true 时批量加入 AND WHERE。
	WhereIf(condition bool, exprs ...field.Expr) IGenWrapper[D]

	// -------- 分组 --------
	//
	// ⚠️ OR 条件强制通过 OrGroup 使用，禁止裸 OR 散落在链式调用中。

	// WhereGroup 创建一个 AND 组合条件块（括号内 AND 连接）。
	// 示例：
	//   .WhereGroup(func(w IGenWrapper[D]) {
	//       w.EqIfNotZero(dao.Entity.A, a).
	//         OrGroup(func(w IGenWrapper[D]) { w.Always(dao.Entity.B.Eq(b)) })
	//   })
	//   → WHERE (a = ? OR (b = ?))
	WhereGroup(fn func(IGenWrapper[D])) IGenWrapper[D]

	// OrGroup 创建一个 OR 组合条件块（括号内 AND 连接，整体以 OR 接入父层）。
	// 示例：
	//   .Always(dao.Entity.X.Eq(1)).
	//    OrGroup(func(w IGenWrapper[D]) {
	//        w.EqIfNotZero(dao.Entity.Y, y).EqIfNotZero(dao.Entity.Z, z)
	//    })
	//   → WHERE (x = 1) OR (y = ? AND z = ?)
	OrGroup(fn func(IGenWrapper[D])) IGenWrapper[D]

	// -------- 原生 SQL --------
	//
	// 适用于 gorm-gen 字段方法无法表达的复杂场景（函数、JSON、全文检索等）。
	// ⚠️ Raw SQL 会自动包一层括号，防止运算符优先级歧义。
	// 示例（自动括号效果）：
	//   .RawWhere("a = 1 OR b = 2")  → WHERE (a = 1 OR b = 2)

	// RawWhere 无条件加入原生 AND WHERE。
	// 示例：
	//   .RawWhere("DATE(created_at) = ?", "2024-01-01")
	//   .RawWhere("JSON_CONTAINS(tags, ?)", `["go","gorm"]`)
	//   .RawWhere("amount BETWEEN ? AND ?", 100, 500)
	RawWhere(sql string, args ...any) IGenWrapper[D]

	// RawOrWhere 无条件加入原生 OR WHERE（括号内）。
	// 示例：
	//   .Always(dao.Order.Status.Eq(1)).RawOrWhere("amount > ?", 1000)
	//   → WHERE (status = 1) OR (amount > 1000)
	RawOrWhere(sql string, args ...any) IGenWrapper[D]

	// RawWhereIf 条件成立时加入原生 AND WHERE。
	// 示例：
	//   .RawWhereIf(keyword != "", "MATCH(title, body) AGAINST(?)", keyword)
	RawWhereIf(condition bool, sql string, args ...any) IGenWrapper[D]

	// -------- 子查询 --------
	//
	// 参数使用 *gorm.DB，由调用方通过 Do 对象链式方法构造后传入。
	// 构造方式：dao.XxxEntity.WithContext(ctx).Select(dao.Xxx.ID).Where(...).UnderlyingDB()
	//
	// 示例：
	//   subDB := dao.Dept.WithContext(ctx).
	//       Select(dao.Dept.ID).
	//       Where(dao.Dept.Status.Eq(1)).
	//       UnderlyingDB()
	//
	//   // IN：WHERE dept_id IN (SELECT id FROM dept WHERE status = 1)
	//   .SubQueryIn(dao.User.DeptID, subDB)
	//
	//   // NOT IN：WHERE dept_id NOT IN (SELECT id FROM dept WHERE ...)
	//   .SubQueryNotIn(dao.User.DeptID, subDB)
	//
	//   // EXISTS：WHERE EXISTS (SELECT id FROM orders WHERE ...)
	//   .SubQueryExists(subDB)
	//
	//   // NOT EXISTS：WHERE NOT EXISTS (SELECT ...)
	//   .SubQueryNotExists(subDB)
	//
	//   // 标量比较：WHERE score = (SELECT MAX(score) FROM exam WHERE ...)
	//   .SubQueryEq(dao.User.Score, subDB)
	//   .SubQueryGt(dao.User.Score, subDB)
	//
	//   // OR 版本：
	//   .OrSubQueryIn(dao.User.DeptID, subDB)

	// SubQueryIn WHERE col IN (SELECT ...)
	SubQueryIn(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryNotIn WHERE col NOT IN (SELECT ...)
	SubQueryNotIn(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryExists WHERE EXISTS (SELECT ...)
	SubQueryExists(sub *gorm.DB) IGenWrapper[D]

	// SubQueryNotExists WHERE NOT EXISTS (SELECT ...)
	SubQueryNotExists(sub *gorm.DB) IGenWrapper[D]

	// SubQueryEq WHERE col = (SELECT 单值)
	SubQueryEq(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryNe WHERE col != (SELECT 单值)
	SubQueryNe(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryGt WHERE col > (SELECT 单值)
	SubQueryGt(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryGte WHERE col >= (SELECT 单值)
	SubQueryGte(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryLt WHERE col < (SELECT 单值)
	SubQueryLt(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// SubQueryLte WHERE col <= (SELECT 单值)
	SubQueryLte(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// OrSubQueryIn OR col IN (SELECT ...)
	OrSubQueryIn(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// OrSubQueryNotIn OR col NOT IN (SELECT ...)
	OrSubQueryNotIn(col field.Expr, sub *gorm.DB) IGenWrapper[D]

	// OrSubQueryExists OR EXISTS (SELECT ...)
	OrSubQueryExists(sub *gorm.DB) IGenWrapper[D]

	// OrSubQueryNotExists OR NOT EXISTS (SELECT ...)
	OrSubQueryNotExists(sub *gorm.DB) IGenWrapper[D]

	// -------- 排序 --------

	// OrderAsc 升序排列。
	OrderAsc(columns ...field.Expr) IGenWrapper[D]

	// OrderDesc 降序排列。
	OrderDesc(columns ...field.Expr) IGenWrapper[D]

	// -------- 分页 --------

	// Page 分页，page 从 1 开始，⚠️ 与 Limit()/Offset() 互斥，Page 优先。
	// 示例：.Page(2, 20) → LIMIT 20 OFFSET 20
	Page(page, size int) IGenWrapper[D]

	// Limit 直接设置返回条数，与 Page() 互斥。
	Limit(n int) IGenWrapper[D]

	// Offset 直接设置跳过条数，与 Page() 互斥。
	Offset(n int) IGenWrapper[D]

	// -------- 输出 --------

	// Apply 将所有条件应用到 Do，返回原始 Do 继续 gorm-gen 链式调用。
	Apply() D
}

// ================== condBuilder：统一条件构建内核 ==================
//
// GenWrapper 和 groupWrapper 的条件逻辑完全相同，抽取到 condBuilder 统一实现。
// GenWrapper  持有 condBuilder 作为根节点，同时管理排序、分页、Do 对象。
// groupWrapper 持有 condBuilder 作为子节点，Apply/Page/Limit/Offset 均为空操作。

type condBuilder[D GenDao[D]] struct {
	do     D
	group  *condGroup
	orders *[]field.Expr
}

func newCondBuilder[D GenDao[D]](do D, group *condGroup, orders *[]field.Expr) *condBuilder[D] {
	return &condBuilder[D]{do: do, group: group, orders: orders}
}

func (b *condBuilder[D]) addExpr(expr field.Expr, typ condType) {
	b.group.add(&condItem{expr: expr, typ: typ})
}

func (b *condBuilder[D]) addRaw(sql string, args []any, typ condType) {
	if sql == "" {
		return
	}
	b.group.add(&condItem{rawSQL: "(" + sql + ")", rawArgs: args, typ: typ, isRaw: true})
}

func (b *condBuilder[D]) addGroup(sub *condGroup, typ condType) {
	if !sub.isEmpty() {
		b.group.add(&condItem{subCond: sub, typ: typ, isNested: true})
	}
}

// addSubQuery 添加子查询条件。
//   - col 非 nil：rawSQL 形如 "? IN (?)"，渲染时 col.RawExpr() 作为第一个参数
//   - col 为 nil：rawSQL 形如 "EXISTS (?)"，渲染时只传 subDB
func (b *condBuilder[D]) addSubQuery(rawSQL string, col field.Expr, sub *gorm.DB, typ condType) {
	b.group.add(&condItem{expr: col, subDB: sub, rawSQL: rawSQL, typ: typ, isRaw: true})
}

func (b *condBuilder[D]) buildSub(typ condType, fn func(IGenWrapper[D])) *condGroup {
	sub := newCondGroup(typ)
	fn(&groupWrapper[D]{condBuilder: newCondBuilder(b.do, sub, b.orders)})
	return sub
}

func (b *condBuilder[D]) always(expr field.Expr) {
	b.addExpr(expr, condAnd)
}

func (b *condBuilder[D]) ifCond(expr field.Expr, condition bool) {
	if condition {
		b.addExpr(expr, condAnd)
	}
}

func (b *condBuilder[D]) whereIf(condition bool, exprs ...field.Expr) {
	if !condition {
		return
	}
	for _, expr := range exprs {
		b.addExpr(expr, condAnd)
	}
}

func (b *condBuilder[D]) eqIfNotZero(expr field.Expr, val any)       { b.ifCond(expr, !isZeroVal(val)) }
func (b *condBuilder[D]) eqIfNotNil(expr field.Expr, val any)        { b.ifCond(expr, !isNilVal(val)) }
func (b *condBuilder[D]) neIfNotZero(expr field.Expr, val any)       { b.ifCond(expr, !isZeroVal(val)) }
func (b *condBuilder[D]) gtIfNotZero(expr field.Expr, val any)       { b.ifCond(expr, !isZeroVal(val)) }
func (b *condBuilder[D]) gteIfNotZero(expr field.Expr, val any)      { b.ifCond(expr, !isZeroVal(val)) }
func (b *condBuilder[D]) ltIfNotZero(expr field.Expr, val any)       { b.ifCond(expr, !isZeroVal(val)) }
func (b *condBuilder[D]) lteIfNotZero(expr field.Expr, val any)      { b.ifCond(expr, !isZeroVal(val)) }
func (b *condBuilder[D]) likeIfNotEmpty(expr field.Expr, val string) { b.ifCond(expr, val != "") }
func (b *condBuilder[D]) inIfNotEmpty(expr field.Expr, val any)      { b.ifCond(expr, !isEmptyVal(val)) }
func (b *condBuilder[D]) notInIfNotEmpty(expr field.Expr, val any)   { b.ifCond(expr, !isEmptyVal(val)) }
func (b *condBuilder[D]) betweenIfNotZero(expr field.Expr, min, max any) {
	b.ifCond(expr, !isZeroVal(min) && !isZeroVal(max))
}

func (b *condBuilder[D]) rawWhere(sql string, args ...any)   { b.addRaw(sql, args, condAnd) }
func (b *condBuilder[D]) rawOrWhere(sql string, args ...any) { b.addRaw(sql, args, condOr) }
func (b *condBuilder[D]) rawWhereIf(condition bool, sql string, args ...any) {
	if condition {
		b.addRaw(sql, args, condAnd)
	}
}

// ---- 子查询内部方法 ----

func (b *condBuilder[D]) subQueryIn(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? IN (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryNotIn(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? NOT IN (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryExists(sub *gorm.DB, ct condType) {
	b.addSubQuery("EXISTS (?)", nil, sub, ct)
}
func (b *condBuilder[D]) subQueryNotExists(sub *gorm.DB, ct condType) {
	b.addSubQuery("NOT EXISTS (?)", nil, sub, ct)
}
func (b *condBuilder[D]) subQueryEq(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? = (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryNe(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? != (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryGt(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? > (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryGte(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? >= (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryLt(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? < (?)", col, sub, ct)
}
func (b *condBuilder[D]) subQueryLte(col field.Expr, sub *gorm.DB, ct condType) {
	b.addSubQuery("? <= (?)", col, sub, ct)
}

func (b *condBuilder[D]) whereGroup(fn func(IGenWrapper[D])) {
	b.addGroup(b.buildSub(condAnd, fn), condAnd)
}

func (b *condBuilder[D]) orGroup(fn func(IGenWrapper[D])) {
	b.addGroup(b.buildSub(condAnd, fn), condOr)
}

func (b *condBuilder[D]) orderAsc(orders *[]field.Expr, columns ...field.Expr) {
	*orders = append(*orders, columns...)
}

func (b *condBuilder[D]) orderDesc(orders *[]field.Expr, columns ...field.Expr) {
	for _, col := range columns {
		if d, ok := any(col).(interface{ Desc() field.Expr }); ok {
			*orders = append(*orders, d.Desc())
		}
	}
}

// ================== GenWrapper ==================

// GenWrapper gorm-gen 链式条件构造器（根节点）
type GenWrapper[D GenDao[D]] struct {
	*condBuilder[D]
	do        D
	orders    []field.Expr
	selects   []any
	page      int
	pageSize  int
	limit     int
	offset    int
	hasOffset bool
}

// GenWrap 从干净 Do 对象创建 GenWrapper。
func GenWrap[D GenDao[D]](do D) IGenWrapper[D] {
	w := &GenWrapper[D]{do: do, orders: make([]field.Expr, 0)}
	w.condBuilder = newCondBuilder(do, newCondGroup(condAnd), &w.orders)
	return w
}

// FromDo 将已有 Do 对象包装为 GenWrapper，原有条件保留。
// 建议优先使用 GenWrap，仅在需要衔接已有 Do 时使用 FromDo。
func FromDo[D GenDao[D]](do D) IGenWrapper[D] {
	return GenWrap(do)
}

func (w *GenWrapper[D]) Select(cols ...any) IGenWrapper[D] {
	w.selects = append(w.selects, cols...)
	return w
}

func (w *GenWrapper[D]) Always(expr field.Expr) IGenWrapper[D] {
	w.always(expr)
	return w
}

func (w *GenWrapper[D]) If(expr field.Expr, condition bool) IGenWrapper[D] {
	w.ifCond(expr, condition)
	return w
}

func (w *GenWrapper[D]) EqIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	w.eqIfNotZero(expr, val)
	return w
}

func (w *GenWrapper[D]) EqIfNotNil(expr field.Expr, val any) IGenWrapper[D] {
	w.eqIfNotNil(expr, val)
	return w
}

func (w *GenWrapper[D]) NeIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	w.neIfNotZero(expr, val)
	return w
}

func (w *GenWrapper[D]) GtIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	w.gtIfNotZero(expr, val)
	return w
}

func (w *GenWrapper[D]) GteIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	w.gteIfNotZero(expr, val)
	return w
}

func (w *GenWrapper[D]) LtIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	w.ltIfNotZero(expr, val)
	return w
}

func (w *GenWrapper[D]) LteIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	w.lteIfNotZero(expr, val)
	return w
}

func (w *GenWrapper[D]) LikeIfNotEmpty(expr field.Expr, val string) IGenWrapper[D] {
	w.likeIfNotEmpty(expr, val)
	return w
}

func (w *GenWrapper[D]) InIfNotEmpty(expr field.Expr, val any) IGenWrapper[D] {
	w.inIfNotEmpty(expr, val)
	return w
}

func (w *GenWrapper[D]) NotInIfNotEmpty(expr field.Expr, val any) IGenWrapper[D] {
	w.notInIfNotEmpty(expr, val)
	return w
}

func (w *GenWrapper[D]) BetweenIfNotZero(expr field.Expr, min, max any) IGenWrapper[D] {
	w.betweenIfNotZero(expr, min, max)
	return w
}

func (w *GenWrapper[D]) WhereIf(condition bool, exprs ...field.Expr) IGenWrapper[D] {
	w.whereIf(condition, exprs...)
	return w
}

func (w *GenWrapper[D]) WhereGroup(fn func(IGenWrapper[D])) IGenWrapper[D] {
	w.whereGroup(fn)
	return w
}

func (w *GenWrapper[D]) OrGroup(fn func(IGenWrapper[D])) IGenWrapper[D] {
	w.orGroup(fn)
	return w
}

func (w *GenWrapper[D]) RawWhere(sql string, args ...any) IGenWrapper[D] {
	w.rawWhere(sql, args...)
	return w
}

func (w *GenWrapper[D]) RawOrWhere(sql string, args ...any) IGenWrapper[D] {
	w.rawOrWhere(sql, args...)
	return w
}

func (w *GenWrapper[D]) RawWhereIf(condition bool, sql string, args ...any) IGenWrapper[D] {
	w.rawWhereIf(condition, sql, args...)
	return w
}

// ---- 子查询接口实现（GenWrapper）----

func (w *GenWrapper[D]) SubQueryIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryIn(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryNotIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryNotIn(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryExists(sub *gorm.DB) IGenWrapper[D] {
	w.subQueryExists(sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryNotExists(sub *gorm.DB) IGenWrapper[D] {
	w.subQueryNotExists(sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryEq(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryEq(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryNe(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryNe(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryGt(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryGt(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryGte(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryGte(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryLt(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryLt(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) SubQueryLte(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryLte(col, sub, condAnd)
	return w
}
func (w *GenWrapper[D]) OrSubQueryIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryIn(col, sub, condOr)
	return w
}
func (w *GenWrapper[D]) OrSubQueryNotIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	w.subQueryNotIn(col, sub, condOr)
	return w
}
func (w *GenWrapper[D]) OrSubQueryExists(sub *gorm.DB) IGenWrapper[D] {
	w.subQueryExists(sub, condOr)
	return w
}
func (w *GenWrapper[D]) OrSubQueryNotExists(sub *gorm.DB) IGenWrapper[D] {
	w.subQueryNotExists(sub, condOr)
	return w
}

func (w *GenWrapper[D]) OrderAsc(columns ...field.Expr) IGenWrapper[D] {
	w.orderAsc(&w.orders, columns...)
	return w
}

func (w *GenWrapper[D]) OrderDesc(columns ...field.Expr) IGenWrapper[D] {
	w.orderDesc(&w.orders, columns...)
	return w
}

func (w *GenWrapper[D]) Page(page, size int) IGenWrapper[D] {
	if page > 0 && size > 0 {
		w.page = page
		w.pageSize = size
	}
	return w
}

func (w *GenWrapper[D]) Limit(n int) IGenWrapper[D] {
	if n > 0 {
		w.limit = n
	}
	return w
}

func (w *GenWrapper[D]) Offset(n int) IGenWrapper[D] {
	if n >= 0 {
		w.offset = n
		w.hasOffset = true
	}
	return w
}

// Apply 将所有条件、排序、分页应用到 Do，返回原始 Do 继续链式调用。
func (w *GenWrapper[D]) Apply() D {
	if w.group.isEmpty() && len(w.orders) == 0 && len(w.selects) == 0 && w.page == 0 && w.limit == 0 && !w.hasOffset {
		return w.do
	}

	db := w.do.UnderlyingDB()

	// SELECT
	if len(w.selects) > 0 {
		db = db.Select(resolveSelects(w.selects))
	}

	// 根条件组统一包一层括号，与 Do 已有条件隔离
	if !w.group.isEmpty() {
		db = db.Where(func(tx *gorm.DB) *gorm.DB {
			return applyCondGroup(tx, w.group)
		})
	}

	for _, order := range w.orders {
		db = db.Order(order)
	}

	if w.page > 0 && w.pageSize > 0 {
		db = db.Limit(w.pageSize).Offset((w.page - 1) * w.pageSize)
	} else {
		if w.limit > 0 {
			db = db.Limit(w.limit)
		}
		if w.hasOffset {
			db = db.Offset(w.offset)
		}
	}

	return w.do.ReplaceDB(db)
}

// ================== groupWrapper ==================

// groupWrapper 分组内部包装器，供 WhereGroup/OrGroup 回调使用。
// ⚠️ Apply/Page/Limit/Offset 在此上下文无意义：Apply() 调用直接 panic，其余静默忽略。
type groupWrapper[D GenDao[D]] struct {
	*condBuilder[D]
}

func (g *groupWrapper[D]) Always(expr field.Expr) IGenWrapper[D] {
	g.always(expr)
	return g
}

func (g *groupWrapper[D]) If(expr field.Expr, condition bool) IGenWrapper[D] {
	g.ifCond(expr, condition)
	return g
}

func (g *groupWrapper[D]) EqIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	g.eqIfNotZero(expr, val)
	return g
}

func (g *groupWrapper[D]) EqIfNotNil(expr field.Expr, val any) IGenWrapper[D] {
	g.eqIfNotNil(expr, val)
	return g
}

func (g *groupWrapper[D]) NeIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	g.neIfNotZero(expr, val)
	return g
}

func (g *groupWrapper[D]) GtIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	g.gtIfNotZero(expr, val)
	return g
}

func (g *groupWrapper[D]) GteIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	g.gteIfNotZero(expr, val)
	return g
}

func (g *groupWrapper[D]) LtIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	g.ltIfNotZero(expr, val)
	return g
}

func (g *groupWrapper[D]) LteIfNotZero(expr field.Expr, val any) IGenWrapper[D] {
	g.lteIfNotZero(expr, val)
	return g
}

func (g *groupWrapper[D]) LikeIfNotEmpty(expr field.Expr, val string) IGenWrapper[D] {
	g.likeIfNotEmpty(expr, val)
	return g
}

func (g *groupWrapper[D]) InIfNotEmpty(expr field.Expr, val any) IGenWrapper[D] {
	g.inIfNotEmpty(expr, val)
	return g
}

func (g *groupWrapper[D]) NotInIfNotEmpty(expr field.Expr, val any) IGenWrapper[D] {
	g.notInIfNotEmpty(expr, val)
	return g
}

func (g *groupWrapper[D]) BetweenIfNotZero(expr field.Expr, min, max any) IGenWrapper[D] {
	g.betweenIfNotZero(expr, min, max)
	return g
}

func (g *groupWrapper[D]) WhereIf(condition bool, exprs ...field.Expr) IGenWrapper[D] {
	g.whereIf(condition, exprs...)
	return g
}

func (g *groupWrapper[D]) WhereGroup(fn func(IGenWrapper[D])) IGenWrapper[D] {
	g.whereGroup(fn)
	return g
}

func (g *groupWrapper[D]) OrGroup(fn func(IGenWrapper[D])) IGenWrapper[D] {
	g.orGroup(fn)
	return g
}

func (g *groupWrapper[D]) RawWhere(sql string, args ...any) IGenWrapper[D] {
	g.rawWhere(sql, args...)
	return g
}

func (g *groupWrapper[D]) RawOrWhere(sql string, args ...any) IGenWrapper[D] {
	g.rawOrWhere(sql, args...)
	return g
}

func (g *groupWrapper[D]) RawWhereIf(condition bool, sql string, args ...any) IGenWrapper[D] {
	g.rawWhereIf(condition, sql, args...)
	return g
}

// ---- 子查询接口实现（groupWrapper）----

func (g *groupWrapper[D]) SubQueryIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryIn(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryNotIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryNotIn(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryExists(sub *gorm.DB) IGenWrapper[D] {
	g.subQueryExists(sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryNotExists(sub *gorm.DB) IGenWrapper[D] {
	g.subQueryNotExists(sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryEq(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryEq(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryNe(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryNe(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryGt(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryGt(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryGte(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryGte(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryLt(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryLt(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) SubQueryLte(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryLte(col, sub, condAnd)
	return g
}
func (g *groupWrapper[D]) OrSubQueryIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryIn(col, sub, condOr)
	return g
}
func (g *groupWrapper[D]) OrSubQueryNotIn(col field.Expr, sub *gorm.DB) IGenWrapper[D] {
	g.subQueryNotIn(col, sub, condOr)
	return g
}
func (g *groupWrapper[D]) OrSubQueryExists(sub *gorm.DB) IGenWrapper[D] {
	g.subQueryExists(sub, condOr)
	return g
}
func (g *groupWrapper[D]) OrSubQueryNotExists(sub *gorm.DB) IGenWrapper[D] {
	g.subQueryNotExists(sub, condOr)
	return g
}

func (g *groupWrapper[D]) OrderAsc(columns ...field.Expr) IGenWrapper[D] {
	g.orderAsc(g.orders, columns...)
	return g
}

func (g *groupWrapper[D]) OrderDesc(columns ...field.Expr) IGenWrapper[D] {
	g.orderDesc(g.orders, columns...)
	return g
}

func (g *groupWrapper[D]) Page(_, _ int) IGenWrapper[D]   { return g }
func (g *groupWrapper[D]) Limit(_ int) IGenWrapper[D]     { return g }
func (g *groupWrapper[D]) Offset(_ int) IGenWrapper[D]    { return g }
func (g *groupWrapper[D]) Select(_ ...any) IGenWrapper[D] { return g } // 分组内无意义，静默忽略

func (g *groupWrapper[D]) Apply() D {
	panic("query: groupWrapper.Apply() must not be called inside WhereGroup/OrGroup callback")
}

// ================== SQL 构建 ==================

func applyCondGroup(db *gorm.DB, group *condGroup) *gorm.DB {
	for _, item := range group.conds {
		switch {
		case item.isRaw && item.subDB != nil:
			// 子查询：col 非 nil 时 col.RawExpr() 作为第一个参数，subDB 作为最后一个参数
			// gorm 会将 *gorm.DB 类型的参数自动展开为子查询 SQL
			var args []any
			if item.expr != nil {
				args = []any{item.expr.RawExpr(), item.subDB}
			} else {
				args = []any{item.subDB}
			}
			if item.typ == condOr {
				db = db.Or(item.rawSQL, args...)
			} else {
				db = db.Where(item.rawSQL, args...)
			}
		case item.isRaw:
			// 普通原生 SQL（addRaw 阶段已自动包括括号）
			if item.typ == condOr {
				db = db.Or(item.rawSQL, item.rawArgs...)
			} else {
				db = db.Where(item.rawSQL, item.rawArgs...)
			}
		case item.isNested && item.subCond != nil:
			scope := buildNestedScope(item.subCond)
			if item.typ == condOr {
				db = db.Or(scope)
			} else {
				db = db.Where(scope)
			}
		default:
			if item.typ == condOr {
				db = db.Or(item.expr)
			} else {
				db = db.Where(item.expr)
			}
		}
	}
	return db
}

func buildNestedScope(group *condGroup) func(*gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		return applyCondGroup(tx, group)
	}
}
