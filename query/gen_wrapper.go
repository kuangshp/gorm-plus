// Package db 提供基于 gorm-gen 的查询条件构建扩展工具。
//
// # 设计原则
//
// 本包只封装 gorm-gen 原生 DO 不支持的能力：
//   - 模糊查询自动拼 %（Like / LLike / RLike）
//   - 可选条件（WhereIf / BetweenIfNotZero）
//   - 简单分组（WhereGroup / OrGroup）：传入多个 field.Expr，组内 AND 连接，自动加括号
//   - 函数分组（WhereGroupFn / OrGroupFn）：传入函数，组内可使用完整 wrapper 能力（WhereIf / Like 等）
//   - 原生 SQL 条件（RawWhere / RawOrWhere / RawWhereIf）
//
// gorm-gen 原生已支持的能力（Eq/Gt/Lt/Order/Limit/Count/Find 等）请在 Apply() 后直接调用。
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
//	    WhereIf(true, dao.AccountEntity.Status.Eq(1)).
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
//	    WhereIf(true, dao.AccountEntity.OrgID.Eq(orgID)).
//	    OrGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
//	    }).Apply().Find()
package query

import (
	"context"
	"reflect"

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
	return &GenWrapper[T]{
		do:    do,
		group: newCondGroup(),
		replaceDB: func(newDB *gorm.DB) T {
			newDO := do.WithContext(context.Background())
			newDO.ReplaceDB(newDB)
			return newDO
		},
	}
}

// ================== IGenWrapper 接口 ==================

// IGenWrapper 扩展条件构建器接口，只包含 gorm-gen 原生不支持的能力。
// 所有方法均支持链式调用，最终通过 Apply() 返回原生 DO。
type IGenWrapper[T GenDo[T]] interface {
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

	// OrGroup 将多个 field.Expr 用括号包裹后以 OR 连接到主查询，组内条件以 AND 连接。
	// 需要组内 WhereIf / Like 等能力时请使用 OrGroupFn。
	//
	//   .WhereIf(true, dao.AccountEntity.Status.Eq(1)).
	//    OrGroup(
	//        dao.AccountEntity.Role.Eq(99),
	//        dao.AccountEntity.DeptID.Eq(10),
	//    )
	//   => WHERE status = 1 OR (role = 99 AND dept_id = 10)
	OrGroup(exprs ...field.Expr) IGenWrapper[T]

	// WhereGroupFn 将 fn 内构建的条件用括号包裹后以 AND 连接到主查询。
	// 组内可使用完整 wrapper 能力：WhereIf / Like / LLike / BetweenIfNotZero / RawWhere 等。
	//
	//   // WHERE (username LIKE '%admin' AND status = 1)  -- status=0 时无 status 条件
	//   .WhereGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
	//       w.LLike(dao.AccountEntity.Username, username).
	//         WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
	//   })
	//
	//   // 配合主查询：WHERE org_id = 1 AND (username LIKE '%admin' AND role = 2)
	//   .WhereIf(true, dao.AccountEntity.OrgID.Eq(orgID)).
	//    WhereGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
	//        w.LLike(dao.AccountEntity.Username, username).
	//          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
	//    })
	WhereGroupFn(fn func(IGenWrapper[T])) IGenWrapper[T]

	// OrGroupFn 将 fn 内构建的条件用括号包裹后以 OR 连接到主查询。
	// 组内可使用完整 wrapper 能力：WhereIf / Like / LLike / BetweenIfNotZero / RawWhere 等。
	//
	//   // WHERE status = 1 OR (username LIKE '%admin' AND role = 99)
	//   .WhereIf(true, dao.AccountEntity.Status.Eq(1)).
	//    OrGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
	//        w.LLike(dao.AccountEntity.Username, username).
	//          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
	//    })
	//
	//   // 组内还可以继续嵌套 RawWhere
	//   .OrGroupFn(func(w db.IGenWrapper[dao.IAccountEntityDo]) {
	//       w.WhereIf(orgID != 0, dao.AccountEntity.OrgID.Eq(orgID)).
	//         RawWhere("deleted_at IS NULL")
	//   })
	OrGroupFn(fn func(IGenWrapper[T])) IGenWrapper[T]

	// RawWhere 追加一段原生 SQL 作为 AND 条件，支持占位符 ?。
	//
	//   .RawWhere("deleted_at IS NULL AND org_id = ?", orgID)
	//   => WHERE (deleted_at IS NULL AND org_id = 1)
	//
	//   // 子查询场景
	//   .RawWhere("id IN (SELECT account_id FROM vip WHERE level > ?)", 2)
	//   => WHERE (id IN (SELECT account_id FROM vip WHERE level > 2))
	RawWhere(sql string, args ...any) IGenWrapper[T]

	// RawOrWhere 追加一段原生 SQL 作为 OR 条件。
	//
	//   .WhereIf(true, dao.AccountEntity.Status.Eq(1)).
	//    RawOrWhere("role = ? AND dept_id = ?", 99, 10)
	//   => WHERE status = 1 OR (role = 99 AND dept_id = 10)
	RawOrWhere(sql string, args ...any) IGenWrapper[T]

	// RawWhereIf condition 为 true 时才追加原生 SQL AND 条件。
	//
	//   .RawWhereIf(orgID > 0, "org_id = ?", orgID)
	//   => WHERE (org_id = 1)  （orgID=0 时无此条件）
	RawWhereIf(condition bool, sql string, args ...any) IGenWrapper[T]

	// Apply 结束扩展条件构建，返回已注入所有条件的原生 DO。
	// 之后可继续调用 gorm-gen 原生方法：Order / Limit / Offset / Count / Find 等。
	//
	//   list, err := db.Wrap(dao.AccountEntity.WithContext(ctx)).
	//       LLike(dao.AccountEntity.Username, username).
	//       Apply().
	//       Order(dao.AccountEntity.CreatedAt.Desc()).
	//       Limit(pageSize).Offset((page-1)*pageSize).
	//       Find()
	//
	//   total, err := db.Wrap(dao.AccountEntity.WithContext(ctx)).
	//       LLike(dao.AccountEntity.Username, username).
	//       Apply().
	//       Count()
	Apply() T
}

