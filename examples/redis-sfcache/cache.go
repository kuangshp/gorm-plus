// Package redissfcache 提供基于 go-redis 的 gorm-plus SFCache 参考实现。
//
// 本模块是独立 example（optional），不会进入根 go.mod 依赖。
// 关键约定：Get 必须返回 gormplus.RawValue(bytes)，否则缓存命中后类型还原失败、命中率静默为 0。
package redissfcache

import (
	"context"
	"encoding/json"
	"time"

	gormplus "github.com/kuangshp/gorm-plus"
	"github.com/redis/go-redis/v9"
)

// Cache 实现 gormplus.SFCache / PrefixDeleter / PrefixBatchDeleter / Closer。
type Cache struct {
	rdb    redis.Cmdable
	prefix string
}

// New 创建 Redis SF 缓存。prefix 建议带业务命名空间，如 "myapp:sf:"。
func New(rdb redis.Cmdable, prefix string) *Cache {
	if prefix == "" {
		prefix = "sf:"
	}
	return &Cache{rdb: rdb, prefix: prefix}
}

func (c *Cache) fullKey(key string) string { return c.prefix + key }

// Get 返回 RawValue 包装的字节流，供 sf 包 json.Unmarshal 到目标类型。
func (c *Cache) Get(key string) (any, bool) {
	b, err := c.rdb.Get(context.Background(), c.fullKey(key)).Bytes()
	if err != nil {
		return nil, false
	}
	return gormplus.RawValue(b), true
}

func (c *Cache) Set(key string, val any, ttl time.Duration) {
	b, err := json.Marshal(val)
	if err != nil {
		return
	}
	_ = c.rdb.Set(context.Background(), c.fullKey(key), b, ttl).Err()
}

func (c *Cache) Del(key string) {
	_ = c.rdb.Del(context.Background(), c.fullKey(key)).Err()
}

// DelByPrefix 使用 SCAN，避免 KEYS 阻塞。
func (c *Cache) DelByPrefix(prefix string) {
	c.DelByPrefixes([]string{prefix})
}

// DelByPrefixes 批量 SCAN + DEL，减少 RTT。
func (c *Cache) DelByPrefixes(prefixes []string) {
	ctx := context.Background()
	for _, p := range prefixes {
		pattern := c.prefix + p + "*"
		var cursor uint64
		for {
			keys, next, err := c.rdb.Scan(ctx, cursor, pattern, 500).Result()
			if err != nil {
				return
			}
			if len(keys) > 0 {
				_ = c.rdb.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
}

// Close 在 rdb 为 *redis.Client 时关闭连接；Cmdable 其它实现则 no-op。
func (c *Cache) Close() error {
	if client, ok := c.rdb.(*redis.Client); ok {
		return client.Close()
	}
	return nil
}
