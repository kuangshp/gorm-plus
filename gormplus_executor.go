package gormplus

import (
	"github.com/kuangshp/gorm-plus/query"
)

// ================== 查询执行器（SF + Cache 统一入口） ==================
//
// 以下函数主要供 generator 生成的 repository 代码消费 QueryOption.SF / QueryOption.Cache 字段，
// 也可在业务代码中直接调用。设计上参考 go-zero sqlc.CachedConn：
//
//   - QueryOption.Cache 启用时：自动包 singleflight 防缓存击穿
//   - 仅 QueryOption.SF 启用时：纯 singleflight，不缓存（实时数据场景）
//   - 两者皆未启用：直接执行查询，零额外开销

// QueryOption 查询参数结构体（条件、排序、字段选择、limit、SF、Cache 等）。
// 通过 query.Query() 链式构造器构建，或直接构造字面量。
type QueryOption = query.QueryOption

// ExecuteQuery 执行单条/聚合查询并按 QueryOption 决定是否走 sf/cache。
//
// 参数：
//   - opt:    查询选项（其中 SF / Cache 字段决定执行策略）
//   - fnName: 查询唯一标识，建议 "表名.方法名"
//   - args:   影响查询结果的参数集合，可用 BuildArgs 快速构造
//   - fn:     真实查询闭包
//
// 示例：
//
//	user, err := gormplus.ExecuteQuery(opt, "sys_user.FindById",
//	    gormplus.BuildArgs("id", userId),
//	    func() (*model.UserEntity, error) {
//	        return dao.UserEntity.WithContext(ctx).Where(dao.UserEntity.ID.Eq(userId)).First()
//	    })
func ExecuteQuery[T any](
	opt query.QueryOption,
	fnName string,
	args map[string]any,
	fn func() (T, error),
) (T, error) {
	return query.ExecuteQuery(opt, fnName, args, fn)
}

// ExecutePage 执行分页查询（list + total）并按 QueryOption 决定是否走 sf/cache。
// 用于 FindPage / FindPageByWrapper 类双返回值方法。
func ExecutePage[T any](
	opt query.QueryOption,
	fnName string,
	args map[string]any,
	fn func() ([]T, int64, error),
) ([]T, int64, error) {
	return query.ExecutePage(opt, fnName, args, fn)
}

// BuildArgs 把零散的 key-value 拼成 map[string]any，方便构造 sf/cache 的 args。
// 奇数个参数时最后一个会被忽略。
//
// 示例：
//
//	args := gormplus.BuildArgs("page", pageNum, "size", pageSize, "status", 1)
func BuildArgs(kv ...any) map[string]any {
	return query.BuildArgs(kv...)
}

// FirstQueryOption 从可变参数中取第一个 QueryOption。
// 当只取首个 opt 即可（不需要合并多个）时使用，比 MergeQueryOptions 省一次循环。
func FirstQueryOption(opts []query.QueryOption) query.QueryOption {
	return query.FirstQueryOption(opts)
}

// MergeQueryOptions 合并多个 QueryOption。
// 后者覆盖前者的同名字段，Cache.Args 累积合并（支持 service 层和业务层各自贡献 args）。
func MergeQueryOptions(opts ...query.QueryOption) query.QueryOption {
	return query.MergeQueryOptions(opts...)
}
