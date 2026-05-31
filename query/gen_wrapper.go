// Package db 提供基于 gorm-gen 的查询条件构建扩展工具。
//
// # 设计原则
//
// 本包只封装 gorm-gen 原生 DO 不支持的能力：
//   - 模糊查询自动拼 %（Like / LLike / RLike）
//   - 可选条件（Where / WhereIf / OrWhereIf / BetweenIfNotZero）
//   - 简单分组（WhereGroup / OrGroup）：传入多个 field.Expr，组内 AND 连接，自动加括号
//   - 函数分组（WhereGroupFn / OrGroupFn）：传入函数，组内可使用完整 wrapper 能力（WhereIf / Like 等）
//   - 原生 SQL 条件（RawWhere / RawOrWhere / RawWhereIf）
//   - 表别名（As）：联表查询时为当前模型设置别名
//
// gorm-gen 原生已支持的能力（Eq/Gt/Lt/Order/Count/Find 等）请在 Apply() 后直接调用。
// Limit / Offset / Select 已在 IGenWrapper 中提供，可在 fn 回调内直接调用，
// 也可在 Apply() 后调用原生 DO 的同名方法，效果一致。
//
// # 快速上手
//
//	// 场景：账号列表查询，username 后缀模糊，status 非零时过滤，按创建时间倒序，分页
//	accountList, err := db.Wrap(dao.AccountEntity.WithContext(ctx)).
//	    LLike(dao.AccountEntity.Username, username).
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Order(dao.AccountEntity.CreatedAt.Desc()).
//	    Limit(20).Offset(0).
//	    Find()
//
// # 简单分组（WhereGroup / OrGroup）
//
//	// AND 分组：WHERE (username LIKE '%kw%' AND status = 1)
//	db.Wrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroup(
//	        dao.AccountEntity.Username.Like("%kw%"),
//	        dao.AccountEntity.Status.Eq(1),
//	    ).Apply().Find()
//
//	// OR 分组：WHERE status = 1 OR (role = 2 AND dept_id = 10)
//	db.Wrap(dao.AccountEntity.WithContext(ctx)).
//	    Where(dao.AccountEntity.Status.Eq(1)).
//	    OrGroup(
//	        dao.AccountEntity.Role.Eq(2),
//	        dao.AccountEntity.DeptID.Eq(10),
//	    ).Apply().Find()
//
// # 函数分组（WhereGroupFn / OrGroupFn）
//
//	// AND 函数分组：组内可用 WhereIf / Like 等完整能力
//	// WHERE (username LIKE '%admin%' AND status = 1)  -- status=0 时无 status 条件
//	db.Wrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
//	    }).Apply().Find()
//
//	// OR 函数分组：WHERE org_id = 1 OR (username LIKE '%admin' AND role = 99)
//	db.Wrap(dao.AccountEntity.WithContext(ctx)).
//	    Where(dao.AccountEntity.OrgID.Eq(orgID)).
//	    OrGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
//	    }).Apply().Find()
//
// # 表别名（As）
//
//	// 联表查询时为当前模型设置别名，之后 RawWhere 等条件中可用别名前缀
//	// WHERE n.status = 1，联表 LEFT JOIN sys_user u ON u.user_id = n.create_by
//	db.Wrap(dao.NotifyEntity.WithContext(ctx)).
//	    As("n").
//	    WhereIf(status != 0, dao.NotifyEntity.Status.Eq(status)).
//	    RawWhere("n.title LIKE ?", "%"+keyword+"%").
//	    Apply().
//	    Select("n.id", "n.title", "u.username AS creator").
//	    Joins("LEFT JOIN sys_user u ON u.user_id = n.create_by").
//	    Find()
package query

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// ================== 内部条件结构 ==================

type condType int

const (
	condAnd condType = iota
	condOr
)

// condItem 表示一个条件节点，支持四种形态：
//   - 普通 field.Expr（gorm-gen 列表达式）
//   - 简单分组（WhereGroup / OrGroup）：多个 expr 加括号，组内 AND 连接
//   - 函数分组（WhereGroupFn / OrGroupFn）：组内可用完整 wrapper 能力
//   - 原生 SQL（RawWhere / RawOrWhere）
type condItem struct {
	expr      field.Expr   // 普通列表达式
	exprs     []field.Expr // 简单分组内的多个表达式
	subGroup  *condGroup   // 函数分组内构建出的条件组
	rawSQL    string       // 原生 SQL 模板
	rawArgs   []any        // 原生 SQL 参数
	typ       condType     // condAnd 或 condOr
	isGroup   bool         // 简单分组节点
	isFnGroup bool         // 函数分组节点
	isRaw     bool         // 原生 SQL 节点
}

type condGroup struct {
	conds []*condItem
}

