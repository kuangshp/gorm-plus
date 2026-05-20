package sf

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ================== SingleFlight + 可插拔缓存 ==================
//
// 本文件提供三个层次的查询保护：
//
//  1. 【纯 singleflight】SFNoCache
//     同一瞬间多个并发请求只有一个真正打到 DB，其余等待并共享结果。
//     适合：详情接口、用户实时数据等对实时性要求高的场景。
//
//  2. 【singleflight + 可插拔缓存】SF / SFWithTTL
//     在纯 singleflight 基础上增加缓存，TTL 内的重复请求直接返回缓存。
//     缓存实现由用户决定：默认内存缓存，也可注入 Redis / Memcached 等。
//
//  3. 【主动失效】SFInvalidate / SFInvalidatePrefix
//     - SFInvalidate         精确失效：写操作后清除指定 args 的缓存。
//     - SFInvalidatePrefix   前缀失效：清除某方法/某表下所有缓存（list/page/count/exists 写场景）。
//
// ── 缓存接口注册 ──────────────────────────────────────────────
//
//	// 默认使用内存缓存，无需任何配置即可使用
//	list, err := sf.SF(fn, "Order.List", args, 30*time.Second)
//
//	// 注册 Redis 缓存（实现 SFCache 接口后注入）
//	sf.RegisterCache(myRedisCache)
//
// ── TTL 选择建议 ──────────────────────────────────────────────
//
//	列表/统计（允许短暂延迟）  → 3s ~ 30s
//	配置/字典（几乎不变）      → 1min ~ 5min（DefaultSFTTL）
//	详情/用户实时数据          → 0 或 SFNoCache

// DefaultSFTTL SF 不传 ttl 时使用的默认缓存时长（5 分钟）。
const DefaultSFTTL = 5 * time.Minute

// keyPrefix 全局 cache key 前缀，统一便于 Redis 实现按前缀扫描。
const keyPrefix = "sf:"

// ================== 缓存接口 ==================

// SFCache 可插拔缓存基础接口（保持向后兼容，不强制实现 DelByPrefix）。
// 实现此接口后通过 RegisterCache 注入，替换默认的内存缓存。
//
// 接口约定：
//   - Get：key 存在且未过期返回 (value, true)；不存在或已过期返回 (nil, false)
//   - Set：存储 key-value，ttl 后自动过期
//   - Del：主动删除指定 key（供 SFInvalidate 使用）
//
// ⚠️ 若需要支持 SFInvalidatePrefix（list/page/count/exists 写场景的前缀失效），
// 请额外实现可选接口 SFCachePrefixDeleter（见下方）。
// 不实现也不会编译报错，但 SFInvalidatePrefix 调用会被静默忽略（日志友好提示由用户自行加）。
type SFCache interface {
	Get(key string) (any, bool)
	Set(key string, val any, ttl time.Duration)
	Del(key string)
}

// SFCachePrefixDeleter 可选接口：支持按前缀批量删除 key。
// 实现此接口后，SFInvalidatePrefix 才会真正生效；否则调用是无操作。
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
//
// 内置 MemoryCache 已实现此接口，开箱即用。
type SFCachePrefixDeleter interface {
	DelByPrefix(prefix string)
}

// SFCacheCloser 可选接口：缓存资源释放能力。
// 实现此接口后，StopSFCache 会自动调用 Close()，统一关闭入口。
//
// 适用场景：Redis 客户端、数据库连接等需要显式释放资源的缓存实现。
//
// Redis 实现示例：
//
//	func (c *RedisSFCache) Close() error {
//	    return c.rdb.Close()
//	}
//
// 用户侧使用：
//
//	func main() {
//	    gormplus.RegisterCache(&RedisSFCache{rdb: rdb})
//	    defer gormplus.StopSFCache()   // 自动调用 Close()，关闭 Redis 连接
//	    // ...
//	}
//
// 内置 MemoryCache 不需要 Close（用 Stop 停后台 goroutine），StopSFCache 会自动处理。
type SFCacheCloser interface {
	Close() error
}

