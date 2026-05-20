package gormplus

import (
	"time"

	"github.com/kuangshp/gorm-plus/sf"
)

// ================== SingleFlight + 可插拔缓存 ==================

// SFCache 可插拔缓存基础接口，实现后通过 RegisterCache 注入替换默认内存缓存。
//
// Redis 实现示例（务必使用 RawValue 包装返回字节流，否则类型断言会失败）：
//
//	type RedisSFCache struct {
//	    rdb    *redis.Client
//	    prefix string
//	}
//
//	func (c *RedisSFCache) Get(key string) (any, bool) {
//	    b, err := c.rdb.Get(context.Background(), c.prefix+key).Bytes()
//	    if err != nil { return nil, false }
//	    return gormplus.RawValue(b), true   // ← 关键：用 RawValue 标记字节流
//	}
//
//	func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
//	    b, err := json.Marshal(val)
//	    if err != nil { return }
//	    c.rdb.Set(context.Background(), c.prefix+key, b, ttl)
//	}
//
//	func (c *RedisSFCache) Del(key string) {
//	    c.rdb.Del(context.Background(), c.prefix+key)
//	}
//
//	gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})
type SFCache = sf.SFCache

// SFCachePrefixDeleter 可选接口（前缀失效能力）。
// 自定义缓存（如 Redis）实现此接口后，SFInvalidatePrefix 才会真正生效。
// 内置 MemoryCache 已实现，开箱即用。
//
// Redis 实现示例（务必用 SCAN 而非 KEYS，避免阻塞集群）：
//
//	func (c *RedisSFCache) DelByPrefix(prefix string) {
//	    ctx := context.Background()
//	    var cursor uint64
//	    for {
//	        keys, next, err := c.rdb.Scan(ctx, cursor, c.prefix+prefix+"*", 500).Result()
//	        if err != nil { return }
//	        if len(keys) > 0 { c.rdb.Del(ctx, keys...) }
//	        cursor = next
//	        if cursor == 0 { break }
//	    }
//	}
type SFCachePrefixDeleter = sf.SFCachePrefixDeleter

// SFCacheCloser 可选接口（资源释放能力）。
// 自定义缓存实现 Close() 后，StopSFCache 会自动调用，统一关闭入口。
//
// Redis 实现示例：
//
//	func (c *RedisSFCache) Close() error { return c.rdb.Close() }
type SFCacheCloser = sf.SFCacheCloser

// RawValue 标记缓存中存储的是字节流（已序列化的数据）。
//
// Redis 等外部缓存的 Get() 方法应返回 RawValue 而不是原始 any，
// sf 包识别后会自动用 json.Unmarshal 反序列化到业务期望的类型 T，
// 避免 raw.(T) 类型断言失败、缓存命中率永远为 0 的 bug。
//
// 内存缓存不需要 RawValue，存什么取什么，零开销，向后兼容。
//
// 详见 SFCache 注释里的 Redis 实现示例。
type RawValue = sf.RawValue

// MemoryCache 内置内存缓存实现，可显式创建后注册（方便单元测试替换）。
type MemoryCache = sf.MemoryCache

// DefaultSFTTL SF 不传 ttl 时的默认缓存时长（5 分钟）。
var DefaultSFTTL = sf.DefaultSFTTL

// RegisterCache 注册自定义缓存实现，**全局只能调用一次**，应在 main 函数早期完成。
// 注册后所有 SF / SFWithTTL / SFInvalidate / SFInvalidatePrefix 均使用此缓存。
//
// 行为规则：
//   - 首次调用：直接注册成功（即使内存缓存已懒加载也允许替换）
//   - 已注册过自定义缓存再次调用：panic，强制 fail-fast 暴露重复注册的 bug
//   - 传 nil：panic
//
// 运行期切换缓存的极少数场景（测试隔离、运维灰度）请使用 ForceReplaceCache。
//
// 方式一：内存缓存（默认，零配置）：
//
//	// 不调用 RegisterCache，SF 自动懒加载内存缓存
//	defer gormplus.StopSFCache()
//
// 方式二：Redis 缓存（多实例部署推荐）：
//
//	gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})
//	defer gormplus.StopSFCache()
func RegisterCache(c sf.SFCache) {
	sf.RegisterCache(c)
}

