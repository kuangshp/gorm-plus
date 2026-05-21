package gormplus

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// ════════════════════════════════════════════════════════════════════════════
//  通用事务工具
// ════════════════════════════════════════════════════════════════════════════
//
// 为什么 gorm-plus 不直接提供 Transaction(fn func(tx *dao.Query) error):
// 因为 dao.Query 是用户项目里 gorm-gen 自动生成的类型,gorm-plus 作为框架包
// 没法 import 用户项目,会产生反向依赖。本文件提供两套方案:
//
//   1. Transaction      → 通用版本,fn 拿到 *gorm.DB,需要在内部自行 dao.Use(tx)
//   2. TransactionAs    → 泛型版本,fn 直接拿到用户期望的类型(如 *dao.Query)
//
// gorm-gen 自动生成的 Use 函数签名固定为 func Use(db *gorm.DB) *Query,
// TransactionAs 利用这一点用泛型推断 Q 类型,业务侧调用最优雅。

// Transaction 执行 gorm 事务,fn 返回 nil 时提交,返回 error 时回滚。
//
// 适合不需要 dao.Query 类型的简单事务场景:
//   - 用 gorm 原生 *tx 操作多张表
//   - 在 fn 内部按需用 dao.Use(tx) 构造类型安全的 Query
//
// 示例:
//
//	err := gormplus.Transaction(db, func(tx *gorm.DB) error {
//	    // 用 gorm 原生
//	    if err := tx.Create(&user).Error; err != nil { return err }
//
//	    // 在事务内用 dao(类型安全)
//	    q := dao.Use(tx)
//	    if _, err := q.User.WithContext(ctx).
//	        Where(q.User.ID.Eq(user.ID)).
//	        Update(q.User.Status, 1); err != nil {
//	        return err
//	    }
//	    return nil
//	})
//
// opts 可选,传递隔离级别等参数(同 gorm.DB.Transaction)。
func Transaction(db *gorm.DB, fn func(tx *gorm.DB) error, opts ...*sql.TxOptions) error {
	return db.Transaction(fn, opts...)
}

// TransactionAs 执行 gorm 事务,fn 直接拿到指定类型的查询对象(如 *dao.Query)。
//
// Q 类型由 useFn 决定:传入 dao.Use 时 Q 自动推断为 *dao.Query。
// useFn 是 gorm-gen 自动生成的 Use 函数,签名固定为 func(*gorm.DB) Q,
// 框架在事务内部用 tx 调一次 useFn 把 Q 构造好喂给 fn。
//
// 示例:
//
//	err := gormplus.TransactionAs(db, dao.Use, func(tx *dao.Query) error {
//	    if err := tx.User.WithContext(ctx).Create(&user); err != nil {
//	        return err
//	    }
//	    if _, err := tx.Profile.WithContext(ctx).
//	        Where(tx.Profile.UserID.Eq(user.ID)).
//	        Update(tx.Profile.Status, 1); err != nil {
//	        return err
//	    }
//	    return nil
//	})
//
// 对比 Transaction:
//   - Transaction:    fn(tx *gorm.DB)    → fn 内手动 dao.Use(tx)
//   - TransactionAs:  fn(tx *dao.Query)  → 框架已经帮你 Use 好,直接用
//
// opts 可选,传递隔离级别等参数。
func TransactionAs[Q any](
	db *gorm.DB,
	useFn func(*gorm.DB) Q,
	fn func(tx Q) error,
	opts ...*sql.TxOptions,
) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return fn(useFn(tx))
	}, opts...)
}

// TransactionCtx 是 Transaction 的带 context 版本:tx 自带 ctx,免去业务侧手动
// 在 dao 调用上挂 WithContext。
//
// 注意:gorm 的 Transaction 方法本身不接受 ctx,这里通过 db.WithContext(ctx)
// 注入,后续在 tx 上发起的所有查询都会自动携带此 ctx。
//
// 示例:
//
//	err := gormplus.TransactionCtx(ctx, db, func(tx *gorm.DB) error {
//	    // tx 已经带了 ctx,traceID / 租户信息自动透传
//	    return tx.Create(&user).Error
//	})
func TransactionCtx(
	ctx context.Context,
	db *gorm.DB,
	fn func(tx *gorm.DB) error,
	opts ...*sql.TxOptions,
) error {
	return db.WithContext(ctx).Transaction(fn, opts...)
}

// TransactionAsCtx 是 TransactionAs 的带 context 版本。
//
// 示例:
//
//	err := gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
//	    return tx.User.WithContext(ctx).Create(&user)
//	})
//
// 注意:此版本会把 ctx 注入到 tx 底层的 *gorm.DB,但 dao.Query 的查询链
// 还是需要显式 .WithContext(ctx) 才能携带 ctx(这是 gorm-gen 的约定)。
func TransactionAsCtx[Q any](
	ctx context.Context,
	db *gorm.DB,
	useFn func(*gorm.DB) Q,
	fn func(tx Q) error,
	opts ...*sql.TxOptions,
) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(useFn(tx))
	}, opts...)
}
