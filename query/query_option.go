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
	Unscoped   bool                // 包含主表逻辑删除数据
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

// Where 追加原生 WHERE 条件,接受任意实现 gen.Condition 接口的值,
// 包括 gorm-gen 类型字段(如 dao.X.Y.Eq(1))和 RawField 包装的原生 SQL。
//
// 多次调用按顺序累积,所有条件之间是 AND 关系。
//
// 示例:
//
//	query.Query().
//	    Where(dao.User.Status.Eq(1)).
//	    Where(dao.User.Type.Eq("vip")).
//	    Build()
//	// SQL: WHERE status = 1 AND type = 'vip'
//
// 复合 SQL 片段(用 RawField 包装,因为 field.Expr 也实现了 gen.Condition):
//
//	query.Query().Where(query.RawField(
//	    "(discount_amount IS NOT NULL AND discount_amount != '') OR " +
//	    "(discount_label IS NOT NULL AND discount_label != '')",
//	)).Build()
//
// 也可以用更直观的 WhereRaw / WhereRawIf 方法,见下面。
func (q *QueryBuilder) Where(cond ...gen.Condition) *QueryBuilder {
	q.option.Cond = append(
		q.option.Cond,
		cond...,
	)
	return q
}

// WhereRaw 用原生 SQL 片段追加 WHERE 条件,语义比 Where(query.RawField(...)) 更明确。
//
// vars 走 gorm 标准 ? 占位符机制,会被参数化,安全防 SQL 注入。
//
// 示例:
//
//	// 1) 复合 OR 条件
//	query.Query().WhereRaw(
//	    "(discount_amount IS NOT NULL AND discount_amount != '') " +
//	    "OR (discount_label IS NOT NULL AND discount_label != '')",
//	).Build()
//
//	// 2) 带占位符的条件
//	query.Query().WhereRaw(
//	    "created_at BETWEEN ? AND ?", startTime, endTime,
//	).Build()
//
//	// 3) JOIN 后用别名引用其他表字段
//	query.Query().WhereRaw("b.status = ?", 1).Build()
//
// ⚠️ 安全提示:sql 参数严禁拼接用户输入,只允许传"固定的 SQL 片段",
// 用户输入应该通过 vars 参数走 ? 占位符传入。
//
// 反例:
//
//	WhereRaw("user_name LIKE '%" + userInput + "%'")     // ❌ SQL 注入
//
// 正例:
//
//	WhereRaw("user_name LIKE ?", "%"+userInput+"%")      // ✅ 参数化
//
// 空 sql 静默跳过,不会产生空条件。
func (q *QueryBuilder) WhereRaw(sql string, vars ...interface{}) *QueryBuilder {
	if sql == "" {
		return q
	}
	q.option.Cond = append(q.option.Cond, RawField(sql, vars...))
	return q
}

// WhereRawIf 仅在 cond 为 true 时追加原生 SQL 条件,适合可选过滤场景。
//
// 等价于:
//
//	if cond { q.WhereRaw(sql, vars...) }
//
// 但链式风格更紧凑,适合多个可选条件的场景。
//
// 示例:
//
//	query.Query().
//	    WhereRawIf(req.IsDiscount == 1,
//	        "(discount_amount IS NOT NULL AND discount_amount != '') " +
//	        "OR (discount_label IS NOT NULL AND discount_label != '')").
//	    WhereRawIf(req.IsDiscount == 2,
//	        "(discount_amount IS NULL OR discount_amount = '') " +
//	        "AND (discount_label IS NULL OR discount_label = '')").
//	    WhereRawIf(req.StartTime > 0,
//	        "created_at >= ?", req.StartTime).
//	    Build()
//
// cond 为 false 或 sql 为空时静默跳过。
func (q *QueryBuilder) WhereRawIf(cond bool, sql string, vars ...interface{}) *QueryBuilder {
	if !cond || sql == "" {
		return q
	}
	q.option.Cond = append(q.option.Cond, RawField(sql, vars...))
	return q
}