// ForceReplaceCache 强制替换全局缓存，无任何幂等检查。
//
// ⚠️ 危险操作：运行期替换缓存会导致并发 goroutine 读写到不同的缓存实例，
// 已经 Set 的数据可能丢失。一般业务请使用 RegisterCache（启动期一次性）。
//
// 仅适用于：
//   - 单元测试用例之间清理缓存状态
//   - 运维灰度切换缓存层（必须确认业务无 in-flight 请求）
//
// 单元测试示例：
//
//	func TestXxx(t *testing.T) {
//	    gormplus.ForceReplaceCache(gormplus.NewMemoryCache())
//	    defer gormplus.StopSFCache()
//	    // ... 测试逻辑
//	}
func ForceReplaceCache(c sf.SFCache) {
	sf.ForceReplaceCache(c)
}

// NewMemoryCache 显式创建内存缓存实例，适合单元测试替换默认缓存。
func NewMemoryCache() *sf.MemoryCache {
	return sf.NewMemoryCache()
}

// SF 通用 singleflight + 缓存查询封装，防止缓存击穿。
//
// 参数：
//   - fn:     实际查询函数，闭包原封不动放入，类型安全
//   - fnName: 查询唯一标识，建议格式 "表名.方法名"，如 "Account.List"
//   - args:   影响查询结果的所有参数；map key 自动排序后哈希，顺序无关
//   - ttl:    可选，缓存时长；不传时使用 DefaultSFTTL（5 分钟）；传 0 等价于 SFNoCache
//
// 使用示例：
//
//	list, err := gormplus.SF(func() ([]*model.Account, error) {
//	    var result []*model.Account
//	    err := gormplus.Query[*model.Account](db, ctx).
//	        WhereIf(status != 0, "status = ?", status).
//	        Build().Find(&result)
//	    return result, err
//	}, "Account.List", map[string]any{"status": status, "page": pageNum}, 30*time.Second)
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	return sf.SF(fn, fnName, args, ttl...)
}

// SFWithTTL 与 SF 相同，但 ttl 为必填参数，语义更明确，避免误用可变参默认值。
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	return sf.SFWithTTL(fn, fnName, args, ttl)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，不缓存结果。
// 适合详情接口、余额查询等对实时性要求高、不允许读到旧数据的场景。
//
// 使用示例：
//
//	account, err := gormplus.SFNoCache(func() (*model.Account, error) {
//	    var a model.Account
//	    err := db.WithContext(ctx).Where("id = ?", id).First(&a).Error
//	    return &a, err
//	}, "Account.Detail", map[string]any{"id": id})
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return sf.SFNoCache(fn, fnName, args)
}

// SFInvalidate 主动使指定查询的缓存立即失效（精确失效，args 必须完全一致）。
//
// 适用场景：FindById 这种 args 完全可预知的缓存。
//
// 使用示例：
//
//	func (s *AccountService) Update(ctx context.Context, id int64) error {
//	    if err := repo.Update(ctx, id); err != nil { return err }
//	    gormplus.SFInvalidate("Account.FindById", gormplus.BuildArgs("id", id))
//	    return nil
//	}
func SFInvalidate(fnName string, args map[string]any) {
	sf.SFInvalidate(fnName, args)
}

// SFInvalidatePrefix 按前缀批量失效缓存。
//
// 与 SFInvalidate 的区别：
//   - SFInvalidate(fnName, args)   精确失效，args 必须和查询时完全一致（适合 FindById）
//   - SFInvalidatePrefix(fnName)   前缀失效，清掉该 fnName 下所有 args 组合的缓存
//
// 适用场景：list / page / count / exists 这类 args 会因 Where 条件或业务参数变化的查询，
// 写操作（Create / Update / Delete）后无法精确知道哪些 args 被缓存了，用前缀一次清光。
//
// 示例（清掉某个方法的所有缓存）：
//
//	gormplus.SFInvalidatePrefix("sys_user.FindList")
//	gormplus.SFInvalidatePrefix("sys_user.FindPage")
//
// 示例（清掉整张表的所有缓存，注意尾部加点，避免误伤前缀相同的其他表）：
//
//	gormplus.SFInvalidatePrefix("sys_user.")
//
// 安全保护：fnName 为空或不含点号且过短时会被拒绝执行，避免误清所有缓存。
//
// 性能说明：
//   - 默认内存缓存：O(n) 全表扫描，写入压力不大时可接受
//   - Redis 缓存：需实现 SFCachePrefixDeleter，建议用 SCAN（非 KEYS）避免阻塞
//   - 未实现 SFCachePrefixDeleter 的自定义缓存：静默无操作
func SFInvalidatePrefix(fnName string) {
	sf.SFInvalidatePrefix(fnName)
}

