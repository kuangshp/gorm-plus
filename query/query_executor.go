package query

import (
	"fmt"
	"github.com/kuangshp/gorm-plus/sf"
	"reflect"
)

// ================== 查询执行器（SF + Cache 统一入口） ==================
//
// 本文件提供 QueryOption.SF / QueryOption.Cache 的执行能力，
// 供 generator 生成的 repository 代码调用，业务代码也可直接使用。
//
// 设计参考 go-zero 的 sqlc.CachedConn：
//   - 走缓存时自动包 singleflight（防缓存击穿）
//   - 不走缓存时也可单独启用 singleflight（适合实时数据场景）
//
// 装饰器顺序：cache 在外、sf 在内（同 go-zero）。
// 并发请求先被 sf 合并为一次，再由这一次去查 cache，避免缓存击穿。
//
// ── 关于 cache key 的生成 ─────────────────────────────────────
//
// 所有 cache key 由 sf 包内部用 buildSFKey(fnName, args) 自动构建，
// 格式：sf:{fnName}:{md5(sorted_json(args))}
//
// 用户无需关心 key 细节，只需传 TTL。失效缓存时用同样的 fnName + args 调用：
//
//	gormplus.SFInvalidate("sys_user.FindById", gormplus.BuildArgs("id", 1))
//
// 由于 key 完全自动生成：
//   - args 不同 → 不同 key（如 id=1 和 id=2 自动隔离）
//   - args 相同 → 相同 key（缓存命中）
//   - 全局唯一：fnName 形如 "sys_user.FindById"，加上 args 的 md5，冲突概率几乎为零
//
// ⚠️ 关于 args 的完整性：
//   - 主键查询（FindById）：args 自动包含主键，key 全局唯一，可放心使用
//   - 列表/单条带条件查询（FindList / FindOne / FindListByWrapper 等）：
//     模板自动填入的 args 不包含 Where 条件！如果多次查询条件不同但用同一方法，
//     会得到相同的 cache key 导致脏数据。这种场景下，请：
//       1. 业务层不要缓存（推荐）
//       2. 或在业务代码中改用 gormplus.SF / gormplus.SFWithTTL 手动包装，
//          自己把 Where 条件值传进 args
//   - 这一限制源于 gorm-gen 的 Condition / field.Expr 是接口类型，无法稳定序列化
//     （同样原因，go-zero 的 sqlc 也只缓存主键和唯一索引查询）。
//
// ── 三种调用语义 ──────────────────────────────────────────────
//
//   1. 不传 SF/Cache（普通查询）：
//        repo.FindById(ctx, 1)
//      → 直接执行 fn()，零额外开销
//
//   2. 启用缓存（推荐）：
//        repo.FindById(ctx, 1, query.Query().WithCache(5*time.Minute).Build())
//      → 自动 sf 防击穿 + 缓存
//
//   3. 仅启用 SF（实时数据，合并并发但不缓存）：
//        repo.FindById(ctx, 1, query.Query().WithSingleFlight(0).Build())
//      → 纯 singleflight，立即 Forget
//

// ExecuteQuery 执行单条/聚合查询并按 QueryOption 决定是否走 sf/cache。
//
// 参数：
//   - opt:    查询选项（其中 SF / Cache 字段决定执行策略）
//   - fnName: 查询业务标识，建议 "表名.方法名"（generator 模板自动填充）。
//     最终 cache key 由 sf 包内部用 buildSFKey(fnName, args) 自动构建。
//   - args:   影响查询结果的参数集合（主键 ID、分页等），用 BuildArgs 构造
//   - fn:     真实查询闭包
//
// 执行规则：
//   - Cache.Enable=true：走 sf.SFWithTTL（自带 sf 保护 + 缓存）
//   - 仅 SF.Enable=true 且 TTL=0：走 sf.SFNoCache（纯 sf，不缓存）
//   - 仅 SF.Enable=true 且 TTL>0：走 sf.SFWithTTL（升级为缓存）
//   - 两者皆未启用：直接调用 fn()
func ExecuteQuery[T any](
	opt QueryOption,
	fnName string,
	args map[string]any,
	fn func() (T, error),
) (T, error) {
	// 走缓存（自动包 sf 防击穿）
	if opt.Cache != nil && opt.Cache.Enable {
		// 合并模板默认 args 与业务方通过 WithCacheArgs/WithCacheArgsMap 提供的 args
		// 业务方 args 覆盖同名模板 args（虽然实践中 key 不应重叠）
		return sf.SFWithTTL(fn, fnName, mergeCacheArgs(args, opt.Cache.Args), opt.Cache.TTL)
	}
	// 仅走 sf
	if opt.SF != nil && opt.SF.Enable {
		// TTL > 0：等价于走缓存
		if opt.SF.TTL > 0 {
			return sf.SFWithTTL(fn, fnName, args, opt.SF.TTL)
		}
		return sf.SFNoCache(fn, fnName, args)
	}
	// 直接执行
	return fn()
}

