package sf

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// 测试辅助 ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// resetCache 重置全局缓存，每个测试用例开始时调用以隔离状态。
// 用 ForceReplaceCache + NewMemoryCache 是为了避开 RegisterCache 的幂等保护。
func resetCache(t *testing.T) {
	t.Helper()
	ForceReplaceCache(NewMemoryCache())
	OnUnwrapError = nil
}

// fakeRedisCache 模拟 Redis 缓存的字节序列化行为：
//   - Set: 把 val json.Marshal 成 []byte 存进 map
//   - Get: 取出 []byte 包装成 RawValue 返回（关键：模拟 Redis 真实行为）
//
// 这是测试 RawValue 路径的核心 mock。
type fakeRedisCache struct {
	mu   sync.Mutex
	data map[string][]byte

	closed     atomic.Bool
	prefixDels atomic.Int32 // 统计 DelByPrefix 调用次数
	batchDels  atomic.Int32 // 统计 DelByPrefixes 调用次数
}

func newFakeRedisCache() *fakeRedisCache {
	return &fakeRedisCache{data: make(map[string][]byte)}
}

func (c *fakeRedisCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.data[key]
	if !ok {
		return nil, false
	}
	return RawValue(b), true
}

func (c *fakeRedisCache) Set(key string, val any, _ time.Duration) {
	b, err := json.Marshal(val)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = b
}

func (c *fakeRedisCache) Del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

func (c *fakeRedisCache) DelByPrefix(prefix string) {
	c.prefixDels.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.data {
		if strings.HasPrefix(k, prefix) {
			delete(c.data, k)
		}
	}
}

func (c *fakeRedisCache) Close() error {
	c.closed.Store(true)
	return nil
}

// fakeRedisBatchCache 在 fakeRedisCache 基础上额外实现 SFCachePrefixBatchDeleter。
type fakeRedisBatchCache struct {
	*fakeRedisCache
}

func newFakeRedisBatchCache() *fakeRedisBatchCache {
	return &fakeRedisBatchCache{fakeRedisCache: newFakeRedisCache()}
}

func (c *fakeRedisBatchCache) DelByPrefixes(prefixes []string) {
	c.batchDels.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.data {
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				delete(c.data, k)
				break
			}
		}
	}
}

// minimalCache 只实现 SFCache 三个必选方法，不实现 prefix / batch / close 任何可选接口。
// 用于验证未实现可选接口时的静默降级行为。
type minimalCache struct {
	mu   sync.Mutex
	data map[string]any
}

func newMinimalCache() *minimalCache { return &minimalCache{data: make(map[string]any)} }

func (c *minimalCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

func (c *minimalCache) Set(key string, val any, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = val
}

func (c *minimalCache) Del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// unwrapCached //////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestUnwrapCached_DirectAssertSuccess(t *testing.T) {
	type User struct{ Name string }
	u := &User{Name: "alice"}

	got, err := unwrapCached[*User](u)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("want alice, got %v", got)
	}
}

