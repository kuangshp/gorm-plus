package sf

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"
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
//  3. 【主动失效】SFInvalidate
//     写操作后主动清除对应缓存，避免数据不一致。
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

// ================== 缓存接口 ==================

// SFCache 可插拔缓存接口。
// 实现此接口后通过 RegisterCache 注入，替换默认的内存缓存。
//
// 接口约定：
//   - Get：key 存在且未过期返回 (value, true)；不存在或已过期返回 (nil, false)
//   - Set：存储 key-value，ttl 后自动过期
//   - Del：主动删除指定 key（供 SFInvalidate 使用）
//
// Redis 实现示例：
//
//	type RedisSFCache struct {
//	    rdb *redis.Client
//	}
//
//	func (c *RedisSFCache) Get(key string) (any, bool) {
//	    val, err := c.rdb.Get(context.Background(), key).Bytes()
//	    if err != nil {
//	        return nil, false
//	    }
//	    var result any
//	    if err := json.Unmarshal(val, &result); err != nil {
//	        return nil, false
//	    }
//	    return result, true
//	}
//
//	func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
//	    b, _ := json.Marshal(val)
//	    c.rdb.Set(context.Background(), key, b, ttl)
//	}
//
//	func (c *RedisSFCache) Del(key string) {
//	    c.rdb.Del(context.Background(), key)
//	}
//
//	// 注册（程序启动时调用一次）
//	sf.RegisterCache(&RedisSFCache{rdb: rdb})
type SFCache interface {
	// Get 读取缓存，key 存在且未过期返回 (value, true)，否则返回 (nil, false)
	Get(key string) (any, bool)

	// Set 写入缓存，ttl 后自动过期
	Set(key string, val any, ttl time.Duration)

	// Del 主动删除指定 key 的缓存
	Del(key string)
}

// ── 全局缓存注册 ──────────────────────────────────────────────

var (
	globalCacheMu sync.RWMutex
	globalCache   SFCache // 全局缓存实例，默认 nil（懒初始化为内存缓存）
)

// RegisterCache 注册自定义缓存实现，程序启动时调用一次。
// 注册后所有 SF / SFWithTTL / SFInvalidate 均使用此缓存，替代默认内存缓存。
//
// 注意：必须在第一次调用 SF 之前注册，否则已懒初始化的内存缓存不会被替换。
//
// 内存缓存示例（默认，无需注册）：
//
//	// 不注册任何缓存，SF 自动使用内置内存缓存
//	list, err := sf.SF(fn, "Order.List", args)
//
// Redis 缓存示例：
//
//	sf.RegisterCache(&RedisSFCache{rdb: rdb})
//	list, err := sf.SF(fn, "Order.List", args, 30*time.Second)
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

	// 升级为写锁，初始化内存缓存
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	if globalCache == nil {
		globalCache = newMemoryCache()
	}
	return globalCache
}

// ================== 内置内存缓存实现 ==================

// MemoryCache 内置内存缓存，实现 SFCache 接口。
// 默认使用，也可显式创建后注册（方便单元测试替换）。
//
// 示例（显式注册内存缓存）：
//
//	sf.RegisterCache(sf.NewMemoryCache())
type MemoryCache struct {
	m      sync.Map
	cancel context.CancelFunc
}

// NewMemoryCache 创建内存缓存实例并启动后台过期清理（每 30 秒扫描一次）。
// 通常不需要手动创建，SF 未注册缓存时会自动使用内置内存缓存。
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
		c.m.Delete(key) // 惰性删除
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

// Stop 停止后台过期清理 goroutine，应在应用退出时调用。
// 如果使用默认内存缓存，通过 StopSFCache() 停止即可。
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

// StopSFCache 停止内置内存缓存的后台清理 goroutine，应在应用退出时调用。
// 如果使用自定义缓存（Redis 等），由用户自行管理生命周期，此函数无需调用。
//
// 示例：
//
//	func main() {
//	    defer sf.StopSFCache()
//	    // ... 启动服务
//	}
func StopSFCache() {
	globalCacheMu.RLock()
	c := globalCache
	globalCacheMu.RUnlock()

	if mc, ok := c.(*MemoryCache); ok {
		mc.Stop()
	}
}

// ================== singleflight 实现 ==================

var sfg sfGroup

type sfGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// Do 确保相同 key 的并发调用只有一个真正执行，其余等待并复用结果。
func (g *sfGroup) Do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &sfCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}

// ================== 公开 SF 函数 ==================