// WithDeleted 查询时包含主表逻辑删除数据。
func (q *QueryBuilder) WithDeleted() *QueryBuilder {
	q.option.Unscoped = true
	return q
}

// WithUnscoped 是 WithDeleted 的 GORM 语义别名。
func (q *QueryBuilder) WithUnscoped() *QueryBuilder {
	return q.WithDeleted()
}

// WhereNotDeleted 追加指定表/别名的未删除条件，适合手写 JOIN 的从表过滤。
//
// 示例：WhereNotDeleted("d") => d.deleted_at IS NULL。
func (q *QueryBuilder) WhereNotDeleted(tableOrAlias string) *QueryBuilder {
	tableOrAlias = strings.TrimSpace(tableOrAlias)
	if tableOrAlias == "" {
		return q
	}
	return q.WhereRaw(fmt.Sprintf("%s.deleted_at IS NULL", tableOrAlias))
}

// WhereDeleted 追加指定表/别名的已删除条件，适合查询 JOIN 从表的逻辑删除数据。
//
// 示例：WhereDeleted("d") => d.deleted_at IS NOT NULL。
func (q *QueryBuilder) WhereDeleted(tableOrAlias string) *QueryBuilder {
	tableOrAlias = strings.TrimSpace(tableOrAlias)
	if tableOrAlias == "" {
		return q
	}
	return q.WhereRaw(fmt.Sprintf("%s.deleted_at IS NOT NULL", tableOrAlias))
}

// Order 追加排序字段(链式顺序生效,先调先排)。
//
// 接受 field.Expr,既支持 gorm-gen 类型安全字段(如 dao.X.Y.Desc()),
// 也支持 RawField 包装的原生 SQL 片段。多次调用会按顺序累积。
//
// 示例:
//
//	query.Query().
//	    Order(dao.Order.Status.Asc()).             // 主排序:status
//	    Order(dao.Order.CreatedAt.Desc()).         // 次排序:created_at
//	    Build()
//	// SQL: ORDER BY status ASC, created_at DESC
//
// ── 组合示例:用户传字段排序 + 默认兜底 ───────────────────────────────────
//
//	b := query.Query().Where(...)
//	if req.TotalAmountSort != 0 {
//	    b.OrderIf(req.TotalAmountSort > 0,
//	        dao.Interview.TotalAmount.Asc(),
//	        dao.Interview.TotalAmount.Desc())
//	}
//	if req.ReturnTimeSort != 0 {
//	    b.OrderIf(req.ReturnTimeSort > 0,
//	        dao.Interview.ReturnTime.Asc(),
//	        dao.Interview.ReturnTime.Desc())
//	}
//	// 用户什么都没传时用此默认排序,前面有任意 Order 时此调用被忽略
//	b.OrderDefault(query.RawField("b.id DESC"))
//	repo.FindPage(ctx, page, size, b.Build())
func (q *QueryBuilder) Order(fields ...field.Expr) *QueryBuilder {
	q.option.Order = append(q.option.Order, fields...)
	return q
}

// OrderRaw 用原生 SQL 片段追加排序,内部走 RawField。
//
// 适合写多表 JOIN 字段、SQL 函数排序、CASE WHEN 表达式等
// gorm-gen 类型安全字段表达不了的场景。
//
// ⚠️ 安全提示:vars 参数走 fmt.Sprintf 拼接,**不是 SQL 占位符**,
// 严禁拼接用户输入(SQL 注入)。仅用于固定的列名、表别名等常量片段。
//
// 示例:
//
//	// 多表 JOIN,b 是 interview 表别名
//	query.Query().OrderRaw("b.id DESC").Build()
//
//	// CASE WHEN 排序(VIP 用户置顶)
//	query.Query().OrderRaw(
//	    "CASE WHEN status = 'vip' THEN 0 ELSE 1 END ASC",
//	).OrderRaw("created_at DESC").Build()
//
//	// 多个字段一次性写(等价于多次 OrderRaw)
//	query.Query().OrderRaw("a.priority DESC, b.id DESC").Build()
//
// 多次调用按顺序累积,与 Order / OrderIf / OrderDefault 写到同一 slice。
func (q *QueryBuilder) OrderRaw(sql string, vars ...interface{}) *QueryBuilder {
	if sql == "" {
		return q
	}
	q.option.Order = append(q.option.Order, RawField(sql, vars...))
	return q
}