// pageResult 是 ExecutePage 内部用于包装分页结果的中间类型，
// 因为 SF 的泛型签名只允许返回单值，所以把 (list, total) 打包成 *pageResult[T] 走流程。
type pageResult[T any] struct {
	List  []T
	Total int64
}

// ExecutePage 执行分页查询（list + total 双返回值）的 sf/cache 包装。
//
// 因为分页方法签名是 (list []*T, total int64, err error)，无法直接复用
// ExecuteQuery 的泛型 T，故单独提供。内部把 list+total 打包成 *pageResult[T]
// 走 SF 流程，再解包返回。
//
// 用法示例（generator 模板使用）：
//
//	list, total, err := query.ExecutePage[*model.UserEntity](
//	    opt,
//	    "sys_user.FindPage",
//	    gormplus.BuildArgs("page", pageNumber, "size", pageSize),
//	    func() ([]*model.UserEntity, int64, error) {
//	        return r.buildTx(ctx, query).FindByPage(offset, limit)
//	    },
//	)
func ExecutePage[T any](
	opt QueryOption,
	fnName string,
	args map[string]any,
	fn func() ([]T, int64, error),
) ([]T, int64, error) {
	// 不走 sf/cache 的快速路径
	if (opt.Cache == nil || !opt.Cache.Enable) && (opt.SF == nil || !opt.SF.Enable) {
		return fn()
	}

	wrapped := func() (*pageResult[T], error) {
		list, total, err := fn()
		if err != nil {
			return nil, err
		}
		return &pageResult[T]{List: list, Total: total}, nil
	}

	res, err := ExecuteQuery[*pageResult[T]](opt, fnName, args, wrapped)
	if err != nil || res == nil {
		return nil, 0, err
	}
	return res.List, res.Total, nil
}

// BuildArgs 把零散的 key-value 拼成 map[string]any，用于构造 sf/cache 的 args。
//
// 用法：
//
//	args := query.BuildArgs("id", id, "status", 1)
//
// 等价于：
//
//	args := map[string]any{"id": id, "status": 1}
//
// 奇数个参数时最后一个会被忽略；非 string 类型的 key 用 fmt 转换。
func BuildArgs(kv ...any) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", kv[i])
		}
		m[key] = kv[i+1]
	}
	return m
}

// BuildArgsFromStruct 把结构体字段自动展开为 cache args。
//
// 适用场景:手动调 SF / SFWithTTL / SFNoCache 时,不想手写
// BuildArgs("a", x.A, "b", x.B, ...) 一长串,直接把整个请求 DTO 喂进来。
//
// 字段名规则(与 QueryBuilder.WithCacheArgsFromStruct 完全一致):
//  1. 优先用 json tag 的第一段(去掉 omitempty 等)
//  2. json tag 为 "-" 的字段跳过
//  3. 没有 tag 则用结构体字段名本身
//  4. 嵌入字段递归平铺到顶层
//  5. nil 指针字段跳过(自动解引用)
//
// 使用示例:
//
//	type LoginQueryReq struct {
//	    Days    int    `json:"days"`
//	    UserId  int64  `json:"user_id,omitempty"`
//	    Source  string `json:"source,omitempty"`
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
//	        query.BuildArgsFromStruct(req),   // ← 一行展开所有字段
//	        5*time.Minute,
//	    )
//	}
//
// 传入 nil / 非结构体 / nil 指针时返回空 map(不会 panic)。
func BuildArgsFromStruct(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return map[string]any{}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return map[string]any{}
	}

	out := make(map[string]any, rv.NumField())
	FlattenStructToArgs(rv, out)
	return out
}

// mergeCacheArgs 把模板默认 args 与业务方提供的 cache args 合并。
//
// 合并规则：
//   - 任一为空时返回另一个，避免不必要的 map 分配
//   - 同名 key 时业务方 args（userArgs）覆盖模板默认 args（tplArgs）
//   - 最终 key 由 sf 包内部 marshalSorted 按 key 排序，故合并顺序不影响 key
func mergeCacheArgs(tplArgs, userArgs map[string]any) map[string]any {
	if len(userArgs) == 0 {
		return tplArgs
	}
	if len(tplArgs) == 0 {
		return userArgs
	}
	merged := make(map[string]any, len(tplArgs)+len(userArgs))
	for k, v := range tplArgs {
		merged[k] = v
	}
	for k, v := range userArgs {
		merged[k] = v
	}
	return merged
}

// FirstQueryOption 从可变参数中取出第一个 QueryOption。
// 模板里 query ...QueryOption 风格的参数会用到，避免 len(query)>0 ? query[0] : QueryOption{} 这种判断重复出现。
func FirstQueryOption(opts []QueryOption) QueryOption {
	if len(opts) == 0 {
		return QueryOption{}
	}
	return opts[0]
}
