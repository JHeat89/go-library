package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"time"
)

// ErrCacheMiss is returned by TokenCache.Get when no entry exists for the
// given key. Implementations must translate their store's "not found" signal
// into this error so Client can distinguish a miss from a real failure.
var ErrCacheMiss = errors.New("oauth: token cache miss")

// TokenCache stores serialized tokens, keyed by string, with a TTL. The
// oauth/redis subpackage adapts go-redis v9; any store (Redis, Memcached,
// DynamoDB, ...) can implement this interface directly.
//
// There is no in-memory implementation in this package by design — see the
// package doc for why.
type TokenCache interface {
	// Get returns the raw bytes stored at key, or ErrCacheMiss if absent.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores value at key with the given TTL.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes any entry at key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
}

// defaultCacheKey derives a stable cache key that distinguishes tenant,
// client, and endpoint without putting secrets in the key itself. The host
// stays readable in key listings for ops visibility; the hash covers the
// parts (token URL, grant type, client ID, username) that make a token
// unique.
func defaultCacheKey(cfg Config) string {
	host := "unknown"
	if u, err := url.Parse(cfg.TokenURL); err == nil && u.Host != "" {
		host = u.Host
	}
	sum := sha256.Sum256([]byte(cfg.TokenURL + "|" + string(cfg.GrantType) + "|" + cfg.ClientID + "|" + cfg.Username))
	return "oauth:token:" + host + ":" + hex.EncodeToString(sum[:])[:16]
}