func newCondGroup() *condGroup {
	return &condGroup{conds: make([]*condItem, 0)}
}

func (g *condGroup) add(item *condItem) { g.conds = append(g.conds, item) }
func (g *condGroup) isEmpty() bool      { return len(g.conds) == 0 }

// ================== 泛型约束 ==================

// GenDo 是对 gorm-gen 生成的 DO 的最小接口约束。
// gorm-gen 生成的每个实体 DO 均自动满足此接口。
type GenDo[T any] interface {
	UnderlyingDB() *gorm.DB
	WithContext(ctx context.Context) T
	ReplaceDB(db *gorm.DB)
}

// ================== Wrap 入口 ==================

// Wrap 将 gorm-gen 生成的 DO 包裹为 IGenWrapper，开启扩展条件链式构建。
// 调用 Apply() 后返回原生 DO，可继续使用所有 gorm-gen 原生方法。
//
// 示例：
//
//	accountList, err := db.Wrap(dao.AccountEntity.WithContext(ctx)).
//	    LLike(dao.AccountEntity.Username, username).
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Order(dao.AccountEntity.CreatedAt.Desc()).
//	    Limit(20).
//	    Find()
func Wrap[T GenDo[T]](do T) IGenWrapper[T] {
	// 从 DO 的底层 DB 中提取原始 ctx，Apply 时复用，避免 ctx 丢失（traceID / 租户信息等）
	ctx := do.UnderlyingDB().Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return &GenWrapper[T]{
		do:    do,
		ctx:   ctx,
		group: newCondGroup(),
		replaceDB: func(newDB *gorm.DB) T {
			newDO := do.WithContext(ctx)
			newDO.ReplaceDB(newDB)
			return newDO
		},
	}
}

// ================== IGenWrapper 接口 ==================

