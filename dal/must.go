package dal

import (
	"context"
	"fmt"
)

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Must ///////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func MustExec(
	ctx context.Context,
	sqlFile string,
	args ...any,
) {
	if err := Exec(ctx, sqlFile, args...); err != nil {
		panic(err)
	}
}

// MustQueryOne 查询失败或记录不存在时直接 panic（慎用，仅适合确定数据存在的场景）
//
// SQL 示例：
//
//	-- rawsql/config/find_by_key.sql
//	SELECT `key`, `value`, remark
//	FROM   sys_config
//	WHERE  `key`      = ?
//	  AND  deleted_at IS NULL
//	LIMIT 1
//
// Go 示例：
//
//	cfg := dal.MustQueryOne[ConfigVO](ctx, "config/find_by_key.sql", "site_name")
func MustQueryOne[T any](
	ctx context.Context,
	sqlFile string,
	args ...any,
) *T {
	v, err := QueryOne[T](ctx, sqlFile, args...)
	if err != nil {
		panic(err)
	}
	if v == nil {
		panic(fmt.Errorf("dal.MustQueryOne [%s]: record not found", sqlFile))
	}
	return v
}
