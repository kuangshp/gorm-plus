package redissfcache_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	gormplus "github.com/kuangshp/gorm-plus"
	redissfcache "github.com/kuangshp/gorm-plus/examples/redis-sfcache"
	"github.com/redis/go-redis/v9"
)

func TestRedisSFCache_HitAndBadJSONFallback(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := redissfcache.New(rdb, "demo:sf:")
	gormplus.ForceReplaceCache(cache)
	t.Cleanup(func() { _ = gormplus.StopSFCache() })

	type User struct {
		Name string `json:"name"`
	}

	calls := 0
	fn := func() (*User, error) {
		calls++
		return &User{Name: "alice"}, nil
	}

	u1, err := gormplus.SFWithTTL(fn, "users.FindById", map[string]any{"id": int64(1)}, time.Minute)
	if err != nil || u1 == nil || u1.Name != "alice" || calls != 1 {
		t.Fatalf("first call: u=%+v err=%v calls=%d", u1, err, calls)
	}

	u2, err := gormplus.SFWithTTL(fn, "users.FindById", map[string]any{"id": int64(1)}, time.Minute)
	if err != nil || u2 == nil || u2.Name != "alice" || calls != 1 {
		t.Fatalf("cache hit: u=%+v err=%v calls=%d", u2, err, calls)
	}

	// 注入坏 JSON，应降级到 fn()
	keys := mr.Keys()
	if len(keys) == 0 {
		t.Fatal("expected redis keys after set")
	}
	if err := mr.Set(keys[0], `{bad`); err != nil {
		t.Fatal(err)
	}

	u3, err := gormplus.SFWithTTL(fn, "users.FindById", map[string]any{"id": int64(1)}, time.Minute)
	if err != nil || u3 == nil || u3.Name != "alice" {
		t.Fatalf("bad json fallback: u=%+v err=%v", u3, err)
	}
	if calls != 2 {
		t.Fatalf("expected fn called again after bad cache, calls=%d", calls)
	}
}

func TestRedisSFCache_DelByPrefix(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := redissfcache.New(rdb, "demo:sf:")
	gormplus.ForceReplaceCache(cache)
	t.Cleanup(func() { _ = gormplus.StopSFCache() })

	_, _ = gormplus.SFWithTTL(func() (int, error) { return 1, nil }, "users.FindList", map[string]any{"p": 1}, time.Minute)
	_, _ = gormplus.SFWithTTL(func() (int, error) { return 2, nil }, "users.FindById", map[string]any{"id": 1}, time.Minute)

	gormplus.SFInvalidatePrefix("users.FindList")
	if len(mr.Keys()) != 1 {
		t.Fatalf("after prefix invalidate keys=%v", mr.Keys())
	}
}
