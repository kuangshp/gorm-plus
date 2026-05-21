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

// BuildArgsFromStruct 把结构体字段自动展开为 cache args,适合手动调
// SF / SFWithTTL / SFNoCache 时不想手写一长串 BuildArgs 的场景。
//
// 字段名规则:
//  1. 优先用 json tag 第一段(去掉 omitempty 等修饰符)
//  2. json tag 为 "-" 的字段跳过
//  3. 没有 tag 用结构体字段名本身
//  4. 嵌入字段递归平铺到顶层
//  5. nil 指针字段跳过
//
// 使用示例(DAL + SF 包装):
//
//	type LoginQueryReq struct {
//	    Days   int    `json:"days"`
//	    UserId int64  `json:"user_id,omitempty"`
//	    Source string `json:"source,omitempty"`
//	}
//
//	func (r *customerAccountRepository) FindLogins(
//	    ctx context.Context, req LoginQueryReq,
//	) ([]*LoginRow, error) {
//	    return gormplus.SF(func() ([]*LoginRow, error) {
//	        return gormplus.DALQuery[*LoginRow](ctx, "login.sql",
//	            req.Days, req.UserId, req.Source)
//	    },
//	        "account.FindLogins",
//	        gormplus.BuildArgsFromStruct(req),   // ← 一行替代手写
//	        5*time.Minute,
//	    )
//	}
//
// 传入 nil / 非结构体 / nil 指针时返回空 map(不会 panic)。
func BuildArgsFromStruct(v any) map[string]any {
	return query.BuildArgsFromStruct(v)
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
