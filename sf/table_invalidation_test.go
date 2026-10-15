package sf

import (
	"testing"
	"time"
)

func TestTableAndMethodInvalidation(t *testing.T) {
	for _, backend := range []string{"memory", "redis-single", "redis-batch"} {
		for _, batch := range []bool{false, true} {
			t.Run(backend+map[bool]string{false: "/single", true: "/batch"}[batch], func(t *testing.T) {
				resetCache(t)
				switch backend {
				case "redis-single":
					ForceReplaceCache(newFakeRedisCache())
				case "redis-batch":
					ForceReplaceCache(newFakeRedisBatchCache())
				}
				invalidate := func(name string) {
					if batch {
						SFInvalidatePrefixes([]string{"", "a", name})
					} else {
						SFInvalidatePrefix(name)
					}
				}
				version := 1
				load := func() (int, error) { return version, nil }
				entries := []struct {
					name string
					args map[string]any
				}{
					{"users.Find", nil},
					{"users.FindList", map[string]any{"status": 1}},
					{"users.FindById", map[string]any{"id": 1}},
					{"users.FindById", map[string]any{"id": 2}},
					{"users.Count", nil},
					{"users_archive.Find", nil},
					{"orders.Find", nil},
				}
				for _, e := range entries {
					if _, err := SFWithTTL(load, e.name, e.args, time.Minute); err != nil {
						t.Fatal(err)
					}
				}
				version = 2
				invalidate("users.Find")
				for i, e := range entries {
					want := 1
					if i == 0 {
						want = 2
					}
					if got, err := SFWithTTL(load, e.name, e.args, time.Minute); err != nil || got != want {
						t.Fatalf("method invalidation %s: got %d, %v; want %d", e.name, got, err, want)
					}
				}
				version = 3
				invalidate("users.")
				for i, e := range entries {
					want := 3
					if i >= 5 {
						want = 1
					}
					if got, err := SFWithTTL(load, e.name, e.args, time.Minute); err != nil || got != want {
						t.Fatalf("table invalidation %s: got %d, %v; want %d", e.name, got, err, want)
					}
				}
			})
		}
	}
}

// 写后失效必须隔离还在运行的旧查询，防止新请求加入旧 singleflight 或旧值回填。
func TestInvalidationFencesInflightQueries(t *testing.T) {
	for _, mode := range []string{"exact", "prefix", "batch"} {
		t.Run(mode, func(t *testing.T) {
			resetCache(t)
			started, release := make(chan struct{}), make(chan struct{})
			oldDone := make(chan int, 1)
			go func() {
				result, _ := SFWithTTL(func() (int, error) { close(started); <-release; return 1, nil }, "users.FindById", nil, time.Minute)
				oldDone <- result
			}()
			<-started
			switch mode {
			case "exact":
				SFInvalidate("users.FindById", nil)
			case "prefix":
				SFInvalidatePrefix("users.")
			case "batch":
				SFInvalidatePrefixes([]string{"users.", "orders."})
			}
			fresh := make(chan int, 1)
			go func() {
				result, _ := SFWithTTL(func() (int, error) { return 2, nil }, "users.FindById", nil, time.Minute)
				fresh <- result
			}()
			select {
			case got := <-fresh:
				if got != 2 {
					t.Errorf("post-invalidation request received stale %d", got)
				}
			case <-time.After(2 * time.Second):
				t.Error("new request joined old singleflight")
			}
			close(release)
			if got := <-oldDone; got != 1 {
				t.Fatalf("old request changed: %d", got)
			}
			got, err := SFWithTTL(func() (int, error) { return 3, nil }, "users.FindById", nil, time.Minute)
			if err != nil || got != 2 {
				t.Fatalf("late old query replaced cache: %d %v", got, err)
			}
		})
	}
}
