package dal

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Query //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Query //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// Query 查询多条记录（位置参数 ?）
//
// SQL 示例：
//
//	-- rawsql/account/list.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//	ORDER BY created_at DESC
//	LIMIT  ?
//	OFFSET ?
//
// Go 示例：
//
//	rows, err := dal.Query[AccountVO](
//	    ctx,
//	    "account/list.sql",
//	    1,  // status  = ?
//	    10, // LIMIT   = ?
//	    0,  // OFFSET  = ?
//	)
func Query[T any](
	ctx context.Context,
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

	err = d.db(ctx).
		Raw(sqlText, args...).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.Query [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// QueryOne 查询单条记录（位置参数 ?）
//
// 返回值语义：
//   - (*T, nil)    — 查到记录
//   - (nil, nil)   — 记录不存在（debug 模式打印 WARN）
//   - (nil, error) — 执行出错
//
// SQL 示例：
//
//	-- rawsql/account/find_by_id.sql
//	SELECT id, username, email, status
//	FROM   account
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//	LIMIT 1
//
// Go 示例：
//
//	account, err := dal.QueryOne[AccountVO](ctx, "account/find_by_id.sql", 123)
//	if err != nil {
//	    return err
//	}
//	if account == nil {
//	    return errors.New("账号不存在")
//	}
func QueryOne[T any](
	ctx context.Context,
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

	tx := d.db(ctx).
		Raw(sqlText, args...).
		Limit(1).
		Scan(&result)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, tx.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, tx.Error)

	if tx.Error != nil {
		return nil, fmt.Errorf("dal.QueryOne [%s]: %w", sqlFile, tx.Error)
	}

	// RowsAffected == 0 表示没有匹配的行
	if tx.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
		return nil, nil
	}

	return &result, nil
}

// QueryNamed 命名参数查询多条记录（命名参数 @name）
//
// SQL 使用 @name 作为占位符，通过 map 传参，与顺序无关。
// 适合参数较多、顺序容易混淆的场景。
//
// SQL 示例：
//
//	-- rawsql/account/search.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  deleted_at IS NULL
//	  AND  (@username  = ''  OR username   LIKE CONCAT('%', @username, '%'))
//	  AND  (@status   = -1  OR status    = @status)
//	  AND  (@start_at = ''  OR created_at >= @start_at)
//	  AND  (@end_at   = ''  OR created_at <= @end_at)
//	ORDER BY created_at DESC
//	LIMIT  @limit
//	OFFSET @offset
//
// Go 示例：
//
//	rows, err := dal.QueryNamed[AccountVO](
//	    ctx,
//	    "account/search.sql",
//	    map[string]any{
//	        "username": "张",
//	        "status":   1,
//	        "start_at": "2024-01-01",
//	        "end_at":   "2024-12-31",
//	        "limit":    10,
//	        "offset":   0,
//	    },
//	)
func QueryNamed[T any](
	ctx context.Context,
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

	// 命名参数必须直接传 map，不能通过 args... 展开，否则 @name 不会被替换
	err = d.db(ctx).
		Raw(sqlText, params).
		Scan(&result).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return nil, fmt.Errorf("dal.QueryNamed [%s]: %w", sqlFile, err)
	}

	if len(result) == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return result, nil
}

// QueryOneNamed 命名参数查询单条记录（命名参数 @name）
//
// 返回值语义：
//   - (*T, nil)    — 查到记录
//   - (nil, nil)   — 记录不存在（debug 模式打印 WARN）
//   - (nil, error) — 执行出错
//
// SQL 示例：
//
//	-- rawsql/account/find_by_username.sql
//	SELECT id, username, password_hash, status
//	FROM   account
//	WHERE  username   = @username
//	  AND  deleted_at IS NULL
//	LIMIT 1
//
// Go 示例：
//
//	account, err := dal.QueryOneNamed[AccountVO](
//	    ctx,
//	    "account/find_by_username.sql",
//	    map[string]any{"username": "admin"},
//	)
//	if err != nil {
//	    return err
//	}
//	if account == nil {
//	    return errors.New("用户名不存在")
//	}
func QueryOneNamed[T any](
	ctx context.Context,
	sqlFile string,
	params map[string]any,
) (*T, error) {
	d := resolve(ctx)
	start := time.Now()
	args := []any{params}
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	var result T

	tx := d.db(ctx).
		Raw(sqlText, params).
		Limit(1).
		Scan(&result)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, tx.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, tx.Error)

	if tx.Error != nil {
		return nil, fmt.Errorf("dal.QueryOneNamed [%s]: %w", sqlFile, tx.Error)
	}

	if tx.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
		return nil, nil
	}

	return &result, nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Exec //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// ExecResult 执行结果