// IGenWrapper 扩展条件构建器接口，只包含 gorm-gen 原生不支持的能力。
// 所有方法均支持链式调用，最终通过 Apply() 返回原生 DO。
type IGenWrapper[T GenDo[T]] interface {
	// As 为当前模型表设置别名，适合联表查询时区分字段归属。
	// 建议在其他条件方法之前调用。
	//
	//   // WHERE n.status = 1，联表 LEFT JOIN sys_user u ON u.user_id = n.create_by
	//   db.Wrap(dao.NotifyEntity.WithContext(ctx)).
	//       As("n").
	//       WhereIf(status != 0, dao.NotifyEntity.Status.Eq(status)).
	//       RawWhere("n.title LIKE ?", "%"+keyword+"%").
	//       Apply().
	//       Select("n.id", "n.title", "u.username AS creator").
	//       Joins("LEFT JOIN sys_user u ON u.user_id = n.create_by").
	//       Find()
	As(alias string) IGenWrapper[T]

	// Like 双侧模糊：WHERE col LIKE '%val%'，val 为空时跳过。
	//
	//   .Like(dao.AccountEntity.Username, "admin")
	//   => WHERE username LIKE '%admin%'
	Like(col field.Expr, val string) IGenWrapper[T]

	// LLike 左侧模糊：WHERE col LIKE '%val'，val 为空时跳过。
	//
	//   .LLike(dao.AccountEntity.Username, "admin")
	//   => WHERE username LIKE '%admin'
	LLike(col field.Expr, val string) IGenWrapper[T]

	// RLike 右侧模糊：WHERE col LIKE 'val%'，val 为空时跳过。
	//
	//   .RLike(dao.AccountEntity.Username, "admin")
	//   => WHERE username LIKE 'admin%'
	RLike(col field.Expr, val string) IGenWrapper[T]

	// BetweenIfNotZero 范围查询：WHERE col BETWEEN min AND max。
	// min 或 max 任一为零值时整体跳过。
	//
	//   .BetweenIfNotZero(dao.AccountEntity.CreatedAt, startTime, endTime)
	//   => WHERE created_at BETWEEN '2024-01-01' AND '2024-12-31'
	BetweenIfNotZero(col field.Expr, min, max any) IGenWrapper[T]

	// Where 追加一个或多个 AND 条件。
	//
	//   .Where(dao.AccountEntity.Status.Eq(1))
	//   => WHERE status = 1
	//
	//   // 同时追加多个条件（全部 AND 连接）
	//   .Where(
	//       dao.AccountEntity.Role.Eq(role),
	//       dao.AccountEntity.IsActive.Eq(true),
	//   )
	//   => WHERE role = 2 AND is_active = true
	Where(exprs ...field.Expr) IGenWrapper[T]

	// WhereIf condition 为 true 时追加一个或多个 AND 条件，否则整体跳过。
	//
	//   .WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
	//   => WHERE status = 1  （status=0 时无此条件）
	//
	//   // 同时追加多个条件（全部 AND 连接）
	//   .WhereIf(role != 0,
	//       dao.AccountEntity.Role.Eq(role),
	//       dao.AccountEntity.IsActive.Eq(true),
	//   )
	//   => WHERE role = 2 AND is_active = true
	WhereIf(condition bool, exprs ...field.Expr) IGenWrapper[T]

	// OrWhereIf condition 为 true 时追加一个或多个 OR 条件，否则整体跳过。
	//
	//   .Where(dao.AccountEntity.Username.Eq(req.Username)).
	//    OrWhereIf(req.Mobile != "", dao.AccountEntity.Mobile.Eq(req.Mobile)).
	//    OrWhereIf(req.Email != "", dao.AccountEntity.Email.Eq(req.Email))
	//   => WHERE username = ? OR mobile = ? OR email = ?
	OrWhereIf(condition bool, exprs ...field.Expr) IGenWrapper[T]

	// WhereGroup 将多个 field.Expr 用括号包裹后以 AND 连接到主查询，组内条件以 AND 连接。
	// 适合组内条件固定、无需条件控制的简单场景。
	// 需要组内 WhereIf / Like 等能力时请使用 WhereGroupFn。
	//
	//   .WhereGroup(
	//       dao.AccountEntity.Role.Eq(1),
	//       dao.AccountEntity.Status.Eq(1),
	//   )
	//   => WHERE (role = 1 AND status = 1)
	WhereGroup(exprs ...field.Expr) IGenWrapper[T]

	// WhereGroupIf condition 为 true 时才追加 AND 分组，否则整体跳过。
	// 适合前端可选参数为空时跳过整组条件。
	//
	//   .WhereGroupIf(req.Keyword != "",
	//       dao.AccountEntity.Username.Like("%"+req.Keyword+"%"),
	//       dao.AccountEntity.Mobile.Like("%"+req.Keyword+"%"),
	//   )
	//   => WHERE (username LIKE ? AND mobile LIKE ?)
	WhereGroupIf(condition bool, exprs ...field.Expr) IGenWrapper[T]

	// OrGroup 将多个 field.Expr 用括号包裹后以 OR 连接到主查询，组内条件以 AND 连接。
	// 需要组内 WhereIf / Like 等能力时请使用 OrGroupFn。
	//
	//   .Where(dao.AccountEntity.Status.Eq(1)).
	//    OrGroup(
	//        dao.AccountEntity.Role.Eq(99),
	//        dao.AccountEntity.DeptID.Eq(10),
	//    )
	//   => WHERE status = 1 OR (role = 99 AND dept_id = 10)
	OrGroup(exprs ...field.Expr) IGenWrapper[T]

	// OrGroupIf condition 为 true 时才追加 OR 分组，否则整体跳过。
	// 适合前端可选参数为空时跳过整组 OR 条件。
	//
	//   .Where(dao.AccountEntity.Status.Eq(1)).
	//    OrGroupIf(req.Keyword != "",
	//        dao.AccountEntity.Username.Like("%"+req.Keyword+"%"),
	//        dao.AccountEntity.Mobile.Like("%"+req.Keyword+"%"),
	//    )
	//   => WHERE status = 1 OR (username LIKE ? AND mobile LIKE ?)
	OrGroupIf(condition bool, exprs ...field.Expr) IGenWrapper[T]

	// WhereGroupFn 将 fn 内构建的条件用括号包裹后以 AND 连接到主查询。
	// 组内可使用完整 wrapper 能力：WhereIf / Like / LLike / BetweenIfNotZero / RawWhere 等。
	//
	//   // WHERE (username LIKE '%admin' AND status = 1)  -- status=0 时无 status 条件
	//   .WhereGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
	//       w.LLike(dao.AccountEntity.Username, username).
	//         WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
	//   })
	WhereGroupFn(fn func(IGenWrapper[T])) IGenWrapper[T]

	// OrGroupFn 将 fn 内构建的条件用括号包裹后以 OR 连接到主查询。
	// 组内可使用完整 wrapper 能力：WhereIf / Like / LLike / BetweenIfNotZero / RawWhere 等。
	//
	//   // WHERE status = 1 OR (username LIKE '%admin' AND role = 99)
	//   .Where(dao.AccountEntity.Status.Eq(1)).
	//    OrGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
	//        w.LLike(dao.AccountEntity.Username, username).
	//          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
	//    })
	OrGroupFn(fn func(IGenWrapper[T])) IGenWrapper[T]

	// RawWhere 追加一段原生 SQL 作为 AND 条件，支持占位符 ?。
	//
	//   .RawWhere("deleted_at IS NULL AND org_id = ?", orgID)
	//   => WHERE (deleted_at IS NULL AND org_id = 1)
	//
	//   // 子查询场景
	//   .RawWhere("id IN (SELECT account_id FROM vip WHERE level > ?)", 2)
	//
	// Deprecated: 用 WhereRaw 替代,命名更符合 Go 习惯。两者行为完全一致。
	RawWhere(sql string, args ...any) IGenWrapper[T]

	// RawOrWhere 追加一段原生 SQL 作为 OR 条件。
	//
	//   .Where(dao.AccountEntity.Status.Eq(1)).
	//    RawOrWhere("role = ? AND dept_id = ?", 99, 10)
	//   => WHERE status = 1 OR (role = 99 AND dept_id = 10)
	//
	// Deprecated: 用 OrWhereRaw 替代,命名更符合 Go 习惯。两者行为完全一致。
	RawOrWhere(sql string, args ...any) IGenWrapper[T]

	// RawWhereIf condition 为 true 时才追加原生 SQL AND 条件。
	//
	//   .RawWhereIf(orgID > 0, "org_id = ?", orgID)
	//   => WHERE (org_id = 1)  （orgID=0 时无此条件）
	//
	// Deprecated: 用 WhereRawIf 替代,命名更符合 Go 习惯。两者行为完全一致。
	RawWhereIf(condition bool, sql string, args ...any) IGenWrapper[T]

	// Limit 限制查询行数，适合在 fn 回调内直接控制返回数量。
	//
	//   // Repository 外部无法拿到 DO，通过 fn 在内部限制行数
	//   repo.FindList(ctx, func(g IGenWrapper[dao.IProductBrandEntityDo]) {
	//       g.Where(dao.ProductBrandEntity.Status.Eq(1)).
	//         Limit(5)
	//   })
	Limit(limit int) IGenWrapper[T]

	// Offset 设置查询偏移量，通常与 Limit 配合使用。
	//
	//   g.Limit(10).Offset(20) // 第三页，每页10条
	Offset(offset int) IGenWrapper[T]

	// Select 指定查询字段，适合只需要部分字段或联表时映射别名字段。
	//
	//   g.Select(dao.ProductBrandEntity.ID, dao.ProductBrandEntity.Name)
	//
	//   // 联表时选取别名字段
	//   g.Select("b.id", "b.name", "c.name AS category_name")
	Select(columns ...any) IGenWrapper[T]

	// WhereRaw 追加一段原生 SQL 作为 AND 条件,功能等同 RawWhere(命名更符合 Go 习惯)。
	//
	//   .WhereRaw("(discount_amount IS NOT NULL AND discount_amount != '') " +
	//             "OR (discount_label IS NOT NULL AND discount_label != '')")
	//   .WhereRaw("created_at BETWEEN ? AND ?", startTime, endTime)
	//
	// 空 sql 静默跳过。
	WhereRaw(sql string, args ...any) IGenWrapper[T]

	// WhereRawIf condition 为 true 时才追加原生 SQL AND 条件,语义更紧凑。
	//
	//   .WhereRawIf(req.IsDiscount == 1,
	//       "(discount_amount IS NOT NULL AND discount_amount != '') " +
	//       "OR (discount_label IS NOT NULL AND discount_label != '')").
	//    WhereRawIf(req.IsDiscount == 2,
	//       "(discount_amount IS NULL OR discount_amount = '') " +
	//       "AND (discount_label IS NULL OR discount_label = '')")
	WhereRawIf(condition bool, sql string, args ...any) IGenWrapper[T]

	// OrWhereRaw 追加一段原生 SQL 作为 OR 条件,功能等同 RawOrWhere。
	//
	//   .Where(dao.AccountEntity.Status.Eq(1)).
	//    OrWhereRaw("role = ? AND dept_id = ?", 99, 10)
	//   => WHERE status = 1 OR (role = 99 AND dept_id = 10)
	OrWhereRaw(sql string, args ...any) IGenWrapper[T]

	// Order 追加排序字段(链式顺序生效,先调先排)。
	//
	// 既支持 gorm-gen 类型安全字段(dao.X.Y.Desc()),也支持 RawField 包装的原生 SQL。
	// 多次调用按顺序累积,Apply 时一次性注入到底层 DB。
	//
	//   g.Order(dao.AccountEntity.Status.Asc()).
	//     Order(dao.AccountEntity.CreatedAt.Desc())
	//   => ORDER BY status ASC, created_at DESC
	Order(fields ...field.Expr) IGenWrapper[T]

	// OrderRaw 用原生 SQL 片段追加排序,内部走 RawField。
	//
	// 适合多表 JOIN 字段、SQL 函数排序、CASE WHEN 表达式等场景。
	//
	//   g.OrderRaw("b.id DESC")                                       // 多表别名
	//   g.OrderRaw("CASE WHEN status='vip' THEN 0 ELSE 1 END ASC")     // CASE WHEN
	//   g.OrderRaw("a.priority DESC, b.id DESC")                       // 多字段一次性
	//
	// ⚠️ sql 严禁拼接用户输入(SQL 注入);vars 走 ? 占位符。
	OrderRaw(sql string, vars ...interface{}) IGenWrapper[T]

	// OrderIf 条件性追加排序:cond 为 true 用 truthy,否则用 falsy(可省)。
	//
	//   // bool 决定 asc/desc
	//   g.OrderIf(req.SortDesc,
	//       dao.X.CreatedAt.Desc(),
	//       dao.X.CreatedAt.Asc())
	//
	//   // 只有 truthy,false 时整个跳过
	//   g.OrderIf(req.UrgentOnly, dao.X.Priority.Desc())
	OrderIf(cond bool, truthy field.Expr, falsy ...field.Expr) IGenWrapper[T]

	// OrderTriState 按 0/1/2 三态选择排序方向,贴合前端 sort 字段约定。
	//
	//   - state == 0   → 跳过此字段
	//   - state == 1   → 用 asc(升序)
	//   - state == 2   → 用 desc(降序)
	//   - 其他值       → 跳过
	//
	// 用法对比:
	//
	//   // ❌ 旧写法:每字段两层包裹
	//   if req.AmountSort != 0 {
	//       g.OrderIf(req.AmountSort == 1, dao.X.Amount.Asc(), dao.X.Amount.Desc())
	//   }
	//
	//   // ✅ 新写法:一行一字段,0 自动跳过
	//   g.OrderTriState(req.AmountSort, dao.X.Amount.Asc(), dao.X.Amount.Desc()).
	//     OrderTriState(req.TimeSort,   dao.X.CreatedAt.Asc(), dao.X.CreatedAt.Desc())
	//
	// state 类型用 any 兼容前端可能传 int/int8/string("1"/"2"/"asc"/"desc")等。
	OrderTriState(state any, asc, desc field.Expr) IGenWrapper[T]

	// OrderDefault 仅在此前未设置任何排序时生效(用作默认/兜底)。
	//
	// 调用顺序无关,Apply 时检测到 orders 已被填过则被忽略。
	//
	//   g.OrderTriState(req.AmountSort, ...).
	//     OrderTriState(req.TimeSort, ...).
	//     OrderDefault(query.RawField("b.id DESC"))   // 全 0 时才用
	OrderDefault(fields ...field.Expr) IGenWrapper[T]

	// ── 调试 ──────────────────────────────────────────────────

	// PrintSQL 立即把当前 wrapper 的最终 SQL(含填好的参数值)打印到 stdout,
	// 并继续返回 wrapper 用于链式调用,不影响实际查询执行。
	//
	//   query.Wrap(dao.AccountEntity.WithContext(ctx)).
	//       LLike(dao.AccountEntity.Username, username).
	//       PrintSQL().                                  // ← 打印 SELECT * FROM ...
	//       Apply().Find()
	PrintSQL() IGenWrapper[T]

	// ToSQL 返回当前查询的最终 SQL 字符串(含填好的参数值),不真正执行 SQL。
	// 适合单测断言、日志记录、调试展示等场景。
	//
	// ⚠️ 返回的 SQL 已经把参数填进了占位符,不再提供 SQL 注入保护,仅用于调试。
	//
	//   sql := query.Wrap(dao.AccountEntity.WithContext(ctx)).
	//       LLike(dao.AccountEntity.Username, "admin").
	//       ToSQL()
	ToSQL() string

	// Explain 执行 EXPLAIN <SQL> 拿到数据库的执行计划,扫描到 target。
	//
	// target 通常是 []map[string]any 或自定义 struct slice。
	//
	//   var plan []map[string]any
	//   err := query.Wrap(dao.AccountEntity.WithContext(ctx)).
	//       Where(dao.AccountEntity.Status.Eq(1)).
	//       Explain(&plan)
	Explain(target any) error

	// ── 完成构建 ──────────────────────────────────────────────

	// Apply 结束扩展条件构建，返回已注入所有条件的原生 DO。
	// 之后可继续调用 gorm-gen 原生方法：Order / Limit / Offset / Count / Find 等。
	//
	//   list, err := db.Wrap(dao.AccountEntity.WithContext(ctx)).
	//       LLike(dao.AccountEntity.Username, username).
	//       Apply().
	//       Order(dao.AccountEntity.CreatedAt.Desc()).
	//       Limit(pageSize).Offset((page-1)*pageSize).
	//       Find()
	Apply() T
}

