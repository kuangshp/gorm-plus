# Redis SFCache 参考实现

独立 example 模块，演示如何把 Redis 接到 `gorm-plus` 的 SingleFlight 缓存：

- `Get` **必须**返回 `gormplus.RawValue(bytes)`，禁止直接返回裸 `[]byte` / `string`
- 实现 `DelByPrefix` / `DelByPrefixes`（SCAN，不用 KEYS）
- 可选实现 `Close`，配合 `gormplus.StopSFCache()`

## 使用

```go
import (
    gormplus "github.com/kuangshp/gorm-plus"
    redissfcache "github.com/kuangshp/gorm-plus/examples/redis-sfcache"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
gormplus.RegisterCache(redissfcache.New(rdb, "myapp:sf:"))
defer gormplus.StopSFCache()
```

本目录有独立 `go.mod`（`replace` 指向仓库根），**不会**把 `go-redis` 强绑进根模块。

```bash
cd examples/redis-sfcache
go test ./...
```