func TestUnwrapCached_RawValueSuccess(t *testing.T) {
	type User struct {
		Name string `json:"name"`
	}
	raw := RawValue(`{"name":"bob"}`)

	got, err := unwrapCached[*User](raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "bob" {
		t.Fatalf("want bob, got %v", got)
	}
}

func TestUnwrapCached_RawValueBadJSON(t *testing.T) {
	type User struct {
		Name string `json:"name"`
	}
	raw := RawValue(`{not valid json}`)

	_, err := unwrapCached[*User](raw)
	if err == nil {
		t.Fatal("expect error on bad json")
	}
	if !strings.Contains(err.Error(), "Unmarshal") {
		t.Errorf("error should mention Unmarshal, got: %v", err)
	}
}

func TestUnwrapCached_TypeMismatch(t *testing.T) {
	// raw 是 int，但期望 *string
	_, err := unwrapCached[*string](42)
	if err == nil {
		t.Fatal("expect type assertion failure")
	}
	if !strings.Contains(err.Error(), "类型断言失败") {
		t.Errorf("error should mention 类型断言失败, got: %v", err)
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////// SFWithTTL (内存缓存路径) ///////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestSFWithTTL_MemoryCacheHit(t *testing.T) {
	resetCache(t)

	type User struct{ Name string }
	calls := atomic.Int32{}
	fn := func() (*User, error) {
		calls.Add(1)
		return &User{Name: "alice"}, nil
	}

	// 第一次 miss → 打 DB
	u1, err := SFWithTTL(fn, "test.UserFind", map[string]any{"id": 1}, time.Minute)
	if err != nil || u1.Name != "alice" {
		t.Fatalf("first call failed: u=%+v err=%v", u1, err)
	}

	// 第二次 hit → 不打 DB
	u2, err := SFWithTTL(fn, "test.UserFind", map[string]any{"id": 1}, time.Minute)
	if err != nil || u2.Name != "alice" {
		t.Fatalf("second call failed: u=%+v err=%v", u2, err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("expected fn called 1 time (cache hit on 2nd), got %d", got)
	}
}

func TestSFWithTTL_ZeroTTLBypassesCache(t *testing.T) {
	resetCache(t)

	calls := atomic.Int32{}
	fn := func() (int, error) {
		calls.Add(1)
		return int(calls.Load()), nil
	}

	// TTL=0 等价于 SFNoCache：每次都打 DB
	_, _ = SFWithTTL(fn, "test.Zero", nil, 0)
	_, _ = SFWithTTL(fn, "test.Zero", nil, 0)

	if got := calls.Load(); got != 2 {
		t.Errorf("TTL=0 should not cache, expected 2 calls, got %d", got)
	}
}

func TestSFWithTTL_PropagatesError(t *testing.T) {
	resetCache(t)

	wantErr := errors.New("db down")
	fn := func() (int, error) { return 0, wantErr }

	_, err := SFWithTTL(fn, "test.Err", nil, time.Minute)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped err, got %v", err)
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////// SFWithTTL (Redis 路径) ////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// 这是 P0-1 修复的核心测试：Redis 缓存返回 RawValue 后，sf 包能正确反序列化
// 到业务期望的类型 T，且 raw.(T) 不再失败。
func TestSFWithTTL_RedisRawValuePath(t *testing.T) {
	type User struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}

	fake := newFakeRedisCache()
	ForceReplaceCache(fake)
	defer ForceReplaceCache(NewMemoryCache())
	OnUnwrapError = nil

	calls := atomic.Int32{}
	fn := func() (*User, error) {
		calls.Add(1)
		return &User{ID: 1, Name: "alice"}, nil
	}

	// 第一次：miss → fn 执行 → Set RawValue 进 fakeRedis
	u1, err := SFWithTTL(fn, "users.FindById", map[string]any{"id": int64(1)}, time.Minute)
	if err != nil {
		t.Fatalf("first call err: %v", err)
	}
	if u1 == nil || u1.Name != "alice" {
		t.Fatalf("first call got %+v", u1)
	}

	// 第二次：hit → 从 RawValue 反序列化回 *User，业务期望的类型
	u2, err := SFWithTTL(fn, "users.FindById", map[string]any{"id": int64(1)}, time.Minute)
	if err != nil {
		t.Fatalf("second call should hit cache, got err: %v", err)
	}
	if u2 == nil || u2.Name != "alice" {
		t.Fatalf("second call got %+v, expected alice from cache", u2)
	}

	// 关键断言：fn 只该被调一次（第二次命中了 Redis 的 RawValue）
	if got := calls.Load(); got != 1 {
		t.Errorf("Redis cache miss bug! fn called %d times, expected 1 (cache hit on 2nd)", got)
	}
}

func TestSFWithTTL_OnUnwrapErrorTriggered(t *testing.T) {
	type User struct {
		Name string `json:"name"`
	}

	// 1) 先用 fakeRedis 写入一个 *User，然后偷偷把数据替换成坏的 json
	fake := newFakeRedisCache()
	ForceReplaceCache(fake)
	defer ForceReplaceCache(NewMemoryCache())

	// 注入坏数据：直接往 fakeRedis 里塞一段无效 json
	key := buildSFKey("users.FindById", map[string]any{"id": int64(1)})
	fake.mu.Lock()
	fake.data[key] = []byte(`{this is not json`)
	fake.mu.Unlock()

	// 2) 注入 unwrap 错误钩子
	var (
		hookCalls atomic.Int32
		gotKey    string
		gotErr    error
		mu        sync.Mutex
	)
	OnUnwrapError = func(k string, e error) {
		hookCalls.Add(1)
		mu.Lock()
		gotKey = k
		gotErr = e
		mu.Unlock()
	}
	defer func() { OnUnwrapError = nil }()

	// 3) fn 返回 fallback 数据
	fn := func() (*User, error) { return &User{Name: "fallback"}, nil }

	u, err := SFWithTTL(fn, "users.FindById", map[string]any{"id": int64(1)}, time.Minute)
	if err != nil {
		t.Fatalf("should fallback to fn on unwrap error, got: %v", err)
	}
	if u == nil || u.Name != "fallback" {
		t.Errorf("should fall back to fn() result, got: %+v", u)
	}

	if got := hookCalls.Load(); got == 0 {
		t.Error("OnUnwrapError should be called when cached data is bad json")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotKey != key {
		t.Errorf("hook key mismatch: want %q got %q", key, gotKey)
	}
	if gotErr == nil {
		t.Error("hook err should not be nil")
	}
}

func TestSFWithTTL_OnUnwrapErrorPanicIsRecovered(t *testing.T) {
	type User struct {
		Name string `json:"name"`
	}

	fake := newFakeRedisCache()
	ForceReplaceCache(fake)
	defer ForceReplaceCache(NewMemoryCache())

	key := buildSFKey("users.FindById", nil)
	fake.mu.Lock()
	fake.data[key] = []byte(`{bad`)
	fake.mu.Unlock()

	// 注入会 panic 的钩子，业务不应因此挂掉
	OnUnwrapError = func(string, error) { panic("hook bug") }
	defer func() { OnUnwrapError = nil }()

	fn := func() (*User, error) { return &User{Name: "ok"}, nil }

	u, err := SFWithTTL(fn, "users.FindById", nil, time.Minute)
	if err != nil {
		t.Fatalf("panic in hook should be recovered, got: %v", err)
	}
	if u == nil || u.Name != "ok" {
		t.Errorf("business should still see fallback, got: %+v", u)
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////// SFInvalidate / Prefix /////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestSFInvalidate_Precise(t *testing.T) {
	resetCache(t)

	calls := atomic.Int32{}
	fn := func() (int, error) {
		calls.Add(1)
		return 42, nil
	}

	_, _ = SFWithTTL(fn, "test.X", map[string]any{"id": 1}, time.Minute)
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}

	// 精确失效
	SFInvalidate("test.X", map[string]any{"id": 1})

	// 再次调用应该 miss
	_, _ = SFWithTTL(fn, "test.X", map[string]any{"id": 1}, time.Minute)
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 calls after invalidate, got %d", got)
	}
}

func TestSFInvalidatePrefix_ClearsMethodGroup(t *testing.T) {
	resetCache(t)

	fn := func() (int, error) { return 42, nil }
	// 先写 3 个不同 args 的 FindList 缓存
	_, _ = SFWithTTL(fn, "users.FindList", map[string]any{"status": 1}, time.Minute)
	_, _ = SFWithTTL(fn, "users.FindList", map[string]any{"status": 2}, time.Minute)
	_, _ = SFWithTTL(fn, "users.FindList", map[string]any{"status": 3}, time.Minute)
	// 写一个不同 fnName 的，确认不会被误删
	_, _ = SFWithTTL(fn, "users.FindById", map[string]any{"id": 1}, time.Minute)

	mc := getCache().(*MemoryCache)
	cntBefore := 0
	mc.m.Range(func(k, _ any) bool { cntBefore++; return true })
	if cntBefore != 4 {
		t.Fatalf("expected 4 cache entries, got %d", cntBefore)
	}

	SFInvalidatePrefix("users.FindList")

	cntAfter := 0
	hasFindById := false
	mc.m.Range(func(k, _ any) bool {
		cntAfter++
		if ks, _ := k.(string); strings.Contains(ks, "FindById") {
			hasFindById = true
		}
		return true
	})
	if cntAfter != 1 {
		t.Errorf("expected 1 entry left after prefix invalidate, got %d", cntAfter)
	}
	if !hasFindById {
		t.Error("FindById cache was wrongly removed")
	}
}

func TestSFInvalidatePrefix_RejectsSuspiciousInput(t *testing.T) {
	resetCache(t)

	fn := func() (int, error) { return 1, nil }
	_, _ = SFWithTTL(fn, "users.FindList", map[string]any{"x": 1}, time.Minute)

	cntBefore := func() int {
		c := 0
		getCache().(*MemoryCache).m.Range(func(_, _ any) bool { c++; return true })
		return c
	}

	if cntBefore() != 1 {
		t.Fatal("setup failed")
	}

	// 空串 / 过短无点 → 应该不动缓存
	SFInvalidatePrefix("")
	SFInvalidatePrefix("a")
	SFInvalidatePrefix("ab")

	if got := cntBefore(); got != 1 {
		t.Errorf("suspicious prefix should not delete anything, but got %d entries (was 1)", got)
	}
}

func TestSFInvalidatePrefix_OnCacheWithoutPrefixDeleter(t *testing.T) {
	// 用 minimalCache（只有 Get/Set/Del），SFInvalidatePrefix 应该静默忽略而不 panic
	ForceReplaceCache(newMinimalCache())
	defer ForceReplaceCache(NewMemoryCache())

	// 不应该 panic
	SFInvalidatePrefix("users.FindList")
}

////////////////////////////////////////////////////////////////////////////////
///////////////////////////// SFInvalidatePrefixes /////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestSFInvalidatePrefixes_BatchInterfacePreferred(t *testing.T) {
	batch := newFakeRedisBatchCache()
	ForceReplaceCache(batch)
	defer ForceReplaceCache(NewMemoryCache())

	SFInvalidatePrefixes([]string{
		"users.FindList",
		"users.FindPage",
		"users.Count",
	})

	if got := batch.batchDels.Load(); got != 1 {
		t.Errorf("expected 1 batch call, got %d", got)
	}
	if got := batch.prefixDels.Load(); got != 0 {
		t.Errorf("should not fall back to single DelByPrefix, but got %d calls", got)
	}
}

func TestSFInvalidatePrefixes_FallbackToSinglePrefix(t *testing.T) {
	// fakeRedisCache 只实现了 DelByPrefix，没实现 DelByPrefixes
	fake := newFakeRedisCache()
	ForceReplaceCache(fake)
	defer ForceReplaceCache(NewMemoryCache())

	SFInvalidatePrefixes([]string{
		"users.FindList",
		"users.FindPage",
		"users.Count",
	})

	if got := fake.prefixDels.Load(); got != 3 {
		t.Errorf("should fall back to 3 DelByPrefix calls, got %d", got)
	}
}

func TestSFInvalidatePrefixes_FiltersSuspiciousNames(t *testing.T) {
	batch := newFakeRedisBatchCache()
	ForceReplaceCache(batch)
	defer ForceReplaceCache(NewMemoryCache())

	// 全部都是可疑前缀
	SFInvalidatePrefixes([]string{"", "a", "ab"})

	if got := batch.batchDels.Load(); got != 0 {
		t.Errorf("all-filtered batch should not call DelByPrefixes, got %d calls", got)
	}
}

func TestSFInvalidatePrefixes_MemoryCacheBatch(t *testing.T) {
	resetCache(t)

	fn := func() (int, error) { return 1, nil }
	// 写 3 个方法各 2 个 args
	_, _ = SFWithTTL(fn, "users.FindList", map[string]any{"s": 1}, time.Minute)
	_, _ = SFWithTTL(fn, "users.FindList", map[string]any{"s": 2}, time.Minute)
	_, _ = SFWithTTL(fn, "users.FindPage", map[string]any{"p": 1}, time.Minute)
	_, _ = SFWithTTL(fn, "users.FindPage", map[string]any{"p": 2}, time.Minute)
	_, _ = SFWithTTL(fn, "users.Count", map[string]any{"x": 1}, time.Minute)
	_, _ = SFWithTTL(fn, "users.Count", map[string]any{"x": 2}, time.Minute)
	// 一个不该被删的
	_, _ = SFWithTTL(fn, "users.FindById", map[string]any{"id": 99}, time.Minute)

	mc := getCache().(*MemoryCache)
	cnt := func() int {
		c := 0
		mc.m.Range(func(_, _ any) bool { c++; return true })
		return c
	}
	if cnt() != 7 {
		t.Fatalf("expected 7 entries, got %d", cnt())
	}

	SFInvalidatePrefixes([]string{
		"users.FindList",
		"users.FindPage",
		"users.Count",
	})

	if got := cnt(); got != 1 {
		t.Errorf("expected 1 entry left (FindById), got %d", got)
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////// RegisterCache 幂等与替换语义 ///////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestRegisterCache_AllowsReplacingMemoryCache(t *testing.T) {
	resetCache(t)
	// 触发懒加载内存缓存
	_ = getCache()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("replacing memory cache should not panic, got: %v", r)
		}
	}()

	RegisterCache(newMinimalCache())
}

func TestRegisterCache_RejectsDuplicateCustomRegistration(t *testing.T) {
	ForceReplaceCache(NewMemoryCache()) // 先清掉
	// 第一次注册自定义：OK
	RegisterCache(newMinimalCache())
	defer ForceReplaceCache(NewMemoryCache()) // 测试结束恢复

	// 第二次注册自定义：应该 panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("second RegisterCache should panic")
		}
	}()
	RegisterCache(newMinimalCache())
}