// OrderIf 条件性追加排序:cond 为 true 用 truthy 排序,否则用 falsy 排序。
//
// 适合 "asc/desc 取决于前端 1/2" 这类场景。
// 若 truthy / falsy 任一为 nil/空,该分支静默跳过。
//
// 示例:
//
//	// req.TotalAmountSort:1=升序 2=降序 其他=跳过此字段
//	b := query.Query().Where(...)
//	if req.TotalAmountSort != 0 {
//	    b.OrderIf(req.TotalAmountSort == 1,
//	        dao.Interview.TotalAmount.Asc(),    // truthy: 升序
//	        dao.Interview.TotalAmount.Desc())   // falsy: 降序
//	}
//
//	// 只在某条件下加一个排序,另一分支不加
//	b.OrderIf(req.UrgentOnly,
//	    dao.Order.Priority.Desc())  // 只传 truthy,cond=false 时整个跳过
func (q *QueryBuilder) OrderIf(cond bool, truthy field.Expr, falsy ...field.Expr) *QueryBuilder {
	if cond {
		if truthy != nil {
			q.option.Order = append(q.option.Order, truthy)
		}
	} else {
		for _, f := range falsy {
			if f != nil {
				q.option.Order = append(q.option.Order, f)
			}
		}
	}
	return q
}

// OrderTriState 按 0/1/2 三态选择排序方向,贴合前端常见的"未选/升序/降序"约定。
//
// 语义:
//   - state == 0   → 跳过此字段(不进入 Order)
//   - state == 1   → 用 asc(升序)
//   - state == 2   → 用 desc(降序)
//   - 其他任意值   → 跳过(健壮性兜底)
//
// 适合 POST 分页里这种字段:
//
//	type ListReq struct {
//	    TotalAmountSort    int8 `json:"total_amount_sort"`     // 0/1/2
//	    TotalAmountCNYSort int8 `json:"total_amount_cny_sort"` // 0/1/2
//	    ReturnTimeSort     int8 `json:"return_time_sort"`      // 0/1/2
//	    InvoiceTimeSort    int8 `json:"invoice_time_sort"`     // 0/1/2
//	}
//
// 用法对比:
//
//	// ❌ 旧写法,每个字段两层包裹,4 个字段 = 一坨 if
//	if req.TotalAmountSort != 0 {
//	    b.OrderIf(req.TotalAmountSort == 1,
//	        dao.Interview.TotalAmount.Asc(),
//	        dao.Interview.TotalAmount.Desc())
//	}
//	if req.ReturnTimeSort != 0 {
//	    b.OrderIf(req.ReturnTimeSort == 1, ...)
//	}
//
//	// ✅ 新写法,一行一个字段,前端传 0 自动跳过
//	b.OrderTriState(req.TotalAmountSort,
//	    dao.Interview.TotalAmount.Asc(),
//	    dao.Interview.TotalAmount.Desc()).
//	OrderTriState(req.TotalAmountCNYSort,
//	    dao.Interview.TotalAmountCNY.Asc(),
//	    dao.Interview.TotalAmountCNY.Desc()).
//	OrderTriState(req.ReturnTimeSort,
//	    dao.Interview.ReturnTime.Asc(),
//	    dao.Interview.ReturnTime.Desc()).
//	OrderTriState(req.InvoiceTimeSort,
//	    dao.Interview.InvoiceTime.Asc(),
//	    dao.Interview.InvoiceTime.Desc()).
//	OrderDefault(query.RawField("b.id DESC"))   // 全 0 时才用此默认排序
//
// state 类型用 any 兼容前端可能传 int / int8 / int32 / int64 / 字符串等,
// 内部统一归一化为整数比较;无法识别的类型当作 0 处理。
//
// 如果前端用其他约定(比如 1=desc 2=asc,或者用字符串 "asc"/"desc"),
// 应该自己改用 OrderIf 显式判断,不要乱套 OrderTriState 的语义。
func (q *QueryBuilder) OrderTriState(state any, asc, desc field.Expr) *QueryBuilder {
	switch normalizeSortState(state) {
	case 1:
		if asc != nil {
			q.option.Order = append(q.option.Order, asc)
		}
	case 2:
		if desc != nil {
			q.option.Order = append(q.option.Order, desc)
		}
	}
	return q
}

