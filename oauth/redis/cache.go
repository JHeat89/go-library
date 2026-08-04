// Package redis adapts go-redis v9 to oauth.TokenCache. This is the only
// file in the module that imports go-redis — services that don't cache
// OAuth tokens in Redis never pull in the dependency; import this
// subpackage only when you do.
//
//	rdb := goredis.NewClient(&goredis.Options{Addr: "elasticache.example:6379"})
//	client, err := oauth.New(log, oauth.Config{
//		...
//		Cache:      redis.New(rdb),
//		CacheRead:  true,
//		CacheWrite: true,
//	})
package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/JHeat89/go-library/oauth"
)

// Cache adapts a go-redis UniversalClient (single-node, cluster, or
// sentinel — the consumer constructs and owns it) to oauth.TokenCache.
type Cache struct {
	client goredis.UniversalClient
}

var _ oauth.TokenCache = (*Cache)(nil)

// New builds a Cache backed by client.
func New(client goredis.UniversalClient) *Cache {
	return &Cache{client: client}
}

// Get returns the bytes stored at key, translating a Redis miss into
// oauth.ErrCacheMiss.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, oauth.ErrCacheMiss
	}
	return data, err
}

// Set stores value at key with the given TTL.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Delete removes any entry at key.
func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