// ================== GenWrapper 实现 ==================

type GenWrapper[T GenDo[T]] struct {
	do        T
	group     *condGroup
	replaceDB func(*gorm.DB) T
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
	if len(exprs) == 0 {
		return
	}
	w.group.add(&condItem{exprs: exprs, typ: typ, isGroup: true})
}

func (w *GenWrapper[T]) addFnGroup(fn func(IGenWrapper[T]), typ condType) {
	sub := &GenWrapper[T]{do: w.do, group: newCondGroup(), replaceDB: w.replaceDB}
	fn(sub)
	if !sub.group.isEmpty() {
		w.group.add(&condItem{subGroup: sub.group, typ: typ, isFnGroup: true})
	}
}

func (w *GenWrapper[T]) like(col field.Expr, pattern string) {
	if pattern != "" {
		if expr, ok := callFieldMethod(col, "Like", pattern).(field.Expr); ok {
			w.addExpr(expr, condAnd)
		}
	}
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
	if !isZeroVal(min) && !isZeroVal(max) {
		if expr, ok := callFieldMethod(col, "Between", min, max).(field.Expr); ok {
			w.addExpr(expr, condAnd)
		}
	}
	return w
}

func (w *GenWrapper[T]) WhereIf(condition bool, exprs ...field.Expr) IGenWrapper[T] {
	if condition {
		for _, expr := range exprs {
			w.addExpr(expr, condAnd)
		}
	}
	return w
}

func (w *GenWrapper[T]) WhereGroup(exprs ...field.Expr) IGenWrapper[T] {
	w.addGroup(exprs, condAnd)
	return w
}

func (w *GenWrapper[T]) OrGroup(exprs ...field.Expr) IGenWrapper[T] {
	w.addGroup(exprs, condOr)
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

func (w *GenWrapper[T]) Apply() T {
	if w.group.isEmpty() {
		return w.do
	}
	return w.replaceDB(applyCondGroup(w.do.UnderlyingDB(), w.group))
}

// ================== SQL 构建（内部） ==================

func applyCondGroup(db *gorm.DB, group *condGroup) *gorm.DB {
	for _, item := range group.conds {
		switch {
		case item.isFnGroup:
			// 函数分组：递归构建子组，加括号后 AND/OR 连接
			subDB := applyCondGroup(db.Session(&gorm.Session{NewDB: true}), item.subGroup)
			if item.typ == condOr {
				db = db.Or(subDB)
			} else {
				db = db.Where(subDB)
			}
		case item.isGroup:
			// 简单分组：多个 expr 逐个 AND 后加括号
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
			// 原生 SQL
			if item.typ == condOr {
				db = db.Or(item.rawSQL, item.rawArgs...)
			} else {
				db = db.Where(item.rawSQL, item.rawArgs...)
			}
		default:
			// 普通 field.Expr
			if item.typ == condOr {
				db = db.Or(item.expr)
			} else {
				db = db.Where(item.expr)
			}
		}
	}
	return db
}

// ================== 工具函数 ==================

// callFieldMethod 通过反射调用 gorm-gen 生成的列字段方法（如 Like / Between）。
// gorm-gen 为每个列生成了对应的类型安全方法，但 field.Expr 接口层面无法静态调用，只能反射。
func callFieldMethod(col field.Expr, method string, args ...any) any {
	v := reflect.ValueOf(col)
	m := v.MethodByName(method)
	if !m.IsValid() {
		return nil
	}
	in := make([]reflect.Value, len(args))
	for i, arg := range args {
		in[i] = reflect.ValueOf(arg)
	}
	res := m.Call(in)
	if len(res) == 0 {
		return nil
	}
	return res[0].Interface()
}