// normalizeSortState 把前端传入的 sort 状态归一化为 int8(0/1/2),
// 兼容 int / int8 / int16 / int32 / int64 / uint* / float* / string("0"/"1"/"2") / json.Number。
// 识别不出的类型一律返回 0(跳过排序),不报错。
func normalizeSortState(s any) int8 {
	if s == nil {
		return 0
	}
	switch v := s.(type) {
	case int:
		return clampSortState(int64(v))
	case int8:
		return clampSortState(int64(v))
	case int16:
		return clampSortState(int64(v))
	case int32:
		return clampSortState(int64(v))
	case int64:
		return clampSortState(v)
	case uint:
		return clampSortState(int64(v))
	case uint8:
		return clampSortState(int64(v))
	case uint16:
		return clampSortState(int64(v))
	case uint32:
		return clampSortState(int64(v))
	case uint64:
		return clampSortState(int64(v))
	case float32:
		return clampSortState(int64(v))
	case float64:
		return clampSortState(int64(v))
	case string:
		// "0" / "1" / "2",或别名 "asc" / "desc"
		switch v {
		case "1", "asc", "ASC":
			return 1
		case "2", "desc", "DESC":
			return 2
		}
		return 0
	default:
		return 0
	}
}

func clampSortState(n int64) int8 {
	if n == 1 || n == 2 {
		return int8(n)
	}
	return 0
}

