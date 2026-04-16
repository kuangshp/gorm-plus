package query

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

// ================== 常量 ==================

const (
	// DefaultSFTTL SF 默认缓存时长，不传 ttl 时使用。
	DefaultSFTTL = 5 * time.Minute
)

// ================== singleflight + 短暂内存缓存 ==================

var sfg sfGroup
var sfCache = newSFLocalCache()

// sfGroup 封装 singleflight，避免直接依赖 golang.org/x/sync/singleflight 包类型泄漏到外层
type sfGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// Do 合并相同 key 的并发调用，同一时刻只有一个真正执行
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

// ================== 本地缓存 ==================

type sfCacheItem struct {
	val      any
	expireAt time.Time
}

type sfLocalCache struct {
	m   sync.Map
	ctx context.Context
}

func newSFLocalCache() *sfLocalCache {
	c := &sfLocalCache{ctx: context.Background()}
	go c.cleanLoop()
	return c
}

func (c *sfLocalCache) get(key string) (any, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return nil, false
	}
	item := v.(*sfCacheItem)
	if time.Now().After(item.expireAt) {
		c.m.Delete(key)
		return nil, false
	}
	return item.val, true
}

func (c *sfLocalCache) set(key string, val any, ttl time.Duration) {
	c.m.Store(key, &sfCacheItem{val: val, expireAt: time.Now().Add(ttl)})
}

func (c *sfLocalCache) cleanLoop() {
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
		case <-c.ctx.Done():
			return
		}
	}
}

// ================== SF 函数 ==================

// SF 通用 singleflight 查询封装。
//
// ttl 为可选参数：
//   - 不传：使用默认缓存时长 DefaultSFTTL（5分钟）
//   - 传 0：纯 singleflight，不缓存（等价于 SFNoCache）
//   - 传具体时长：缓存指定时长
//
// 兼容两种写法：
//
//	// 新写法：不传 ttl，使用默认 5 分钟
//	list, err := query.SF(fn, "Order.List", args)
//
//	// 旧写法：显式传 ttl，完全兼容
//	list, err := query.SF(fn, "Order.List", args, 5*time.Second)
//
// 与 NewQuery 配合（原生 gorm）：
//
//	list, err := query.SF(func() ([]*model.Order, error) {
//	    var result []*model.Order
//	    err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("user_id", userId).
//	        EqIfNotZero("status", status).
//	        OrderByDesc("created_at").
//	        Page(pageNum, pageSize).
//	        Find(&result)
//	    return result, err
//	}, "Order.List", map[string]any{
//	    "user_id": userId,
//	    "status":  status,
//	    "page":    pageNum,
//	    "size":    pageSize,
//	})
//
// 与 GenWrap 配合（gorm-gen）：
//
//	list, err := query.SF(func() ([]InterviewVo, error) {
//	    var result []InterviewVo
//	    err := query.GenWrap(dao.InterviewEntity.WithContext(ctx)).
//	        EqIfNotZero(dao.InterviewEntity.Status.Eq(int64(status)), status).
//	        InIfNotEmpty(dao.InterviewEntity.ID.In(ids...), ids).
//	        Apply().Scan(&result)
//	    return result, err
//	}, "Interview.List", map[string]any{
//	    "status": status,
//	    "ids":    ids,
//	}, 5*time.Second)
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	t := DefaultSFTTL
	if len(ttl) > 0 {
		t = ttl[0]
	}
	return SFWithTTL(fn, fnName, args, t)
}

// SFNoCache 纯 singleflight，只合并同一瞬间的并发请求，完成后 key 立即释放，不缓存结果。
// 适合详情接口、用户敏感数据等实时性要求高的场景。
//
// 示例：
//
//	order, err := query.SFNoCache(func() (*model.Order, error) {
//	    var o model.Order
//	    err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
//	        EqIfNotZero("id", orderId).
//	        First(&o)
//	    return &o, err
//	}, "Order.Detail", map[string]any{"id": orderId})
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return SFWithTTL(fn, fnName, args, 0)
}

// SFWithTTL 通用 singleflight 查询封装，手动指定缓存时长。
//
// 行为说明：
//   - TTL=0：纯 singleflight，只合并同一瞬间的并发请求，完成后 key 立即释放
//   - TTL>0：singleflight + 内存缓存，TTL 内相同请求直接返回缓存，不查库
//
// TTL 选择建议：
//   - 列表/统计（实时性要求不高）：3s ~ 30s
//   - 配置/字典（几乎不变）：      1min ~ 5min（即 DefaultSFTTL）
//   - 详情/用户敏感数据：          0 或直接用 SFNoCache
//
// 参数说明：
//   - fn:     实际查询函数，原封不动放入闭包即可
//   - fnName: 查询唯一标识，建议用 "表名.方法名"，如 "Order.List"
//   - args:   影响查询结果的所有参数，map key 自动排序，{"a":1,"b":2} 与 {"b":2,"a":1} 视为同一查询
//   - ttl:    缓存时长，0 表示不缓存只合并并发
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	key := buildSFKey(fnName, args)

	// 先查缓存（Do 外快速返回，避免进入锁）
	if ttl > 0 {
		if cached, ok := sfCache.get(key); ok {
			if result, ok := cached.(T); ok {
				return result, nil
			}
		}
	}

	// singleflight：同一 key 并发时只有一个真正执行，其余等待
	// 缓存写入放在 Do 内部，确保只写一次，避免竞态窗口
	raw, err := sfg.Do(key, func() (any, error) {
		// Do 内部串行，再查一次缓存（防止等待期间已由其他协程写入）
		if ttl > 0 {
			if cached, ok := sfCache.get(key); ok {
				return cached, nil
			}
		}
		result, err := fn()
		if err == nil && ttl > 0 {
			sfCache.set(key, result, ttl)
		}
		return result, err
	})

	if err != nil {
		var zero T
		return zero, err
	}

	result, ok := raw.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("SF: type assertion failed for key=%s, want %s got %s",
			key, reflect.TypeOf(zero), reflect.TypeOf(raw))
	}
	return result, nil
}

// ================== key 构建 ==================

func buildSFKey(fnName string, args map[string]any) string {
	if len(args) == 0 {
		return fmt.Sprintf("sf:%s:noargs", fnName)
	}
	b, err := marshalSorted(args)
	if err != nil {
		// 序列化失败时 fallback 到 fmt.Sprintf，保证相同内容有相同 key
		// 注意：fmt.Sprintf 对 map 顺序不保证，此路径为兜底，正常不会触发
		fallback := fmt.Sprintf("%v", args)
		hash := md5.Sum([]byte(fallback))
		return fmt.Sprintf("sf:%s:%x", fnName, hash)
	}
	hash := md5.Sum(b)
	return fmt.Sprintf("sf:%s:%x", fnName, hash)
}

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
