package dal

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

////////////////////////////////////////////////////////////////////////////////
////////////////////////////// Transaction /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func WithTx(
	ctx context.Context,
	fn func(tx *gorm.DB) error,
) error {
	return resolve(ctx).db(ctx).Transaction(fn)
}

// TxQuery 在事务中查询多条记录（位置参数 ?）
//
// SQL 示例：
//
//	-- rawsql/stock/find_for_update.sql
//	SELECT id, product_id, quantity
//	FROM   stock
//	WHERE  product_id = ?
//	  AND  deleted_at IS NULL
//	FOR UPDATE
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    stocks, err := dal.TxQuery[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
//	    if err != nil {
//	        return err
//	    }
//	    // ... 处理库存逻辑
//	    return nil
//	})
func TxQuery[T any](
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) ([]T, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result []T

	err = tx.WithContext(ctx).
		Raw(sqlText, args...).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.TxQuery [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// TxQueryOne 在事务中查询单条记录（位置参数 ?）
//
// 返回值语义：
//   - (*T, nil)    — 查到记录
//   - (nil, nil)   — 记录不存在（debug 模式打印 WARN）
//   - (nil, error) — 执行出错
//
// SQL 示例：
//
//	-- rawsql/account/find_for_update.sql
//	SELECT id, username, balance
//	FROM   account
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//	FOR UPDATE
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    account, err := dal.TxQueryOne[AccountVO](ctx, tx, "account/find_for_update.sql", accountID)
//	    if err != nil {
//	        return err
//	    }
//	    if account == nil {
//	        return errors.New("账号不存在")
//	    }
//	    // ... 处理余额逻辑
//	    return nil
//	})
func TxQueryOne[T any](
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) (*T, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result T

	txResult := tx.WithContext(ctx).
		Raw(sqlText, args...).
		Limit(1).
		Scan(&result)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, txResult.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, txResult.Error)

	if txResult.Error != nil {
		return nil, fmt.Errorf("dal.TxQueryOne [%s]: %w", sqlFile, txResult.Error)
	}

	if txResult.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
		return nil, nil
	}

	return &result, nil
}

// TxQueryNamed 在事务中命名参数查询多条记录（命名参数 @name）
//
// SQL 示例：
//
//	-- rawsql/order/list_by_account.sql
//	SELECT id, order_no, amount, status
//	FROM   `order`
//	WHERE  account_id = @account_id
//	  AND  status     = @status
//	  AND  deleted_at IS NULL
//	FOR UPDATE
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    orders, err := dal.TxQueryNamed[OrderVO](
//	        ctx, tx,
//	        "order/list_by_account.sql",
//	        map[string]any{"account_id": 123, "status": 1},
//	    )
//	    if err != nil {
//	        return err
//	    }
//	    // ... 处理订单逻辑
//	    return nil
//	})
func TxQueryNamed[T any](
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	params map[string]any,
) ([]T, error) {
	d := resolve(ctx)
	start := time.Now()
	args := []any{params}
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result []T

	err = tx.WithContext(ctx).
		Raw(sqlText, params).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.TxQueryNamed [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// TxCount 在事务中查询数量
//
// SQL 示例：
//
//	-- rawsql/order/count_by_account.sql
//	SELECT COUNT(*)
//	FROM   `order`
//	WHERE  account_id = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    total, err := dal.TxCount(ctx, tx, "order/count_by_account.sql", accountID)
//	    if err != nil {
//	        return err
//	    }
//	    log.Println("订单总数:", total)
//	    return nil
//	})
func TxCount(
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) (int64, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return 0, err
	}

	var total int64

	err = tx.WithContext(ctx).
		Raw(sqlText, args...).
		Scan(&total).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return 0, fmt.Errorf("dal.TxCount [%s]: %w", sqlFile, err)
	}

	return total, nil
}

// TxExec 在事务中执行 SQL（INSERT / UPDATE / DELETE）
//
// SQL 示例：
//
//	-- rawsql/stock/deduct.sql
//	UPDATE stock
//	SET    quantity   = quantity - ?,
//	       updated_at = NOW()
//	WHERE  product_id = ?
//	  AND  quantity   >= ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    return dal.TxExec(
//	        ctx, tx,
//	        "stock/deduct.sql",
//	        qty,       // quantity - ?
//	        productID, // product_id = ?
//	        qty,       // quantity >= ?（防超卖）
//	    )
//	})
func TxExec(
	ctx context.Context,
	tx *gorm.DB,
	sqlFile string,
	args ...any,
) error {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return err
	}

	err = tx.WithContext(ctx).
		Exec(sqlText, args...).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return fmt.Errorf("dal.TxExec [%s]: %w", sqlFile, err)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Must //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// MustExec 执行失败直接 panic（慎用，仅适合初始化/启动阶段）
//
// SQL 示例：
//
//	-- rawsql/schema/create_account.sql
//	CREATE TABLE IF NOT EXISTS account (
//	    id            BIGINT        NOT NULL AUTO_INCREMENT PRIMARY KEY,
//	    username      VARCHAR(64)   NOT NULL UNIQUE,
//	    password_hash VARCHAR(128)  NOT NULL,
//	    status        TINYINT       NOT NULL DEFAULT 1,
//	    created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
//	    updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
//	    deleted_at    DATETIME      NULL
//	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
//
// Go 示例：
//