// OrderDefault 仅在此前未设置任何排序时生效(用作默认/兜底排序)。
//
// 调用顺序无关:不管放在链式哪个位置,只要 Build() 时检测到 q.Order 已经被
// 之前的 Order / OrderRaw / OrderIf 填过任一字段,本次调用就被静默忽略。
//
// 适合"用户传了什么用什么、否则用默认排序"这类场景。
//
// 示例:
//
//	query.Query().
//	    OrderIf(req.AmountSort == 1, dao.X.Amount.Asc(),
//	                                  dao.X.Amount.Desc()).
//	    OrderIf(req.TimeSort != 0,
//	            chooseTimeOrder(req.TimeSort)).
//	    OrderDefault(query.RawField("b.id DESC")).   // ← 上面都没排到时才用
//	    Build()
//
// 注意:OrderDefault 只能在没有任何 Order 时生效。如果你想"用户传了字段后
// 仍然在末尾追加一个 fallback 排序"(SQL 里的 stable order),应该用 Order 而不是 OrderDefault。
func (q *QueryBuilder) OrderDefault(fields ...field.Expr) *QueryBuilder {
	if len(q.option.Order) > 0 {
		return q
	}
	for _, f := range fields {
		if f != nil {
			q.option.Order = append(q.option.Order, f)
		}
	}
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

// ═══════════════════════════════════════════════════════════════════════════
//  三个 WithCacheArgs* 方法的组合规则
// ═══════════════════════════════════════════════════════════════════════════
//
// 三个方法写的是同一份 Cache.Args map,所以可以自由组合使用:
//
//   WithCacheArgs("k", v)           变参形式,适合少量字面值
//   WithCacheArgsMap(m)              map 形式,适合已经组装好的 map
//   WithCacheArgsFromStruct(req)     反射形式,适合 POST DTO 直接灌入
//
// ── 覆盖规则:链式调用顺序,后写覆盖先写 ──────────────────────────────────
//
//   query.Query().
//       WithCacheArgs("status", 1, "page", 10).        // status=1, page=10
//       WithCacheArgsMap(map[string]any{               //
//           "status": 2,                                // ← status 被覆盖为 2
//           "size":   20,                               //   新增 size=20
//       }).
//       WithCacheArgsFromStruct(LoginReq{              //
//           Status: 3,                                  // ← status 再次被覆盖为 3
//           Page:   99,                                 // ← page 被覆盖为 99
//       }).
//       Build()
//   // 最终:status=3, page=99, size=20
//
// ── 推荐使用模式 ──────────────────────────────────────────────────────────
//
// 模式 A:Service 层贡献固定字段 + Handler 层灌入 DTO(最常见)
//
//   repo.FindPage(ctx, req.Page, req.Size, query.Query().
//       Where(buildConditions(req)...).
//       WithCache(30*time.Second).
//       WithCacheArgs("tenant_id", tenantId).      // Service 层固定值
//       WithCacheArgsFromStruct(req).               // Handler 层 DTO 整体
//       Build())
//
// 模式 B:只用 DTO(POST 接口最常用)
//
//   repo.FindPage(..., query.Query().
//       WithCache(30*time.Second).
//       WithCacheArgsFromStruct(req).
//       Build())
//
// ── 重要约定 ──────────────────────────────────────────────────────────────
//
//   - 同名 key 的覆盖顺序由调用链决定,与 map 的插入顺序无关
//     (因为内部 marshalSorted 会按 key 字典序排序后再 md5)
//   - 字段值必须可被 json.Marshal,否则 cache key 不稳定
//   - 结构体字段 / 切片元素的顺序会影响 key,跨版本改字段名要注意缓存兼容

// WithCacheArgs 追加 cache key 参数（变参形式）。
//
// 用于解决列表/分页查询中 Where 条件值无法自动进入 cache key 的问题:
// 业务方把影响结果的条件值显式声明出来,框架将其与模板默认 args 合并后参与 key 计算。
//
// 参数为 key-value 交替的变参,最后一个落单的 key 会被忽略。
// 多次调用 / 与 WithCacheArgsMap / WithCacheArgsFromStruct 混用时,
// 按链式顺序后写覆盖先写(详见上方组合规则)。
//
// 示例:
//
//	// 列表分页查询:不同 status 的列表得到不同缓存
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

// WithCacheArgsMap 追加 cache key 参数(map 形式)。
//
// 适合从 HTTP 请求 / 查询 DTO 等已有 map 结构直接灌入,避免手动展开成 kv 对。
// 多次调用 / 与 WithCacheArgs / WithCacheArgsFromStruct 混用时,
// 按链式顺序后写覆盖先写(详见三个方法顶部的组合规则注释)。
//
// 示例:
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
// 注意:传入的 map 值必须是可被 json.Marshal 的类型;如果是结构体/切片,
// 字段顺序 / 元素顺序会影响最终 key(因为 json 序列化保序)。
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

// WithCacheArgsFromStruct 把结构体字段自动展开为 cache args,方便 POST 分页 DTO 直接灌入。
//
// 使用场景:分页查询的 POST 请求里通常会绑定一个查询 DTO,业务方不希望手写一长串
// WithCacheArgs("page", req.Page, "size", req.Size, "status", req.Status, ...)
// 用本方法直接传 DTO 指针/值即可。
//
// 多次调用 / 与 WithCacheArgs / WithCacheArgsMap 混用时,
// 按链式顺序后写覆盖先写(详见三个方法顶部的组合规则注释)。
//
// 字段名取值规则:
//  1. 优先用 json tag 的第一段(如 `json:"user_name,omitempty"` → "user_name")
//  2. json tag 为 "-" 的字段跳过
//  3. 没有 json tag 的字段用结构体字段名本身(如 "UserName")
//
// 跳过规则:
//   - nil 指针字段跳过(避免污染 key)
//   - 字段值为零值时仍会进入 args(业务方需要语义一致性,所以保留)
//   - 嵌入字段会递归展开
//
// 示例(POST 分页查询):
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
// 注意:传入结构体的字段顺序、值类型必须稳定,跨版本改字段名会导致 cache key 漂移。
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

		if opt.Unscoped {
			result.Unscoped = true
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