// RawValue 标记缓存中存储的是字节流（已序列化的数据）。
//
// ── 解决的问题 ────────────────────────────────────────────────
//
// SFCache 接口的 Get/Set 用 any 传值。内存缓存场景下"存什么取什么"，类型断言天然成立；
// 但 Redis 等外部缓存必须经过序列化：Set 时把 *model.UserEntity 序列化成 []byte 写入，
// Get 时反序列化出来的 any 通常是 map[string]any，导致 sf 包内部 raw.(T) 断言失败，
// 缓存命中率永远为 0，且业务无感知（每次都打 DB）。
//
// ── 工作机制 ──────────────────────────────────────────────────
//
// 自定义缓存（Redis 等）的 Get() 返回 RawValue(b) 而不是 any(b)，
// sf 包识别到 RawValue 后会自动用 json.Unmarshal 反序列化到目标类型 T，
// 既不破坏 SFCache 接口签名，也保证类型安全。
//
// 内存缓存不需要序列化，依然直接存 T，零开销，向后兼容。
//
// ── Redis 实现示例 ────────────────────────────────────────────
//
//	type RedisSFCache struct {
//	    rdb    *redis.Client
//	    prefix string
//	}
//
//	func (c *RedisSFCache) Get(key string) (any, bool) {
//	    b, err := c.rdb.Get(context.Background(), c.prefix+key).Bytes()
//	    if err != nil {
//	        return nil, false
//	    }
//	    return sf.RawValue(b), true   // ← 关键：用 RawValue 包装字节流
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
type RawValue []byte

// ── 全局缓存注册 ──────────────────────────────────────────────

var (
	globalCacheMu sync.RWMutex
	globalCache   SFCache
)

// RegisterCache 注册自定义缓存实现，程序启动时调用一次。
// 注册后所有 SF / SFWithTTL / SFInvalidate / SFInvalidatePrefix 均使用此缓存，替代默认内存缓存。
//
// 注意：必须在第一次调用 SF 之前注册，否则已懒初始化的内存缓存不会被替换。
func RegisterCache(c SFCache) {
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	globalCache = c
}

// getCache 获取当前缓存实例，未注册时懒初始化为内存缓存。
func getCache() SFCache {
	globalCacheMu.RLock()
	if globalCache != nil {
		defer globalCacheMu.RUnlock()
		return globalCache
	}
	globalCacheMu.RUnlock()

	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	if globalCache == nil {
		globalCache = newMemoryCache()
	}
	return globalCache
}

// ================== 内置内存缓存实现 ==================

// MemoryCache 内置内存缓存，实现 SFCache 接口。
type MemoryCache struct {
	m      sync.Map
	cancel context.CancelFunc
}

// NewMemoryCache 创建内存缓存实例并启动后台过期清理（每 30 秒扫描一次）。
func NewMemoryCache() *MemoryCache {
	return newMemoryCache()
}

func newMemoryCache() *MemoryCache {
	ctx, cancel := context.WithCancel(context.Background())
	c := &MemoryCache{cancel: cancel}
	go c.cleanLoop(ctx)
	return c
}

type memoryCacheItem struct {
	val      any
	expireAt time.Time
}

func (c *MemoryCache) Get(key string) (any, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return nil, false
	}
	item := v.(*memoryCacheItem)
	if time.Now().After(item.expireAt) {
		c.m.Delete(key)
		return nil, false
	}
	return item.val, true
}

func (c *MemoryCache) Set(key string, val any, ttl time.Duration) {
	c.m.Store(key, &memoryCacheItem{val: val, expireAt: time.Now().Add(ttl)})
}

func (c *MemoryCache) Del(key string) {
	c.m.Delete(key)
}

// DelByPrefix 删除所有以 prefix 开头的 key。
// 内存实现是 O(n) 全表扫描，对大数据量场景请使用 Redis 等支持 SCAN 的缓存。
func (c *MemoryCache) DelByPrefix(prefix string) {
	c.m.Range(func(k, _ any) bool {
		if ks, ok := k.(string); ok && strings.HasPrefix(ks, prefix) {
			c.m.Delete(k)
		}
		return true
	})
}

// Stop 停止后台过期清理 goroutine，应在应用退出时调用。
func (c *MemoryCache) Stop() {
	c.cancel()
}