type ExecResult struct {
	RowsAffected int64 `json:"rowsAffected"`
}

// Exec 执行 SQL，不关心影响行数（INSERT / UPDATE / DELETE）
//
// SQL 示例：
//
//	-- rawsql/account/disable.sql
//	UPDATE account
//	SET    status = 0, updated_at = NOW()
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	err := dal.Exec(ctx, "account/disable.sql", 123)
func Exec(
	ctx context.Context,
	sqlFile string,
	args ...any,
) error {
	_, err := ExecAffected(ctx, sqlFile, args...)
	return err
}

// ExecAffected 执行 SQL 并返回影响行数
//
// debug 模式下，影响行数为 0 时打印 WARN。
//
// SQL 示例：
//
//	-- rawsql/account/update_status.sql
//	UPDATE account
//	SET    status = ?, updated_at = NOW()
//	WHERE  id         = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	result, err := dal.ExecAffected(ctx, "account/update_status.sql", 0, 123)
//	if err != nil {
//	    return err
//	}
//	if result.RowsAffected == 0 {
//	    return errors.New("记录不存在或已被删除")
//	}
func ExecAffected(
	ctx context.Context,
	sqlFile string,
	args ...any,
) (*ExecResult, error) {
	d := resolve(ctx)
	start := time.Now()
	runBeforeHooks(d, ctx, sqlFile, args)

	sqlText, err := d.loader.Load(sqlFile)
	if err != nil {
		return nil, err
	}

	tx := d.db(ctx).Exec(sqlText, args...)

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, tx.Error)
	runAfterHooks(d, ctx, sqlFile, args, cost, tx.Error)

	if tx.Error != nil {
		return nil, fmt.Errorf("dal.ExecAffected [%s]: %w", sqlFile, tx.Error)
	}

	if tx.RowsAffected == 0 {
		debugWarnEmpty(d, sqlFile)
	}

	return &ExecResult{RowsAffected: tx.RowsAffected}, nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Count /////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// Count 查询数量（支持位置参数和命名参数）