// SF 通用 singleflight + 缓存查询封装（最常用入口）。
//
// 参数：
//   - fn:     实际查询函数，原封不动放入闭包即可，类型安全
//   - fnName: 查询唯一标识，建议格式 "表名.方法名"，如 "Order.List"
//   - args:   影响查询结果的所有参数（分页、筛选条件等）
//     map key 自动排序后序列化为 cache key，{"a":1,"b":2} 与 {"b":2,"a":1} 视为同一查询
//   - ttl:    可选，缓存时长；不传时使用 DefaultSFTTL（5分钟）；传 0 等价于 SFNoCache
//
// 缓存默认使用内存缓存，注册 Redis 后自动切换：
//
//	// 内存缓存（默认）
//	list, err := sf.SF(fn, "Order.List", args, 30*time.Second)
//
//	// Redis 缓存（注册后自动生效）
//	sf.RegisterCache(&RedisSFCache{rdb: rdb})
//	list, err := sf.SF(fn, "Order.List", args, 30*time.Second)
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	t := DefaultSFTTL
	if len(ttl) > 0 {
		t = ttl[0]
	}
	return SFWithTTL(fn, fnName, args, t)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，完成后立即释放，不缓存结果。
//
// 适合：详情接口、用户余额、敏感数据等对实时性要求高、不允许读到旧数据的场景。
//
// 示例：
//
//	account, err := sf.SFNoCache(func() (*model.Account, error) {
//	    var a model.Account
//	    err := db.WithContext(ctx).Where("id = ?", id).First(&a).Error
//	    return &a, err
//	}, "Account.Detail", map[string]any{"id": id})
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return SFWithTTL(fn, fnName, args, 0)
}

// SFInvalidate 主动使指定查询的缓存立即失效。
// 写操作（Create / Update / Delete）后调用，避免后续读取到旧缓存数据。
// args 需要与查询时传入的完全一致（key-value 相同，顺序无关）。
//
// 示例：
//
//	func (s *AccountService) Update(ctx context.Context, id int64) error {
//	    if err := repo.Update(ctx, id); err != nil {
//	        return err
//	    }
//	    sf.SFInvalidate("Account.List", map[string]any{"status": 1})
//	    return nil
//	}
func SFInvalidate(fnName string, args map[string]any) {
	key := buildSFKey(fnName, args)
	getCache().Del(key)
}

// SFWithTTL 通用 singleflight + 缓存封装，手动指定缓存时长（底层实现）。
//
// 执行流程：
//  1. 用 fnName + args 构建确定性 cache key（map key 自动排序）
//  2. TTL>0 时先查缓存，命中则直接返回（最快路径）
//  3. 进入 singleflight Do：同一 key 只有一个 goroutine 真正执行
//  4. Do 内部再查一次缓存（防止等待期间其他 goroutine 已写入）
//  5. 执行 fn()，成功且 TTL>0 时写入缓存
//  6. 类型断言后返回结果
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	key := buildSFKey(fnName, args)
	cache := getCache()

	// 步骤 2：缓存快速路径
	if ttl > 0 {
		if cached, ok := cache.Get(key); ok {
			if result, ok := cached.(T); ok {
				return result, nil
			}
		}
	}

	// 步骤 3-5：singleflight 保护
	raw, err := sfg.Do(key, func() (any, error) {
		// 步骤 4：Do 内部二次查缓存（防止等待期间已由其他 goroutine 写入）
		if ttl > 0 {
			if cached, ok := cache.Get(key); ok {
				return cached, nil
			}
		}
		// 步骤 5：真正执行查询
		result, err := fn()
		if err == nil && ttl > 0 {
			cache.Set(key, result, ttl)
		}
		return result, err
	})

	if err != nil {
		var zero T
		return zero, err
	}

	// 步骤 6：类型断言
	result, ok := raw.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("SF: 类型断言失败 key=%s, 期望 %s 实际 %s",
			key, reflect.TypeOf(zero), reflect.TypeOf(raw))
	}
	return result, nil
}

// ================== cache key 构建 ==================

// buildSFKey 将 fnName + args 构建为确定性字符串 key。
// key 格式：sf:{fnName}:{md5(sorted_json(args))}
func buildSFKey(fnName string, args map[string]any) string {
	if len(args) == 0 {
		return fmt.Sprintf("sf:%s:noargs", fnName)
	}
	b, err := marshalSorted(args)
	if err != nil {
		fallback := fmt.Sprintf("%v", args)
		hash := md5.Sum([]byte(fallback))
		return fmt.Sprintf("sf:%s:%x", fnName, hash)
	}
	hash := md5.Sum(b)
	return fmt.Sprintf("sf:%s:%x", fnName, hash)
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