func TestRegisterCache_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("RegisterCache(nil) should panic")
		}
	}()
	RegisterCache(nil)
}

func TestForceReplaceCache_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("ForceReplaceCache(nil) should panic")
		}
	}()
	ForceReplaceCache(nil)
}

func TestForceReplaceCache_StopsOldMemoryCache(t *testing.T) {
	ForceReplaceCache(NewMemoryCache())
	old := getCache().(*MemoryCache)

	ForceReplaceCache(NewMemoryCache())

	// old 的 cancel 应该已被调用，cleanLoop 应该退出
	// 用一个间接方法验证：cancel 后再 cancel 是无副作用的
	// （这里不强制验证 goroutine 退出，只确保 Stop 被调用不报错）
	old.Stop() // 二次调用应该无副作用
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////// MemoryCache 基本能力 ///////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestMemoryCache_GetSetDel(t *testing.T) {
	mc := NewMemoryCache()
	defer mc.Stop()

	mc.Set("k1", "v1", time.Minute)
	if v, ok := mc.Get("k1"); !ok || v != "v1" {
		t.Errorf("expected v1, got %v ok=%v", v, ok)
	}

	mc.Del("k1")
	if _, ok := mc.Get("k1"); ok {
		t.Error("k1 should be deleted")
	}
}