// ================== GenWrapper 实现 ==================

type GenWrapper[T GenDo[T]] struct {
	do            T
	ctx           context.Context // 保存原始 ctx，Apply 时复用
	alias         string          // 表别名，As() 设置后在 Apply 时注入到底层 DB
	limit         *int            // 限制行数，nil 表示不限制
	offset        *int            // 偏移量，nil 表示不设置
	selectCols    []any           // 指定查询字段，nil 表示查询全部
	orders        []field.Expr    // 显式排序字段(Order/OrderRaw/OrderIf/OrderTriState 累积),Apply 时注入
	defaultOrders []field.Expr    // 默认排序字段,仅在没有显式排序时注入
	group         *condGroup
	replaceDB     func(*gorm.DB) T
}

func (w *GenWrapper[T]) addExpr(expr field.Expr, typ condType) {
	w.group.add(&condItem{expr: expr, typ: typ})
}

func (w *GenWrapper[T]) addRaw(sql string, args []any, typ condType) {
	if sql == "" {
		return
	}
	w.group.add(&condItem{rawSQL: "(" + sql + ")", rawArgs: args, typ: typ, isRaw: true})
}

func (w *GenWrapper[T]) addGroup(exprs []field.Expr, typ condType) {
	filtered := make([]field.Expr, 0, len(exprs))
	for _, expr := range exprs {
		if expr != nil {
			filtered = append(filtered, expr)
		}
	}
	if len(filtered) == 0 {
		return
	}
	w.group.add(&condItem{exprs: filtered, typ: typ, isGroup: true})
}

