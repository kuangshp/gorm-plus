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

// ================== SingleFlight + 内存缓存 ==================
//
// 本文件提供三个层次的查询保护：
//
//  1. 【纯 singleflight】SFNoCache
//     同一瞬间多个并发请求只有一个真正打到 DB，其余等待并共享结果。
//     适合：详情接口、用户实时数据等对实时性要求高的场景。
//
//  2. 【singleflight + 内存缓存】SF / SFWithTTL
//     在纯 singleflight 基础上增加 TTL 缓存，TTL 内的重复请求直接返回缓存，
//     不再执行 DB 查询，显著减少数据库压力。
//     适合：列表、统计、配置、字典等读多写少的场景。
//
//  3. 【主动失效】SFInvalidate
//     写操作后主动清除对应缓存，避免数据不一致。
//
// ── 与 NewQuery 配合（原生 gorm） ──────────────────────────────
//
//	list, err := query.SF(func() ([]*model.Order, error) {
//	    var result []*model.Order
//	    err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("user_id", userID).
//	        EqIfNotZero("status", status).
//	        OrderByDesc("created_at").
//	        Page(pageNum, pageSize).
//	        Find(&result)
//	    return result, err
//	}, "Order.List", map[string]any{
//	    "user_id": userID,
//	    "status":  status,
//	    "page":    pageNum,
//	    "size":    pageSize,
//	})
//
// ── 与 GenWrap 配合（gorm-gen 类型安全） ──────────────────────
//
//	list, err := query.SF(func() ([]InterviewVO, error) {
//	    var result []InterviewVO
//	    err := query.GenWrap(dao.InterviewEntity.WithContext(ctx)).
//	        Always(dao.InterviewEntity.Status.Eq(int64(1))).
//	        EqIfNotZero(dao.InterviewEntity.DeptID.Eq(deptID), deptID).
//	        InIfNotEmpty(dao.InterviewEntity.ID.In(ids...), ids).
//	        Apply().Scan(&result)
//	    return result, err
//	}, "Interview.List", map[string]any{
//	    "dept_id": deptID,
//	    "ids":     ids,
//	}, 5*time.Second)
//
// ── 写后失效 ──────────────────────────────────────────────────
//
//	func UpdateOrder(ctx context.Context, userID int64, ...) error {
//	    // ... 执行更新
//	    query.SFInvalidate("Order.List", map[string]any{"user_id": userID})
//	    return nil
//	}
//
// ── TTL 选择建议 ──────────────────────────────────────────────
//
//	列表/统计（允许短暂延迟）  → 3s ~ 30s
//	配置/字典（几乎不变）      → 1min ~ 5min（DefaultSFTTL）
//	详情/用户实时数据          → 0 或 SFNoCache
//
// ── 应用退出 ──────────────────────────────────────────────────
//
//	defer query.StopSFCache() // 停止后台清理 goroutine，避免泄漏

// DefaultSFTTL SF 不传 ttl 时使用的默认缓存时长（5 分钟）。
// 适合配置、字典等几乎不变的数据。对实时性要求较高的场景请显式传 ttl。
const DefaultSFTTL = 5 * time.Minute

// ── sfGroup：singleflight 实现 ────────────────────────────────
//
// 自行实现而非使用 golang.org/x/sync/singleflight，原因：
//  1. 避免泄漏 singleflight.Result 类型到调用方
//  2. 泛型 SF 函数需要自定义类型断言逻辑

var sfg sfGroup

// sfGroup 并发安全的 singleflight 调度器
type sfGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

// sfCall 代表一个正在执行的调用，等待中的 goroutine 共享同一个 sfCall
type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// Do 确保相同 key 的并发调用只有一个真正执行，其余等待并复用结果。
// 调用完成后立即从 map 中删除，下一次调用会重新执行（不缓存）。
func (g *sfGroup) Do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	if c, ok := g.m[key]; ok {
		// 已有相同 key 的调用正在执行，等待其完成并复用结果
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &sfCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	// 当前 goroutine 真正执行查询
	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}

// ── 本地内存缓存 ──────────────────────────────────────────────

// sfCache 全局缓存实例（懒初始化，避免 import 包时意外启动 goroutine）
var (
	sfCacheOnce sync.Once
	sfCacheInst *sfLocalCache
)

// getSFCache 获取全局缓存实例，首次调用时初始化并启动后台清理。
func getSFCache() *sfLocalCache {
	sfCacheOnce.Do(func() {
		sfCacheInst = newSFLocalCache()
	})
	return sfCacheInst
}

// StopSFCache 停止后台过期清理 goroutine，应在应用退出时调用。
//
// 示例：
//
//	func main() {
//	    defer query.StopSFCache()
//	    // ... 启动服务
//	}
func StopSFCache() {
	sfCacheOnce.Do(func() {}) // 确保 once 已触发，避免 cancel 为 nil
	if sfCacheInst != nil {
		sfCacheInst.cancel()
	}
}

type sfCacheItem struct {
	val      any
	expireAt time.Time
}

// sfLocalCache 内存缓存，使用 sync.Map 保证并发安全
type sfLocalCache struct {
	m      sync.Map
	cancel context.CancelFunc // 用于停止后台清理 goroutine
}

// newSFLocalCache 创建缓存并启动后台定时清理（每 30 秒扫描一次过期 key）
func newSFLocalCache() *sfLocalCache {
	ctx, cancel := context.WithCancel(context.Background())
	c := &sfLocalCache{cancel: cancel}
	go c.cleanLoop(ctx)
	return c
}