func TestMemoryCache_Expiration(t *testing.T) {
	mc := NewMemoryCache()
	defer mc.Stop()

	mc.Set("k1", "v1", 10*time.Millisecond)
	if _, ok := mc.Get("k1"); !ok {
		t.Fatal("k1 should exist immediately")
	}

	time.Sleep(30 * time.Millisecond)

	if _, ok := mc.Get("k1"); ok {
		t.Error("k1 should be expired")
	}
}

func TestMemoryCache_DelByPrefix(t *testing.T) {
	mc := NewMemoryCache()
	defer mc.Stop()

	mc.Set("sf:users.FindList:abc", "v1", time.Minute)
	mc.Set("sf:users.FindList:def", "v2", time.Minute)
	mc.Set("sf:users.FindById:xyz", "v3", time.Minute)

	mc.DelByPrefix("sf:users.FindList:")

	if _, ok := mc.Get("sf:users.FindList:abc"); ok {
		t.Error("abc should be deleted")
	}
	if _, ok := mc.Get("sf:users.FindList:def"); ok {
		t.Error("def should be deleted")
	}
	if _, ok := mc.Get("sf:users.FindById:xyz"); !ok {
		t.Error("xyz should remain")
	}
}

func TestMemoryCache_DelByPrefixes(t *testing.T) {
	mc := NewMemoryCache()
	defer mc.Stop()

	mc.Set("sf:a.FindList:1", "v", time.Minute)
	mc.Set("sf:a.FindPage:1", "v", time.Minute)
	mc.Set("sf:a.Count:1", "v", time.Minute)
	mc.Set("sf:a.FindById:1", "v", time.Minute) // 不该删

	mc.DelByPrefixes([]string{
		"sf:a.FindList:",
		"sf:a.FindPage:",
		"sf:a.Count:",
	})

	got := 0
	mc.m.Range(func(_, _ any) bool { got++; return true })
	if got != 1 {
		t.Errorf("expected 1 left (FindById), got %d", got)
	}
	if _, ok := mc.Get("sf:a.FindById:1"); !ok {
		t.Error("FindById was wrongly removed")
	}
}