func (c *MemoryCache) cleanLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.m.Range(func(k, v any) bool {
				if now.After(v.(*memoryCacheItem).expireAt) {
					c.m.Delete(k)
				}
				return true
			})
		case <-ctx.Done():
			return
		}
	}
}

// StopSFCache 统一关闭入口，应在应用退出时调用（推荐 defer）。
//
// 行为：
//   - 默认内存缓存（MemoryCache）：停掉后台过期清理 goroutine
//   - 自定义缓存实现了 SFCacheCloser：自动调用 Close() 释放资源
//   - 都没匹配：no-op，不会报错
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
//
// 返回值：自定义缓存 Close() 的错误（内存缓存场景固定返回 nil）。
func StopSFCache() error {
	globalCacheMu.RLock()
	c := globalCache
	globalCacheMu.RUnlock()

	if c == nil {
		return nil
	}

	// 内存缓存：停后台清理 goroutine
	if mc, ok := c.(*MemoryCache); ok {
		mc.Stop()
	}

	// 任何实现了 Close() 的缓存：自动调用（Redis 客户端等）
	if closer, ok := c.(SFCacheCloser); ok {
		return closer.Close()
	}
	return nil
}

// ================== singleflight（官方库） ==================

var sfg singleflight.Group

// ================== 公开 SF 函数 ==================

// SF 通用 singleflight + 缓存查询封装（最常用入口）。
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	t := DefaultSFTTL
	if len(ttl) > 0 {
		t = ttl[0]
	}
	return SFWithTTL(fn, fnName, args, t)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，完成后立即释放，不缓存结果。
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return SFWithTTL(fn, fnName, args, 0)
}

// SFInvalidate 主动使指定查询的缓存立即失效（精确失效，args 必须完全一致）。
//
// 适用场景：FindById 这种 args 完全可预知的缓存。
//
// 示例：
//
//	sf.SFInvalidate("sys_user.FindById", map[string]any{"id": int64(1)})
func SFInvalidate(fnName string, args map[string]any) {
	key := buildSFKey(fnName, args)
	getCache().Del(key)
}

// SFInvalidatePrefix 按前缀批量失效缓存（前缀失效，不需要知道具体 args）。
//
// 适用场景：list/page/count/exists 类查询的 args 因 Where 条件 / 业务参数变化而无法穷举，
// 写操作后需要把整张表的列表/统计缓存全部清掉。
//
// 入参支持两种粒度：
//
//	// ① 清掉某个方法的所有缓存（推荐，粒度更细）
//	sf.SFInvalidatePrefix("sys_user.FindList")
//	sf.SFInvalidatePrefix("sys_user.FindPage")
//
//	// ② 清掉整张表的所有缓存（必须带点号结尾，避免误伤 sys_user_role 等）
//	sf.SFInvalidatePrefix("sys_user.")
//
// 安全校验：
//   - fnName 为空字符串：直接返回，避免误清所有缓存
//   - fnName 不含点号且短于 3 字符：视为可疑前缀，直接返回
//
// 实现说明：
//   - 内存缓存：O(n) 扫描，写入压力不大的场景没问题
//   - Redis 缓存：用户实现 SFCachePrefixDeleter 时建议用 SCAN 而非 KEYS 避免阻塞
//   - 自定义缓存未实现 SFCachePrefixDeleter 时静默忽略（业务不会报错，但缓存不会清）
func SFInvalidatePrefix(fnName string) {
	// 安全校验：拒绝可疑的空 / 过短前缀，避免误操作清空整个缓存
	if fnName == "" {
		return
	}
	// 不含点号且短于 3 字符的，多半是误用（合法格式应为 "tableName.MethodName" 或 "tableName."）
	if len(fnName) < 3 && !strings.Contains(fnName, ".") {
		return
	}

	c := getCache()
	if pd, ok := c.(SFCachePrefixDeleter); ok {
		// 与 buildSFKey 保持一致：noargs 的 key 也以 "sf:{fnName}:" 开头，
		// 用同样的前缀即可一次清干净。
		pd.DelByPrefix(keyPrefix + fnName + ":")
	}
	// 未实现 SFCachePrefixDeleter 时静默忽略（保持向后兼容，业务可继续运行）。
}