func (c *sfLocalCache) get(key string) (any, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return nil, false
	}
	item := v.(*sfCacheItem)
	if time.Now().After(item.expireAt) {
		c.m.Delete(key) // 惰性删除：读到过期 key 时顺手删除
		return nil, false
	}
	return item.val, true
}

func (c *sfLocalCache) set(key string, val any, ttl time.Duration) {
	c.m.Store(key, &sfCacheItem{val: val, expireAt: time.Now().Add(ttl)})
}

// del 主动删除指定 key 的缓存（供 SFInvalidate 使用）
func (c *sfLocalCache) del(key string) {
	c.m.Delete(key)
}

// cleanLoop 后台定时扫描并删除过期缓存项
func (c *sfLocalCache) cleanLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.m.Range(func(k, v any) bool {
				if now.After(v.(*sfCacheItem).expireAt) {
					c.m.Delete(k)
				}
				return true
			})
		case <-ctx.Done():
			return // StopSFCache() 被调用时退出
		}
	}
}

// ── 公开 SF 函数 ──────────────────────────────────────────────

// SF 通用 singleflight + 内存缓存查询封装（最常用入口）。
//
// 参数：
//   - fn:     实际查询函数，原封不动放入闭包即可，类型安全
//   - fnName: 查询唯一标识，建议格式 "表名.方法名"，如 "Order.List"
//   - args:   影响查询结果的所有参数（分页、筛选条件等）
//     map key 自动排序后序列化为 cache key，{"a":1,"b":2} 与 {"b":2,"a":1} 视为同一查询
//   - ttl:    可选，缓存时长；不传时使用 DefaultSFTTL（5分钟）；传 0 等价于 SFNoCache
//
// 示例（不传 ttl，使用默认 5 分钟）：
//
//	list, err := query.SF(func() ([]*model.Order, error) {
//	    var result []*model.Order
//	    err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("user_id", userID).
//	        Find(&result)
//	    return result, err
//	}, "Order.List", map[string]any{"user_id": userID, "page": pageNum, "size": pageSize})
//
// 示例（显式传 ttl）：
//
//	list, err := query.SF(fn, "Order.List", args, 30*time.Second)
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
// 效果：假设同一时刻 100 个请求查询同一个订单，只有 1 个真正执行 DB 查询，
//
//	其余 99 个等待并复用结果。查询完成后 key 立即释放，下次请求会重新查询。
//
// 示例：
//
//	order, err := query.SFNoCache(func() (*model.Order, error) {
//	    var o model.Order
//	    err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("id", orderID).
//	        First(&o)
//	    return &o, err
//	}, "Order.Detail", map[string]any{"id": orderID})
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return SFWithTTL(fn, fnName, args, 0)
}

// SFInvalidate 主动使指定查询的缓存立即失效。
//
// 写操作（Create / Update / Delete）后调用，避免后续读取到旧缓存数据。
// args 需要与查询时传入的完全一致（key-value 相同），顺序无关。
//
// 示例：
//
//	// 更新订单状态后，使该用户的订单列表缓存失效
//	func (s *OrderService) UpdateStatus(ctx context.Context, userID int64, ...) error {
//	    if err := repo.Update(ctx, ...); err != nil {
//	        return err
//	    }
//	    // 失效缓存（args 需与 SF 查询时一致）
//	    query.SFInvalidate("Order.List", map[string]any{"user_id": userID})
//	    return nil
//	}
func SFInvalidate(fnName string, args map[string]any) {
	key := buildSFKey(fnName, args)
	getSFCache().del(key)
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
//
// 参数：
//   - fn:     实际查询函数
//   - fnName: 查询唯一标识
//   - args:   查询参数（用于构建 cache key）
//   - ttl:    0=不缓存只合并并发，>0=缓存指定时长
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	key := buildSFKey(fnName, args)
	cache := getSFCache()

	// 步骤 2：缓存快速路径（Do 外部，避免不必要的加锁）
	if ttl > 0 {
		if cached, ok := cache.get(key); ok {
			if result, ok := cached.(T); ok {
				return result, nil
			}
		}
	}

	// 步骤 3-5：singleflight 保护
	raw, err := sfg.Do(key, func() (any, error) {
		// 步骤 4：Do 内部二次查缓存（防止等待期间已由其他 goroutine 写入）
		if ttl > 0 {
			if cached, ok := cache.get(key); ok {
				return cached, nil
			}
		}
		// 步骤 5：真正执行查询
		result, err := fn()
		if err == nil && ttl > 0 {
			cache.set(key, result, ttl)
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

// ── cache key 构建 ────────────────────────────────────────────

// buildSFKey 将 fnName + args 构建为确定性字符串 key。
//
// key 格式：sf:{fnName}:{md5(sorted_json(args))}
//
// map 的 key 按字母序排序后再 JSON 序列化，保证：
//   - {"a":1,"b":2} 与 {"b":2,"a":1} 产生相同的 key
//   - 相同参数永远映射到相同的 cache key
func buildSFKey(fnName string, args map[string]any) string {
	if len(args) == 0 {
		return fmt.Sprintf("sf:%s:noargs", fnName)
	}
	b, err := marshalSorted(args)
	if err != nil {
		// 序列化失败时降级到 fmt.Sprintf（map 顺序不稳定，但总比崩溃强）
		fallback := fmt.Sprintf("%v", args)
		hash := md5.Sum([]byte(fallback))
		return fmt.Sprintf("sf:%s:%x", fnName, hash)
	}
	hash := md5.Sum(b)
	return fmt.Sprintf("sf:%s:%x", fnName, hash)
}

// marshalSorted 将 map 按 key 字母序排列后序列化为 JSON 字节。
// 用于构建稳定的 cache key，避免 map 随机迭代顺序带来的 key 不一致问题。
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