func (w *GenWrapper[T]) addFnGroup(fn func(IGenWrapper[T]), typ condType) {
	if fn == nil {
		return
	}
	sub := &GenWrapper[T]{do: w.do, ctx: w.ctx, group: newCondGroup(), replaceDB: w.replaceDB}
	fn(sub)
	if !sub.group.isEmpty() {
		w.group.add(&condItem{subGroup: sub.group, typ: typ, isFnGroup: true})
	}
}

func (w *GenWrapper[T]) like(col field.Expr, pattern string) {
	if col == nil || pattern == "" {
		return
	}
	// 直接拼 SQL,值走 ? 占位符,避免反射调用 field 类型方法的类型严格匹配问题。
	colName := fmt.Sprint(col.ColumnName())
	w.addExpr(RawField(colName+" LIKE ?", pattern), condAnd)
}

func (w *GenWrapper[T]) As(alias string) IGenWrapper[T] {
	w.alias = alias
	return w
}

func (w *GenWrapper[T]) Like(col field.Expr, val string) IGenWrapper[T] {
	w.like(col, "%"+val+"%")
	return w
}
func (w *GenWrapper[T]) LLike(col field.Expr, val string) IGenWrapper[T] {
	w.like(col, "%"+val)
	return w
}
func (w *GenWrapper[T]) RLike(col field.Expr, val string) IGenWrapper[T] {
	w.like(col, val+"%")
	return w
}