// SFWithTTL 通用 singleflight + 缓存封装，手动指定缓存时长（底层实现）。
//
// 类型安全机制：
//   - 内存缓存（MemoryCache）：直接存 T 取 T，类型断言 raw.(T) 必成立，零开销
//   - 外部缓存（Redis 等）：Get 返回 RawValue 标记字节流，sf 包自动 json 反序列化到 T
//
// 缓存命中场景的执行路径：
//
//	┌─ 内存缓存 ────────→ raw.(T) 直接返回
//	│
//	└─ Redis 缓存 ─────→ raw.(RawValue) → json.Unmarshal → 返回 T
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	key := buildSFKey(fnName, args)
	cache := getCache()

	// 步骤 1：缓存快速路径
	if ttl > 0 {
		if cached, ok := cache.Get(key); ok {
			if result, ok := unwrapCached[T](cached); ok {
				return result, nil
			}
		}
	}

	// 步骤 2：singleflight 保护
	raw, err, _ := sfg.Do(key, func() (any, error) {
		// Do 内部二次查缓存（防止等待期间已由其他 goroutine 写入）
		if ttl > 0 {
			if cached, ok := cache.Get(key); ok {
				return cached, nil
			}
		}
		// 真正执行查询
		result, err := fn()
		if err == nil && ttl > 0 {
			cache.Set(key, result, ttl)
		}
		if ttl == 0 {
			sfg.Forget(key)
		}
		return result, err
	})

	if err != nil {
		var zero T
		return zero, err
	}

	// 步骤 3：类型还原（支持 RawValue 反序列化）
	result, ok := unwrapCached[T](raw)
	if !ok {
		var zero T
		return zero, fmt.Errorf("SF: 类型还原失败 key=%s, 期望 %s 实际 %s",
			key, reflect.TypeOf(zero), reflect.TypeOf(raw))
	}
	return result, nil
}

// unwrapCached 把缓存返回的 any 还原成业务期望的类型 T。
//
// 处理两种 case：
//  1. 内存缓存：raw 本身就是 T（或可断言为 T），直接断言成功返回
//  2. 外部缓存（Redis 等）：raw 是 RawValue（已序列化字节），用 json.Unmarshal 还原
//
// 注意：RawValue 必须在断言 T 之前判断——如果 T 恰好是 []byte，会优先走 RawValue 分支
// 反序列化（json 把 []byte 当 base64 字符串处理），这是符合预期的：业务上 T=[]byte
// 的缓存值本来就该自己处理序列化。
func unwrapCached[T any](raw any) (T, bool) {
	var zero T

	// 优先识别 RawValue（Redis 等外部缓存的协议）
	if rv, isRaw := raw.(RawValue); isRaw {
		var t T
		if err := json.Unmarshal(rv, &t); err != nil {
			return zero, false
		}
		return t, true
	}

	// 普通类型断言（内存缓存路径）
	if t, ok := raw.(T); ok {
		return t, true
	}

	return zero, false
}

// ================== cache key 构建 ==================

// buildSFKey 将 fnName + args 构建为确定性字符串 key。
// key 格式：sf:{fnName}:{md5(sorted_json(args))}    （有 args）
// key 格式：sf:{fnName}:noargs                       （无 args）
//
// 注意：SFInvalidatePrefix 依赖 "sf:{fnName}:" 这个公共前缀，修改 key 格式时要同步调整。
func buildSFKey(fnName string, args map[string]any) string {
	if len(args) == 0 {
		return fmt.Sprintf("%s%s:noargs", keyPrefix, fnName)
	}
	b, err := marshalSorted(args)
	if err != nil {
		fallback := fmt.Sprintf("%v", args)
		hash := md5.Sum([]byte(fallback))
		return fmt.Sprintf("%s%s:%x", keyPrefix, fnName, hash)
	}
	hash := md5.Sum(b)
	return fmt.Sprintf("%s%s:%x", keyPrefix, fnName, hash)
}

// marshalSorted 将 map 按 key 字母序排列后序列化为 JSON 字节。
func marshalSorted(m map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
