package query

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"gorm.io/gen"
	"gorm.io/gen/field"
)

type SingleFlightOption struct {
	TTL    time.Duration
	Enable bool
}

type CacheOption struct {
	TTL    time.Duration
	Enable bool
	// Args 业务方提供的补充 cache key 参数。
	// 用于解决列表/分页查询中 Where 条件值无法自动捕获的问题。
	// 框架会把模板默认 args（如 page/size/id）与此 Args 合并后送入 buildSFKey。
	// 由 marshalSorted 按 key 字典序排序后 json 序列化再 md5，故传入顺序无关。
	Args map[string]any
}

// QueryOption 查询参数结构体
type QueryOption struct {
	Cond       []gen.Condition     // 原生条件
	Order      []field.Expr        // 排序字段
	Select     []field.Expr        // 指定查询字段
	OmitFields []field.Expr        // 排除字段
	Limit      *int                // 查询条数
	SF         *SingleFlightOption // 使用sf并发处理
	Cache      *CacheOption        // 使用缓存
}

// QueryBuilder 链式构建器
type QueryBuilder struct {
	option QueryOption
}

// Query 创建构建器
func Query() *QueryBuilder {
	return &QueryBuilder{
		option: QueryOption{},
	}
}

// Where 条件
func (q *QueryBuilder) Where(cond ...gen.Condition) *QueryBuilder {
	q.option.Cond = append(
		q.option.Cond,
		cond...,
	)
	return q
}

// Order 排序
func (q *QueryBuilder) Order(fields ...field.Expr) *QueryBuilder {
	q.option.Order = append(
		q.option.Order,
		fields...,
	)
	return q
}

// Select 指定字段
func (q *QueryBuilder) Select(fields ...field.Expr) *QueryBuilder {
	q.option.Select = append(
		q.option.Select,
		fields...,
	)
	return q
}

// Omit 排除字段
func (q *QueryBuilder) Omit(fields ...field.Expr) *QueryBuilder {
	q.option.OmitFields = append(
		q.option.OmitFields,
		fields...,
	)
	return q
}

// Limit 查询条数
func (q *QueryBuilder) Limit(limit int) *QueryBuilder {
	q.option.Limit = &limit
	return q
}

// WithSingleFlight 启用 singleflight，key 自动由 fnName + args 生成（buildSFKey）。
//
//   - ttl=0：纯 singleflight 合并并发，不缓存（适合实时数据如用户余额、详情）
//   - ttl>0：singleflight + 缓存（等价于 WithCache）
//
// 示例：
//
//	repo.FindById(ctx, 1, query.Query().WithSingleFlight(0).Build())
func (q *QueryBuilder) WithSingleFlight(ttl time.Duration) *QueryBuilder {
	q.option.SF = &SingleFlightOption{
		Enable: true,
		TTL:    ttl,
	}
	return q
}

// WithCache 启用缓存，key 自动由 fnName + args 生成，自动包 singleflight 防缓存击穿。
//
// 这是缓存的推荐用法，符合 go-zero sqlc.CachedConn 的设计哲学：用户只关心 TTL，
// 不需要手动管理 cache key。失效缓存时：
//
//	gormplus.SFInvalidate("sys_user.FindById", gormplus.BuildArgs("id", int64(1)))
//
// 示例：
//
//	repo.FindById(ctx, 1, query.Query().WithCache(5*time.Minute).Build())
func (q *QueryBuilder) WithCache(ttl time.Duration) *QueryBuilder {
	if q.option.Cache == nil {
		q.option.Cache = &CacheOption{}
	}
	q.option.Cache.Enable = true
	q.option.Cache.TTL = ttl
	return q
}