//
// SQL 必须返回单个数值列（通常是 COUNT(*)）。
//
// 位置参数示例：
//
//	-- rawsql/account/count_page.sql
//	SELECT COUNT(*)
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//
//	total, err := dal.Count(ctx, "account/count_page.sql", 1)
//
// 命名参数示例：
//
//	-- rawsql/order/count_page.sql
//	SELECT COUNT(*)
//	FROM   `order`
//	WHERE  deleted_at  IS NULL
//	  AND  (@account_id = 0  OR account_id = @account_id)
//	  AND  (@status    = -1 OR status     = @status)
//
//	total, err := dal.Count(
//	    ctx, "order/count_page.sql",
//	    map[string]any{"account_id": 123, "status": 1},
//	)
func Count(
	ctx context.Context,
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

	err = d.db(ctx).
		Raw(sqlText, args...).
		Scan(&total).
		Error

	cost := time.Since(start)
	debugLog(d, sqlFile, sqlText, args, cost, err)
	runAfterHooks(d, ctx, sqlFile, args, cost, err)

	if err != nil {
		return 0, fmt.Errorf("dal.Count [%s]: %w", sqlFile, err)
	}

	return total, nil
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////////// Page //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// PageResult 分页结果
type PageResult[T any] struct {
	List  []T   `json:"list"`
	Total int64 `json:"total"`
}

// QueryPage 位置参数分页查询
//
// count SQL 由数据 SQL 文件名自动推导，规则：文件名前加 "count_" 前缀。
//
//	"account/page.sql"  →  "account/count_page.sql"
//
// 数据 SQL 示例：
//
//	-- rawsql/account/page.sql
//	SELECT id, username, status, created_at
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//	ORDER BY created_at DESC
//	LIMIT  ?
//	OFFSET ?
//
// Count SQL 示例（与数据 SQL 过滤条件完全一致，去掉 LIMIT/OFFSET）：
//
//	-- rawsql/account/count_page.sql
//	SELECT COUNT(*)
//	FROM   account
//	WHERE  status     = ?
//	  AND  deleted_at IS NULL
//
// Go 示例：
//
//	result, err := dal.QueryPage[AccountVO](
//	    ctx,
//	    "account/page.sql",
//	    []any{1},      // 业务过滤参数，同时传给 count SQL: status = ?
//	    []any{10, 0},  // 分页参数，仅传给数据 SQL: LIMIT=10, OFFSET=0
//	)
//	// result.List  — 当页数据
//	// result.Total — 总条数
func QueryPage[T any](
	ctx context.Context,
	dataSqlFile string,
	filterArgs []any,
	pageArgs []any,
) (PageResult[T], error) {
	var result PageResult[T]

	countSqlFile := buildCountSQLPath(dataSqlFile)

	// 安全合并，避免修改调用方传入的原始 slice
	dataArgs := make([]any, 0, len(filterArgs)+len(pageArgs))
	dataArgs = append(dataArgs, filterArgs...)
	dataArgs = append(dataArgs, pageArgs...)

	list, err := Query[T](ctx, dataSqlFile, dataArgs...)
	if err != nil {
		return result, err
	}

	total, err := Count(ctx, countSqlFile, filterArgs...)
	if err != nil {
		return result, err
	}

	result.List = list
	result.Total = total

	return result, nil
}

// QueryPageNamed 命名参数分页查询
//
// count SQL 文件命名规则同 QueryPage，文件名前加 "count_" 前缀。
// limit 和 offset 放在 params 中，count SQL 不引用它们即可。
//
// 数据 SQL 示例：
//
//	-- rawsql/order/page.sql
//	SELECT id, order_no, amount, status, created_at
//	FROM   `order`
//	WHERE  deleted_at IS NULL
//	  AND  (@account_id = 0  OR account_id = @account_id)
//	  AND  (@status    = -1 OR status     = @status)
//	ORDER BY created_at DESC
//	LIMIT  @limit
//	OFFSET @offset
//
// Count SQL 示例（不引用 @limit/@offset 即可）：
//
//	-- rawsql/order/count_page.sql
//	SELECT COUNT(*)
//	FROM   `order`
//	WHERE  deleted_at IS NULL
//	  AND  (@account_id = 0  OR account_id = @account_id)
//	  AND  (@status    = -1 OR status     = @status)
//
// Go 示例：
//
//	result, err := dal.QueryPageNamed[OrderVO](
//	    ctx,
//	    "order/page.sql",
//	    map[string]any{
//	        "account_id": 123,
//	        "status":     1,
//	        "limit":      10,
//	        "offset":     0,
//	    },
//	)
func QueryPageNamed[T any](
	ctx context.Context,
	dataSqlFile string,
	params map[string]any,
) (PageResult[T], error) {
	var result PageResult[T]

	countSqlFile := buildCountSQLPath(dataSqlFile)

	list, err := QueryNamed[T](ctx, dataSqlFile, params)
	if err != nil {
		return result, err
	}

	// count SQL 传相同 map，SQL 中不引用 @limit/@offset 即可自动忽略
	total, err := Count(ctx, countSqlFile, params)
	if err != nil {
		return result, err
	}

	result.List = list
	result.Total = total

	return result, nil
}

func buildCountSQLPath(dataSqlFile string) string {
	dir := filepath.Dir(dataSqlFile)
	base := filepath.Base(dataSqlFile)
	return filepath.ToSlash(filepath.Join(dir, "count_"+base))
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// Transaction ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// WithTx 开启事务，fn 返回 nil 时提交，返回 error 时自动回滚。
//
// 示例（下单扣库存）：
//
//	err := dal.WithTx(ctx, func(tx *gorm.DB) error {
//	    stock, err := dal.TxQueryOne[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
//	    if err != nil {
//	        return err
//	    }
//	    if stock == nil || stock.Quantity < qty {
//	        return errors.New("库存不足")
//	    }
//	    if err := dal.TxExec(ctx, tx, "stock/deduct.sql", qty, productID, qty); err != nil {
//	        return err
//	    }
//	    return dal.TxExec(ctx, tx, "order/insert.sql", accountID, productID, qty, amount, orderNo)