func (w *GenWrapper[T]) BetweenIfNotZero(col field.Expr, min, max any) IGenWrapper[T] {
	if col == nil || isZeroVal(min) || isZeroVal(max) {
		return w
	}
	// 通过反射调用 gorm-gen 的 Between 方法存在类型严格匹配问题:
	// 不同 field 类型(field.Int / field.Int64 / field.Float64 / field.String 等)
	// 的 Between 方法形参类型不同,业务方传 int 时若字段是 Int64 会 panic 或静默失败。
	//
	// 直接用 RawField 拼 SQL,值走 ? 占位符,gorm 内部按 reflect 转换值类型,
	// 业务方完全不需要关心字段的具体 field 子类型。
	//
	// 注意:field.Expr 的 ColumnName() 返回未导出类型 field.sql(本质是 string),
	// 通过 fmt.Sprint 转换为通用 string,效果等同 string(x) 但避免类型导出限制。
	colName := fmt.Sprint(col.ColumnName())
	w.addExpr(RawField(colName+" BETWEEN ? AND ?", min, max), condAnd)
	return w
}

func (w *GenWrapper[T]) Where(exprs ...field.Expr) IGenWrapper[T] {
	for _, expr := range exprs {
		if expr != nil {
			w.addExpr(expr, condAnd)
		}
	}
	return w
}

func (w *GenWrapper[T]) WhereIf(condition bool, exprs ...field.Expr) IGenWrapper[T] {
	if condition {
		return w.Where(exprs...)
	}
	return w
}

func (w *GenWrapper[T]) OrWhereIf(condition bool, exprs ...field.Expr) IGenWrapper[T] {
	if condition {
		for _, expr := range exprs {
			if expr != nil {
				w.addExpr(expr, condOr)
			}
		}
	}
	return w
}

func (w *GenWrapper[T]) WhereGroup(exprs ...field.Expr) IGenWrapper[T] {
	w.addGroup(exprs, condAnd)
	return w
}

func (w *GenWrapper[T]) WhereGroupIf(condition bool, exprs ...field.Expr) IGenWrapper[T] {
	if condition {
		w.addGroup(exprs, condAnd)
	}
	return w
}