// WithCacheArgs 追加 cache key 参数（变参形式）。
//
// 用于解决列表/分页查询中 Where 条件值无法自动进入 cache key 的问题：
// 业务方把影响结果的条件值显式声明出来，框架将其与模板默认 args 合并后参与 key 计算。
//
// 参数为 key-value 交替的变参，最后一个落单的 key 会被忽略。
// 多次调用 / 与 WithCacheArgsMap 混用时，后调用的同名 key 会覆盖前者。
// 内部由 marshalSorted 按 key 字典序排序，传入顺序不影响最终 key。
//
// 示例：
//
//	// 列表分页查询：不同 status 的列表得到不同缓存
//	repo.FindPage(ctx, 1, 20, query.Query().
//	    Where(dao.User.Status.Eq(1)).
//	    WithCache(5*time.Minute).
//	    WithCacheArgs("status", 1).
//	    Build())
//
//	// 多个条件
//	repo.FindList(ctx, query.Query().
//	    Where(dao.User.Status.Eq(1), dao.User.Type.Eq("vip")).
//	    WithCache(30*time.Second).
//	    WithCacheArgs("status", 1, "type", "vip").
//	    Build())
func (q *QueryBuilder) WithCacheArgs(kv ...any) *QueryBuilder {
	if q.option.Cache == nil {
		q.option.Cache = &CacheOption{}
	}
	if q.option.Cache.Args == nil {
		q.option.Cache.Args = make(map[string]any)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", kv[i])
		}
		q.option.Cache.Args[key] = kv[i+1]
	}
	return q
}

// WithCacheArgsMap 追加 cache key 参数（map 形式）。
//
// 适合从 HTTP 请求 / 查询 DTO 等已有 map 结构直接灌入，避免手动展开成 kv 对。
// 多次调用 / 与 WithCacheArgs 混用时，后调用的同名 key 会覆盖前者。
// 内部由 marshalSorted 按 key 字典序排序，故 map 本身的无序性不影响最终 key。
//
// 示例：
//
//	// HTTP handler 直接把 query 参数喂进来
//	queryMap := map[string]any{
//	    "status":   ctx.Query("status"),
//	    "type":     ctx.Query("type"),
//	    "keyword":  ctx.Query("keyword"),
//	}
//	repo.FindPage(ctx, page, size, query.Query().
//	    Where(...).
//	    WithCache(30*time.Second).
//	    WithCacheArgsMap(queryMap).
//	    Build())
//
// 注意：传入的 map 值必须是可被 json.Marshal 的类型；如果是结构体/切片，
// 字段顺序 / 元素顺序会影响最终 key（因为 json 序列化保序）。
func (q *QueryBuilder) WithCacheArgsMap(m map[string]any) *QueryBuilder {
	if q.option.Cache == nil {
		q.option.Cache = &CacheOption{}
	}
	if q.option.Cache.Args == nil {
		q.option.Cache.Args = make(map[string]any, len(m))
	}
	for k, v := range m {
		q.option.Cache.Args[k] = v
	}
	return q
}

// WithCacheArgsFromStruct 把结构体字段自动展开为 cache args，方便 POST 分页 DTO 直接灌入。
//
// 使用场景：分页查询的 POST 请求里通常会绑定一个查询 DTO，业务方不希望手写一长串
// WithCacheArgs("page", req.Page, "size", req.Size, "status", req.Status, ...)
// 用本方法直接传 DTO 指针/值即可。
//
// 字段名取值规则：
//  1. 优先用 json tag 的第一段（如 `json:"user_name,omitempty"` → "user_name"）
//  2. json tag 为 "-" 的字段跳过
//  3. 没有 json tag 的字段用结构体字段名本身（如 "UserName"）
//
// 跳过规则：
//   - nil 指针字段跳过（避免污染 key）
//   - 字段值为零值时仍会进入 args（业务方需要语义一致性，所以保留）
//   - 嵌入字段会递归展开
//
// 示例（POST 分页查询）：
//
//	type UserListReq struct {
//	    Page    int    `json:"page"`
//	    Size    int    `json:"size"`
//	    Status  int    `json:"status,omitempty"`
//	    Keyword string `json:"keyword,omitempty"`
//	    UserId  int64  `json:"user_id,omitempty"`
//	}
//
//	func (h *UserHandler) List(c *gin.Context) {
//	    var req UserListReq
//	    c.ShouldBindJSON(&req)
//
//	    list, total, _ := h.repo.FindPage(c, req.Page, req.Size, query.Query().
//	        Where(buildConditions(req)...).
//	        WithCache(30*time.Second).
//	        WithCacheArgsFromStruct(req).    // ← 一行灌入,无需手动展开
//	        Build())
//	}
//
// 注意：传入结构体的字段顺序、值类型必须稳定，跨版本改字段名会导致 cache key 漂移。
func (q *QueryBuilder) WithCacheArgsFromStruct(v any) *QueryBuilder {
	if v == nil {
		return q
	}

	rv := reflect.ValueOf(v)
	// 解指针
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return q
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return q
	}

	if q.option.Cache == nil {
		q.option.Cache = &CacheOption{}
	}
	if q.option.Cache.Args == nil {
		q.option.Cache.Args = make(map[string]any, rv.NumField())
	}

	FlattenStructToArgs(rv, q.option.Cache.Args)
	return q
}

