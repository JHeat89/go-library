package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JHeat89/go-library/logger"
)

// maxBodyBytes caps how much of a token response is read, defending against
// a misbehaving or malicious endpoint streaming an unbounded body.
const maxBodyBytes = 1 << 20 // 1MB

// TokenSource is satisfied by *Client. Consumers that only need to acquire a
// token — an HTTP client wrapper that attaches Authorization headers, say —
// can depend on this instead of the concrete type, making it easy to supply
// a fake in tests.
type TokenSource interface {
	Token(ctx context.Context) (*Token, error)
}

var _ TokenSource = (*Client)(nil)

// Token returns a valid access token. When CacheRead is set it consults the
// cache first; on a hit with a still-valid entry it returns that token
// without contacting the token endpoint. On a cache miss, a read error, or
// an entry that's expired (or within ExpiryMargin of expiring), it fetches a
// fresh token and, when CacheWrite is set, stores it.
//
// There is no in-memory cache: every call does at least a cache read or an
// HTTP fetch. See the package doc for why.
func (c *Client) Token(ctx context.Context) (*Token, error) {
	ctx, _ = logger.EnsureRequestID(ctx)
	logger.LifecycleFrom(ctx).Stage(ctx, "oauth.token")

	if c.cfg.CacheRead {
		if tok := c.readCache(ctx); tok != nil {
			return tok, nil
		}
	}

	return c.fetchAndCache(ctx)
}

// Refresh always fetches a fresh token, bypassing the cache read — but still
// writes the result to the cache when CacheWrite is set. Use it after a
// downstream 401 to force a new grant, rather than risk re-reading the very
// cached token that just failed.
func (c *Client) Refresh(ctx context.Context) (*Token, error) {
	ctx, _ = logger.EnsureRequestID(ctx)
	logger.LifecycleFrom(ctx).Stage(ctx, "oauth.token")

	return c.fetchAndCache(ctx)
}

// Invalidate deletes any cached token. It is a no-op if no Cache is
// configured.
func (c *Client) Invalidate(ctx context.Context) error {
	if c.cfg.Cache == nil {
		return nil
	}
	return c.cfg.Cache.Delete(ctx, c.cfg.CacheKey)
}

// baseAttrs returns the identifying fields attached to every oauth log line.
// It allocates a fresh slice on each call so callers can safely append
// without aliasing another call's backing array.
func (c *Client) baseAttrs() []any {
	return []any{
		"oauth.grantType", string(c.cfg.GrantType),
		"oauth.tokenUrl", c.cfg.TokenURL,
	}
}

func (c *Client) cacheAttrs() []any {
	return append(c.baseAttrs(), "oauth.cacheKey", c.cfg.CacheKey)
}

// readCache returns a valid cached token, or nil if there is no usable
// entry (miss, expired/within-margin, or a read/decode error — all logged,
// none fatal).
func (c *Client) readCache(ctx context.Context) *Token {
	data, err := c.cfg.Cache.Get(ctx, c.cfg.CacheKey)
	if err != nil {
		if errors.Is(err, ErrCacheMiss) {
			c.base.Debug(ctx, "oauth token cache miss", c.cacheAttrs()...)
		} else {
			c.base.Warn(ctx, "oauth token cache read failed", append(c.cacheAttrs(), logger.Err(err))...)
		}
		return nil
	}

	tok, err := unmarshalCache(data)
	if err != nil {
		c.base.Warn(ctx, "oauth token cache read failed", append(c.cacheAttrs(), logger.Err(err))...)
		return nil
	}

	if !c.tokenValid(tok) {
		c.base.Debug(ctx, "oauth token cache miss", c.cacheAttrs()...)
		return nil
	}

	c.base.Debug(ctx, "oauth token cache hit", append(c.cacheAttrs(), "oauth.expiresAt", tok.ExpiresAt)...)
	return tok
}