func TestMemoryCache_DelByPrefixesEmpty(t *testing.T) {
	mc := NewMemoryCache()
	defer mc.Stop()

	mc.Set("sf:a:1", "v", time.Minute)
	mc.DelByPrefixes(nil) // 不应该清空所有
	mc.DelByPrefixes([]string{})

	if _, ok := mc.Get("sf:a:1"); !ok {
		t.Error("empty prefixes should be no-op")
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////// buildSFKey //////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestBuildSFKey_ArgsOrderIrrelevant(t *testing.T) {
	a := buildSFKey("X", map[string]any{"a": 1, "b": 2, "c": 3})
	b := buildSFKey("X", map[string]any{"c": 3, "b": 2, "a": 1})
	if a != b {
		t.Errorf("same args in different order produced different keys: %q vs %q", a, b)
	}
}

func TestBuildSFKey_DifferentArgsDifferentKeys(t *testing.T) {
	a := buildSFKey("X", map[string]any{"id": 1})
	b := buildSFKey("X", map[string]any{"id": 2})
	if a == b {
		t.Errorf("different args produced same key: %q", a)
	}
}

func TestBuildSFKey_NoArgs(t *testing.T) {
	k := buildSFKey("X", nil)
	if !strings.HasSuffix(k, ":noargs") {
		t.Errorf("nil args should produce :noargs suffix, got %q", k)
	}
	k2 := buildSFKey("X", map[string]any{})
	if k != k2 {
		t.Errorf("nil and empty map should yield same key: %q vs %q", k, k2)
	}
}

////////////////////////////////////////////////////////////////////////////////
///////////////////////////////// singleflight /////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestSFWithTTL_ConcurrentCallsMergedByFlight(t *testing.T) {
	resetCache(t)

	calls := atomic.Int32{}
	fn := func() (int, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // 模拟慢查询
		return 42, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := SFWithTTL(fn, "test.Slow", map[string]any{"id": 1}, time.Minute)
			if err != nil || v != 42 {
				t.Errorf("concurrent call got v=%v err=%v", v, err)
			}
		}()
	}
	wg.Wait()

	// 20 个 goroutine 同时打过来，singleflight 应该合并成 1 次（最坏 2 次）
	if got := calls.Load(); got > 2 {
		t.Errorf("singleflight should merge 20 concurrent calls into 1-2, got %d", got)
	}
}

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// StopSFCache ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestStopSFCache_CallsCloserOnCustomCache(t *testing.T) {
	fake := newFakeRedisCache()
	ForceReplaceCache(fake)

	err := StopSFCache()
	if err != nil {
		t.Errorf("StopSFCache err: %v", err)
	}
	if !fake.closed.Load() {
		t.Error("Close() should have been called on custom cache implementing SFCacheCloser")
	}

	// 恢复，避免污染后续测试
	ForceReplaceCache(NewMemoryCache())
}

func TestStopSFCache_OnNoCache(t *testing.T) {
	// 即使从未注册过任何缓存（globalCache=nil），StopSFCache 也不该 panic
	// 注意：本测试无法真正让 globalCache=nil，因为前面测试已经懒加载过了。
	// 这里至少验证不 panic 即可。
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("StopSFCache should not panic, got: %v", r)
		}
	}()
	_ = StopSFCache()
}