// FlattenStructToArgs 递归把结构体字段写入 args map(导出供 BuildArgsFromStruct 复用)。
// 嵌入字段会平铺到顶层(避免出现 args["EmbeddedStruct"] 这种不稳定的 key)。
//
// 一般业务方不直接调用此函数,而是用更高层的:
//   - QueryBuilder.WithCacheArgsFromStruct(req)        链式查询场景
//   - BuildArgsFromStruct(req)                          手动 SF 调用场景
func FlattenStructToArgs(rv reflect.Value, out map[string]any) {
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		sf := rt.Field(i)
		// 跳过未导出字段
		if !sf.IsExported() {
			continue
		}
		fv := rv.Field(i)

		// 嵌入字段递归展开
		if sf.Anonymous {
			fk := fv.Kind()
			if fk == reflect.Ptr {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
				fk = fv.Kind()
			}
			if fk == reflect.Struct {
				FlattenStructToArgs(fv, out)
				continue
			}
		}

		// 解析 json tag
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := sf.Name
		if tag != "" {
			if idx := strings.Index(tag, ","); idx >= 0 {
				tag = tag[:idx]
			}
			if tag != "" {
				name = tag
			}
		}

		// 解指针；nil 指针跳过避免污染 key
		for fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				fv = reflect.Value{}
				break
			}
			fv = fv.Elem()
		}
		if !fv.IsValid() {
			continue
		}

		out[name] = fv.Interface()
	}
}

// Build 构建QueryOption
func (q *QueryBuilder) Build() QueryOption {
	return q.option
}

// MergeQueryOptions 合并配置
func MergeQueryOptions(opts ...QueryOption) QueryOption {
	var result QueryOption
	for _, opt := range opts {
		// Cond
		if len(opt.Cond) > 0 {
			result.Cond = append(
				result.Cond,
				opt.Cond...,
			)
		}
		// Order
		if len(opt.Order) > 0 {
			result.Order = append(
				result.Order,
				opt.Order...,
			)
		}

		// Select
		if len(opt.Select) > 0 {
			result.Select = append(
				result.Select,
				opt.Select...,
			)
		}

		// Omit
		if len(opt.OmitFields) > 0 {
			result.OmitFields = append(
				result.OmitFields,
				opt.OmitFields...,
			)
		}

		// Limit
		if opt.Limit != nil {
			result.Limit = opt.Limit
		}

		// SF
		if opt.SF != nil {
			result.SF = opt.SF
		}
		// Cache：TTL/Enable 后者覆盖，Args 累积合并（service 层和业务层都能贡献 args）
		if opt.Cache != nil {
			if result.Cache == nil {
				result.Cache = &CacheOption{}
			}
			result.Cache.Enable = opt.Cache.Enable
			if opt.Cache.TTL > 0 {
				result.Cache.TTL = opt.Cache.TTL
			}
			if len(opt.Cache.Args) > 0 {
				if result.Cache.Args == nil {
					result.Cache.Args = make(map[string]any, len(opt.Cache.Args))
				}
				for k, v := range opt.Cache.Args {
					result.Cache.Args[k] = v
				}
			}
		}
	}
	return result
}