// tokenValid applies ExpiryMargin on top of Token.Valid: a token within
// ExpiryMargin of expiring is treated as invalid so callers refresh ahead of
// a downstream 401 rather than racing it. A zero ExpiresAt (cache entries
// trusting the store's TTL) is always valid, matching Token.Valid.
func (c *Client) tokenValid(tok *Token) bool {
	if tok == nil || tok.AccessToken == "" {
		return false
	}
	if tok.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(c.cfg.ExpiryMargin).Before(tok.ExpiresAt)
}

// fetchAndCache fetches a fresh token, logs the outcome, and — on success,
// when CacheWrite is set — stores it.
func (c *Client) fetchAndCache(ctx context.Context) (*Token, error) {
	start := time.Now()
	tok, err := c.fetch(ctx)
	elapsedMs := float64(time.Since(start).Microseconds()) / 1000

	if err != nil {
		attrs := append(c.baseAttrs(), "oauth.durationMs", elapsedMs)
		var authErr *AuthError
		if errors.As(err, &authErr) {
			attrs = append(attrs, "oauth.httpStatus", authErr.StatusCode, "oauth.errorCode", authErr.Code)
		}
		attrs = append(attrs, logger.Err(err))
		c.base.Error(ctx, "oauth token fetch failed", attrs...)
		return nil, err
	}

	attrs := append(c.baseAttrs(),
		"oauth.expiresInSec", time.Until(tok.ExpiresAt).Seconds(),
		"oauth.durationMs", elapsedMs,
		"oauth.cacheHit", false,
	)
	if tok.InstanceURL != "" {
		attrs = append(attrs, "oauth.instanceUrl", tok.InstanceURL)
	}
	c.base.Info(ctx, "oauth token acquired", attrs...)

	if c.cfg.CacheWrite {
		c.writeCache(ctx, tok)
	}
	return tok, nil
}

// writeCache stores tok in the cache. Failures are logged at Warn and
// otherwise ignored — the caller already has a good token.
func (c *Client) writeCache(ctx context.Context, tok *Token) {
	data, err := tok.marshalCache()
	if err != nil {
		c.base.Warn(ctx, "oauth token cache write failed", append(c.cacheAttrs(), logger.Err(err))...)
		return
	}
	if err := c.cfg.Cache.Set(ctx, c.cfg.CacheKey, data, c.cacheTTL(tok)); err != nil {
		c.base.Warn(ctx, "oauth token cache write failed", append(c.cacheAttrs(), logger.Err(err))...)
	}
}

// cacheTTL derives the store TTL from the token's remaining lifetime minus
// ExpiryMargin, so the cache entry expires slightly before Client would
// consider the token itself invalid on a subsequent read.
func (c *Client) cacheTTL(tok *Token) time.Duration {
	if tok.ExpiresAt.IsZero() {
		return c.cfg.DefaultTTL
	}
	if ttl := time.Until(tok.ExpiresAt) - c.cfg.ExpiryMargin; ttl > 0 {
		return ttl
	}
	return time.Second // still cache briefly rather than pass a non-positive TTL to the store
}

// fetch performs the token request and parses the response. It never returns
// a token and an error together.
func (c *Client) fetch(ctx context.Context) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", string(c.cfg.GrantType))
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)

	switch c.cfg.GrantType {
	case GrantSalesforcePassword:
		form.Set("username", c.cfg.Username)
		form.Set("password", c.cfg.Password+c.cfg.SecurityToken)
	default:
		if len(c.cfg.Scopes) > 0 {
			form.Set("scope", strings.Join(c.cfg.Scopes, " "))
		}
	}
	for k, v := range c.cfg.ExtraParams {
		form.Set(k, v)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("oauth: reading token response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAuthError(resp.StatusCode, body)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("oauth: decoding token response: %w", err)
	}

	return parseTokenResponse(raw, time.Now(), c.cfg.DefaultTTL), nil
}
