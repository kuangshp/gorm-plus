package sf

import (
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkBuildSFKey_WithArgs(b *testing.B) {
	args := map[string]any{
		"id": int64(1), "status": int64(2), "page": 1, "size": 20, "keyword": "hello",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = buildSFKey("sys_user.FindPage", args)
	}
}

func BenchmarkBuildSFKey_NoArgs(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = buildSFKey("sys_user.FindById", nil)
	}
}

func BenchmarkSFWithTTL_Hit(b *testing.B) {
	resetCacheForBench(b)
	fn := func() (int, error) { return 42, nil }
	_, _ = SFWithTTL(fn, "bench.Hit", map[string]any{"id": int64(1)}, time.Minute)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SFWithTTL(fn, "bench.Hit", map[string]any{"id": int64(1)}, time.Minute)
	}
}

func BenchmarkSFWithTTL_Miss(b *testing.B) {
	resetCacheForBench(b)
	var n atomic.Int64
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		id := n.Add(1)
		_, _ = SFWithTTL(func() (int, error) { return int(id), nil }, "bench.Miss", map[string]any{"id": id}, time.Minute)
	}
}

func BenchmarkMemoryCache_DelByPrefixes(b *testing.B) {
	c := NewMemoryCache()
	defer c.Stop()
	for i := 0; i < 1000; i++ {
		c.Set(buildSFKey("sys_user.FindList", map[string]any{"i": i}), i, time.Minute)
		c.Set(buildSFKey("sys_user.FindPage", map[string]any{"i": i}), i, time.Minute)
	}
	prefixes := []string{
		keyPrefix + "sys_user.FindList:",
		keyPrefix + "sys_user.FindPage:",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 重新填充少量 key 以免第二次 Del 无事可做导致失真
		c.Set(buildSFKey("sys_user.FindList", map[string]any{"i": i % 100}), i, time.Minute)
		c.DelByPrefixes(prefixes)
	}
}

func resetCacheForBench(b *testing.B) {
	b.Helper()
	ForceReplaceCache(NewMemoryCache())
	b.Cleanup(func() { _ = StopSFCache() })
}