// SFInvalidatePrefixes 批量按前缀失效缓存（一次调用清多个 fnName 的缓存）。
//
// 适用场景：写操作后需要一次性失效多个前缀。比如 Update 操作要清掉
// FindList / FindPage / Count / Exists 等多个前缀的缓存，用本函数比多次单独调用
// SFInvalidatePrefix 性能好得多——Redis 场景下从 N 次 SCAN 降为 1 次 pipeline。
//
// 执行优先级：
//   - 缓存实现了 SFCachePrefixBatchDeleter：走批量接口（最快）
//   - 缓存只实现了 SFCachePrefixDeleter：自动 fallback 为循环调用
//   - 都没实现：静默无操作
//
// 安全校验：每个 fnName 都过和 SFInvalidatePrefix 一样的过滤规则。
//
// 示例（在 service 层批量失效）：
//
//	gormplus.SFInvalidatePrefixes([]string{
//	    "sys_user.FindList",
//	    "sys_user.FindPage",
//	    "sys_user.Count",
//	    "sys_user.Exists",
//	})
func SFInvalidatePrefixes(fnNames []string) {
	sf.SFInvalidatePrefixes(fnNames)
}

// SFCachePrefixBatchDeleter 可选接口，支持批量按前缀删除 key。
// Redis 等外部缓存实现此接口可大幅减少 RTT（用 pipeline 一次处理多个前缀）。
// 内置 MemoryCache 已实现，开箱即用。
//
// 详细说明和 Redis 实现示例见 sf.SFCachePrefixBatchDeleter。
type SFCachePrefixBatchDeleter = sf.SFCachePrefixBatchDeleter

// SetCacheUnwrapErrorHandler 注入缓存反序列化失败钩子。
//
// 当 SF/SFWithTTL 取到缓存但还原成业务类型失败时（json.Unmarshal 报错 / 类型断言失败），
// 框架默认行为是降级到 fn() 重新查 DB，**对业务透明**。这意味着缓存可能悄悄失效，
// 生产环境难以察觉。注入此钩子可以监控这类异常：
//
//	gormplus.SetCacheUnwrapErrorHandler(func(key string, err error) {
//	    zap.L().Warn("cache unwrap failed",
//	        zap.String("key", key),
//	        zap.Error(err),
//	    )
//	    metrics.CacheUnwrapErrors.Inc()
//	})
//
// 触发场景：
//   - Redis 数据格式损坏（手动改过、跨版本结构变更）
//   - 缓存里存的类型和业务期望类型不匹配（极少见，通常是 bug）
//
// 注意：钩子可能在高频路径执行，实现要尽量快、不阻塞、不 panic。
// 钩子 panic 会被框架吞掉，不影响主流程。
//
// 传 nil 可清除已注入的钩子。
func SetCacheUnwrapErrorHandler(fn func(key string, err error)) {
	sf.OnUnwrapError = fn
}

// StopSFCache 统一关闭入口，应在应用退出时调用（推荐 defer）。
//
// 行为：
//   - 默认内存缓存（MemoryCache）：停掉后台过期清理 goroutine
//   - 自定义缓存实现了 SFCacheCloser：自动调用 Close() 释放资源（如 Redis 连接池）
//   - 两种场景都未匹配：no-op，不会报错
//
// 推荐用法（内存 / Redis 通用，业务代码无需关心底层实现）：
//
//	func main() {
//	    // 方式 A：内存缓存（零配置）
//	    // 方式 B：Redis 缓存
//	    // gormplus.RegisterCache(&RedisSFCache{rdb: rdb})
//
//	    defer gormplus.StopSFCache()   // 一行兼顾两种场景
//	    // ... 启动服务
//	}
func StopSFCache() error {
	return sf.StopSFCache()
}
