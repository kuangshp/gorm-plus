package gormplus

import (
	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/gen/field"
)

type IGenWrapper[D query.GenDo[D]] = query.IGenWrapper[D]

// GenWrap 将 gorm-gen 生成的 DO 包裹为 IGenWrapper，开启扩展条件链式构建。
// 调用 Apply() 后返回原生 DO，可继续使用所有 gorm-gen 原生方法。
//
// 使用示例：
//
//	// 基础查询
//	list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    LLike(dao.AccountEntity.Username, username).
//	    Where(dao.AccountEntity.TenantID.Eq(tenantID)).
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Order(dao.AccountEntity.CreatedAt.Desc()).
//	    Limit(pageSize).Offset((page-1)*pageSize).
//	    Find()
//
//	// OR 可选条件：前端未传手机号/邮箱时自动跳过
//	list, err := gormplus.GenWrap(dao.SysAccountEntity.WithContext(ctx)).
//	    Where(dao.SysAccountEntity.Username.Eq(req.Username)).
//	    OrWhereIf(req.Mobile != "", dao.SysAccountEntity.Mobile.Eq(req.Mobile)).
//	    OrWhereIf(req.Email != "", dao.SysAccountEntity.Email.Eq(req.Email)).
//	    Apply().
//	    Find()
//
//	// 联表查询（别名 + 原生 SQL 条件）
//	list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    As("a").
//	    RawWhere("a.username LIKE ?", "%"+username+"%").
//	    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
//	    Apply().
//	    Select(dao.AccountEntity.ID, dao.AccountEntity.Username).
//	    Find()
//
//	// AND 简单分组：WHERE (status = 1 AND role = 2)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroup(dao.AccountEntity.Status.Eq(1), dao.AccountEntity.Role.Eq(2)).
//	    Apply().Find()
//
//	// AND 可选分组：keyword 为空时整组跳过
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroupIf(req.Keyword != "",
//	        dao.AccountEntity.Username.Like("%"+req.Keyword+"%"),
//	        dao.AccountEntity.Mobile.Like("%"+req.Keyword+"%"),
//	    ).
//	    Apply().Find()
//
//	// AND 接入的 OR 可选分组：WHERE status = 1 AND (username LIKE ? OR mobile LIKE ?)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    Where(dao.AccountEntity.Status.Eq(1)).
//	    WhereOrGroupIf(req.Keyword != "",
//	        dao.AccountEntity.Username.Like("%"+req.Keyword+"%"),
//	        dao.AccountEntity.Mobile.Like("%"+req.Keyword+"%"),
//	    ).
//	    Apply().Find()
//
//	// OR 可选分组：keyword 为空时整组跳过
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    Where(dao.AccountEntity.Status.Eq(1)).
//	    OrGroupIf(req.Keyword != "",
//	        dao.AccountEntity.Username.Like("%"+req.Keyword+"%"),
//	        dao.AccountEntity.Mobile.Like("%"+req.Keyword+"%"),
//	    ).
//	    Apply().Find()
//
//	// AND 函数分组（组内可用 WhereIf / Like 等）
//	// => WHERE (username LIKE '%admin' AND status = 1)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    WhereGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
//	    }).Apply().Find()
//
//	// OR 函数分组：WHERE status = 1 OR (username LIKE '%admin' AND role = 99)
//	gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
//	    Where(dao.AccountEntity.Status.Eq(1)).
//	    OrGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
//	        w.LLike(dao.AccountEntity.Username, username).
//	          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
//	    }).Apply().Find()
func GenWrap[D query.GenDo[D]](do D) query.IGenWrapper[D] {
	return query.Wrap(do)
}

// RawField 原生字段，用于 SELECT、WHERE 等原生 SQL 拼接。
func RawField(rawSql string, args ...interface{}) field.Expr {
	return query.RawField(rawSql, args...)
}