func (w *GenWrapper[T]) OrGroup(exprs ...field.Expr) IGenWrapper[T] {
	w.addGroup(exprs, condOr)
	return w
}

func (w *GenWrapper[T]) OrGroupIf(condition bool, exprs ...field.Expr) IGenWrapper[T] {
	if condition {
		w.addGroup(exprs, condOr)
	}
	return w
}

func (w *GenWrapper[T]) WhereGroupFn(fn func(IGenWrapper[T])) IGenWrapper[T] {
	w.addFnGroup(fn, condAnd)
	return w
}

func (w *GenWrapper[T]) OrGroupFn(fn func(IGenWrapper[T])) IGenWrapper[T] {
	w.addFnGroup(fn, condOr)
	return w
}

func (w *GenWrapper[T]) RawWhere(sql string, args ...any) IGenWrapper[T] {
	w.addRaw(sql, args, condAnd)
	return w
}
func (w *GenWrapper[T]) RawOrWhere(sql string, args ...any) IGenWrapper[T] {
	w.addRaw(sql, args, condOr)
	return w
}
func (w *GenWrapper[T]) RawWhereIf(condition bool, sql string, args ...any) IGenWrapper[T] {
	if condition {
		w.addRaw(sql, args, condAnd)
	}
	return w
}

func (w *GenWrapper[T]) Limit(limit int) IGenWrapper[T] {
	w.limit = &limit
	return w
}

func (w *GenWrapper[T]) Offset(offset int) IGenWrapper[T] {
	w.offset = &offset
	return w
}

func (w *GenWrapper[T]) Select(columns ...any) IGenWrapper[T] {
	w.selectCols = columns
	return w
}

// ── Where 系列新命名(推荐) ────────────────────────────────────────────

func (w *GenWrapper[T]) WhereRaw(sql string, args ...any) IGenWrapper[T] {
	w.addRaw(sql, args, condAnd)
	return w
}

func (w *GenWrapper[T]) WhereRawIf(condition bool, sql string, args ...any) IGenWrapper[T] {
	if condition {
		w.addRaw(sql, args, condAnd)
	}
	return w
}

func (w *GenWrapper[T]) OrWhereRaw(sql string, args ...any) IGenWrapper[T] {
	w.addRaw(sql, args, condOr)
	return w
}

// ── Order 系列 ────────────────────────────────────────────────────────

func (w *GenWrapper[T]) Order(fields ...field.Expr) IGenWrapper[T] {
	for _, f := range fields {
		if f != nil {
			w.orders = append(w.orders, f)
		}
	}
	return w
}

func (w *GenWrapper[T]) OrderRaw(sql string, vars ...interface{}) IGenWrapper[T] {
	if sql == "" {
		return w
	}
	w.orders = append(w.orders, RawField(sql, vars...))
	return w
}

func (w *GenWrapper[T]) OrderIf(cond bool, truthy field.Expr, falsy ...field.Expr) IGenWrapper[T] {
	if cond {
		if truthy != nil {
			w.orders = append(w.orders, truthy)
		}
	} else {
		for _, f := range falsy {
			if f != nil {
				w.orders = append(w.orders, f)
			}
		}
	}
	return w
}

func (w *GenWrapper[T]) OrderTriState(state any, asc, desc field.Expr) IGenWrapper[T] {
	switch normalizeSortState(state) {
	case 1:
		if asc != nil {
			w.orders = append(w.orders, asc)
		}
	case 2:
		if desc != nil {
			w.orders = append(w.orders, desc)
		}
	}
	return w
}

func (w *GenWrapper[T]) OrderDefault(fields ...field.Expr) IGenWrapper[T] {
	if len(w.orders) > 0 || len(w.defaultOrders) > 0 {
		return w
	}
	for _, f := range fields {
		if f != nil {
			w.defaultOrders = append(w.defaultOrders, f)
		}
	}
	return w
}

// buildDB 把所有累积的条件、字段选择、排序、分页应用到 db,返回新的 *gorm.DB。
// Apply / ToSQL / Explain 共用此方法,保持行为一致。
func (w *GenWrapper[T]) buildDB(db *gorm.DB) *gorm.DB {
	// 注入表别名:gorm 支持 "table_name alias" 格式
	if w.alias != "" {
		db = db.Table(db.Statement.Table + " " + w.alias)
	}
	if !w.group.isEmpty() {
		db = applyCondGroup(db, w.group)
	}
	if len(w.selectCols) > 0 {
		db = db.Select(w.selectCols[0], w.selectCols[1:]...)
	}
	// 排序:显式排序优先;没有显式排序时才使用 OrderDefault 的默认排序。
	// 按调用顺序写入 db,gorm 内部会拼成 ORDER BY a, b, c
	for _, o := range w.effectiveOrders() {
		orderSQL := strings.TrimSpace(buildOrderSQL(db, o))
		if orderSQL != "" {
			db = db.Order(orderSQL)
		}
	}
	if w.limit != nil {
		db = db.Limit(*w.limit)
	}
	if w.offset != nil {
		db = db.Offset(*w.offset)
	}
	return db
}

func buildOrderSQL(db *gorm.DB, order field.Expr) string {
	stmt := &gorm.Statement{
		DB:     db.Statement.DB,
		Table:  db.Statement.Table,
		Schema: db.Statement.Schema,
	}
	order.Build(stmt)
	return db.Dialector.Explain(stmt.SQL.String(), stmt.Vars...)
}

func (w *GenWrapper[T]) effectiveOrders() []field.Expr {
	if len(w.orders) > 0 {
		return w.orders
	}
	return w.defaultOrders
}

func (w *GenWrapper[T]) Apply() T {
	db := w.buildDB(w.do.UnderlyingDB())
	newDO := w.do.WithContext(w.ctx)
	newDO.ReplaceDB(db)
	return newDO
}

// ──── 调试方法 ──────────────────────────────────────────────────────────────

func (w *GenWrapper[T]) PrintSQL() IGenWrapper[T] {
	fmt.Println(w.ToSQL())
	return w
}

func (w *GenWrapper[T]) ToSQL() string {
	return w.do.UnderlyingDB().ToSQL(func(tx *gorm.DB) *gorm.DB {
		// 必须调用一个终结方法(Find/First/Count 等),否则 gorm 不会生成完整 SQL。
		// 这里用通用的 Find 触发 SELECT;Update/Delete 的调试场景请用 gorm 原生 ToSQL。
		return w.buildDB(tx).Find(&struct{}{})
	})
}

func (w *GenWrapper[T]) Explain(target any) error {
	sql := w.ToSQL()
	if sql == "" {
		return fmt.Errorf("ToSQL returned empty,无法 EXPLAIN")
	}
	return w.do.UnderlyingDB().Raw("EXPLAIN " + sql).Scan(target).Error
}

// ================== SQL 构建（内部） ==================

func applyCondGroup(db *gorm.DB, group *condGroup) *gorm.DB {
	for _, item := range group.conds {
		switch {
		case item.isFnGroup:
			subDB := applyCondGroup(db.Session(&gorm.Session{NewDB: true}), item.subGroup)
			if item.typ == condOr {
				db = db.Or(subDB)
			} else {
				db = db.Where(subDB)
			}
		case item.isGroup:
			subDB := db.Session(&gorm.Session{NewDB: true})
			for _, expr := range item.exprs {
				subDB = subDB.Where(expr)
			}
			if item.typ == condOr {
				db = db.Or(subDB)
			} else {
				db = db.Where(subDB)
			}
		case item.isRaw:
			if item.typ == condOr {
				db = db.Or(item.rawSQL, item.rawArgs...)
			} else {
				db = db.Where(item.rawSQL, item.rawArgs...)
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

// ════════════════════════════════════════════════════════════════════════════
//  工具函数
// ════════════════════════════════════════════════════════════════════════════

// RawField 创建一个原始 SQL 表达式,既可用作 Select / Order 字段,
// 也可作为 Where 条件(因为 field.Expr 已实现 gen.Condition 接口)。
//
// vars 走 gorm 的标准 ? 占位符机制,会被参数化,安全防 SQL 注入。
//
// 示例:
//
//	// 1) 作为 Order 字段
//	query.Query().Order(query.RawField("created_at DESC")).Build()
//
//	// 2) 作为 Where 条件(纯字符串)
//	query.Query().Where(query.RawField(
//	    "(discount_amount IS NOT NULL AND discount_amount != '') " +
//	    "OR (discount_label IS NOT NULL AND discount_label != '')",
//	)).Build()
//
//	// 3) 带参数化占位符的 Where 条件
//	query.Query().Where(query.RawField(
//	    "created_at BETWEEN ? AND ?", startTime, endTime,
//	)).Build()
//
//	// 4) Select 字段(SQL 函数 / 子查询)
//	query.Query().Select(query.RawField("COUNT(DISTINCT user_id) AS uv")).Build()
//
// ⚠️ 安全提示:sql 参数本身严禁拼接用户输入——只允许传"固定的 SQL 片段",
// 用户输入应该通过 vars 参数传入并由 ? 占位符消费,gorm 会自动转义。
//
// 反例(SQL 注入):
//
//	query.RawField("user_name LIKE '%" + userInput + "%'")   // ❌ 危险
//
// 正例:
//
//	query.RawField("user_name LIKE ?", "%"+userInput+"%")    // ✅ 安全
func RawField(sql string, vars ...interface{}) field.Expr {
	return field.NewUnsafeFieldRaw(sql, vars...)
}
