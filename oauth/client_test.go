package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JHeat89/go-library/logger"
)

func newTestLogger(buf *bytes.Buffer) *logger.Logger {
	return logger.New(logger.Config{
		Service:  "test-svc",
		Platform: logger.PlatformLocal,
		Format:   logger.FormatJSON,
		Level:    "debug",
		Output:   buf,
	})
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// fakeCache is an in-memory TokenCache for tests, with knobs for injecting
// read/write errors.
type fakeCache struct {
	mu       sync.Mutex
	data     map[string][]byte
	getErr   error
	setErr   error
	setCalls int
	lastTTL  time.Duration
}

func newFakeCache() *fakeCache {
	return &fakeCache{data: map[string][]byte{}}
}

func (f *fakeCache) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.data[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	return v, nil
}

func (f *fakeCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.lastTTL = ttl
	if f.setErr != nil {
		return f.setErr
	}
	f.data[key] = value
	return nil
}

func (f *fakeCache) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func TestTokenClientCredentialsSuccess(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "secret-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        "api",
		})
	}))
	defer srv.Close()

	client, err := New(log, Config{
		GrantType:    GrantClientCredentials,
		TokenURL:     srv.URL,
		ClientID:     "client-1",
		ClientSecret: "top-secret",
		Scopes:       []string{"api", "refresh_token"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := time.Now()
	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if gotForm.Get("grant_type") != "client_credentials" {
		t.Errorf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("client_id") != "client-1" {
		t.Errorf("client_id = %q", gotForm.Get("client_id"))
	}
	if gotForm.Get("client_secret") != "top-secret" {
		t.Errorf("client_secret = %q", gotForm.Get("client_secret"))
	}
	if gotForm.Get("scope") != "api refresh_token" {
		t.Errorf("scope = %q", gotForm.Get("scope"))
	}

	if tok.AccessToken != "secret-access-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if tok.TokenType != "Bearer" {
		t.Errorf("TokenType = %q", tok.TokenType)
	}
	wantExpiry := before.Add(3600 * time.Second)
	if diff := tok.ExpiresAt.Sub(wantExpiry); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("ExpiresAt = %v, want ~%v", tok.ExpiresAt, wantExpiry)
	}
}

func TestTokenSalesforcePasswordSuccess(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "sf-access-token",
			"token_type":   "Bearer",
			"instance_url": "https://example.my.salesforce.com",
			"issued_at":    "1767182400000",
			// no expires_in — Salesforce omits it.
		})
	}))
	defer srv.Close()

	client, err := New(log, Config{
		GrantType:     GrantSalesforcePassword,
		TokenURL:      srv.URL,
		ClientID:      "client-2",
		ClientSecret:  "client-secret-2",
		Username:      "user@example.com",
		Password:      "my-password",
		SecurityToken: "sectok123",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := time.Now()
	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if gotForm.Get("grant_type") != "password" {
		t.Errorf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("username") != "user@example.com" {
		t.Errorf("username = %q", gotForm.Get("username"))
	}
	if gotForm.Get("password") != "my-passwordsectok123" {
		t.Errorf("password = %q, want password+securityToken concatenated", gotForm.Get("password"))
	}

	if tok.InstanceURL != "https://example.my.salesforce.com" {
		t.Errorf("InstanceURL = %q", tok.InstanceURL)
	}
	if tok.IssuedAt.UnixMilli() != 1767182400000 {
		t.Errorf("IssuedAt unixMs = %d", tok.IssuedAt.UnixMilli())
	}

	wantExpiry := before.Add(15 * time.Minute) // DefaultTTL
	if diff := tok.ExpiresAt.Sub(wantExpiry); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("ExpiresAt = %v, want ~%v (DefaultTTL)", tok.ExpiresAt, wantExpiry)
	}
}

func TestTokenFetchAuthError(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_grant",
			"error_description": "authentication failure",
		})
	}))
	defer srv.Close()

	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-3",
		ClientSecret: "client-secret-3",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Token(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("error is not *AuthError: %v", err)
	}
	if authErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d", authErr.StatusCode)
	}
	if authErr.Code != "invalid_grant" {
		t.Errorf("Code = %q", authErr.Code)
	}

	var found bool
	for _, m := range logLines(t, &buf) {
		if m["message"] == "oauth token fetch failed" {
			found = true
			if m["level"] != "error" {
				t.Errorf("level = %v, want error", m["level"])
			}
			if m["oauth.httpStatus"] != float64(http.StatusBadRequest) {
				t.Errorf("oauth.httpStatus = %v", m["oauth.httpStatus"])
			}
			if m["oauth.errorCode"] != "invalid_grant" {
				t.Errorf("oauth.errorCode = %v", m["oauth.errorCode"])
			}
		}
	}
	if !found {
		t.Error(`expected an "oauth token fetch failed" log line`)
	}
}

func TestTokenCacheHitSkipsFetch(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-4",
		ClientSecret: "secret-4",
		Cache:        cache,
		CacheRead:    true,
		CacheWrite:   true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cached := &Token{AccessToken: "cached-token", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
	data, err := cached.marshalCache()
	if err != nil {
		t.Fatalf("marshalCache: %v", err)
	}
	cache.data[client.CacheKey()] = data

	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "cached-token" {
		t.Errorf("AccessToken = %q, want cached-token", tok.AccessToken)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server hit count = %d, want 0 (should not fetch on cache hit)", hits)
	}
}

func TestTokenCacheMissFetchesAndWrites(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-token", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-5",
		ClientSecret: "secret-5",
		Cache:        cache,
		CacheRead:    true,
		CacheWrite:   true,
		ExpiryMargin: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "fresh-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache.setCalls = %d, want 1", cache.setCalls)
	}

	wantTTL := 3600*time.Second - 30*time.Second
	if diff := cache.lastTTL - wantTTL; diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("cache TTL = %v, want ~%v (expires_in - margin)", cache.lastTTL, wantTTL)
	}
	if _, ok := cache.data[client.CacheKey()]; !ok {
		t.Error("token was not written to cache")
	}
}

func TestTokenExpiredCacheEntryRefetches(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-token", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-6",
		ClientSecret: "secret-6",
		Cache:        cache,
		CacheRead:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expired := &Token{AccessToken: "old-token", ExpiresAt: time.Now().Add(-time.Hour)}
	data, _ := expired.marshalCache()
	cache.data[client.CacheKey()] = data

	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "new-token" {
		t.Errorf("AccessToken = %q, want new-token (refetched)", tok.AccessToken)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("server hit count = %d, want 1", hits)
	}
}

func TestTokenCacheEntryWithoutExpiresAtIsTrusted(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "should-not-be-used", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-7",
		ClientSecret: "secret-7",
		Cache:        cache,
		CacheRead:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cache.data[client.CacheKey()] = []byte(`{"accessToken":"externally-refreshed","tokenType":"Bearer"}`)

	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "externally-refreshed" {
		t.Errorf("AccessToken = %q, want externally-refreshed", tok.AccessToken)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server hit count = %d, want 0 (no-expiresAt entry should be trusted)", hits)
	}
}

func TestRefreshBypassesCacheAndOverwrites(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "refreshed-token", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-8",
		ClientSecret: "secret-8",
		Cache:        cache,
		CacheRead:    true,
		CacheWrite:   true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	valid := &Token{AccessToken: "still-valid-token", ExpiresAt: time.Now().Add(time.Hour)}
	data, _ := valid.marshalCache()
	cache.data[client.CacheKey()] = data

	tok, err := client.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q, want refreshed-token", tok.AccessToken)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("server hit count = %d, want 1 (Refresh must always fetch)", hits)
	}

	stored, err := unmarshalCache(cache.data[client.CacheKey()])
	if err != nil {
		t.Fatalf("unmarshalCache: %v", err)
	}
	if stored.AccessToken != "refreshed-token" {
		t.Errorf("cached token = %q, want overwritten with refreshed-token", stored.AccessToken)
	}
}

func TestTokenCacheReadErrorFallsThrough(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fetched-after-cache-error", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	cache.getErr = errors.New("redis: connection refused")
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-9",
		ClientSecret: "secret-9",
		Cache:        cache,
		CacheRead:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "fetched-after-cache-error" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}

	var warned bool
	for _, m := range logLines(t, &buf) {
		if m["message"] == "oauth token cache read failed" && m["level"] == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Error(`expected a Warn "oauth token cache read failed" log line`)
	}
}

func TestTokenCacheWriteErrorStillReturnsToken(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-despite-write-failure", "expires_in": 3600})
	}))
	defer srv.Close()

	cache := newFakeCache()
	cache.setErr = errors.New("redis: readonly replica")
	client, err := New(log, Config{
		TokenURL:     srv.URL,
		ClientID:     "client-10",
		ClientSecret: "secret-10",
		Cache:        cache,
		CacheWrite:   true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tok, err := client.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "token-despite-write-failure" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}

	var warned bool
	for _, m := range logLines(t, &buf) {
		if m["message"] == "oauth token cache write failed" && m["level"] == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Error(`expected a Warn "oauth token cache write failed" log line`)
	}
}

func TestInvalidateDeletesCachedToken(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	cache := newFakeCache()
	client, err := New(log, Config{
		TokenURL:     "https://example.invalid/token",
		ClientID:     "client-11",
		ClientSecret: "secret-11",
		Cache:        cache,
		CacheRead:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cache.data[client.CacheKey()] = []byte(`{"accessToken":"to-be-invalidated"}`)

	if err := client.Invalidate(context.Background()); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := cache.data[client.CacheKey()]; ok {
		t.Error("cache entry still present after Invalidate")
	}
}

// TestSecretsNeverAppearInLogs is the secrecy assertion: across every code
// path (fetch success, cache hit, refresh, invalidate, fetch failure) the
// log output must never contain a client secret, password, security token,
// or access token value.
func TestSecretsNeverAppearInLogs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	const (
		clientSecret  = "sUpEr-cLiEnT-sEcReT"
		password      = "sUpEr-pAsSwOrD"
		securityToken = "sEcUrItY-tOkEn-999"
		accessToken   = "sEcReT-AcCeSs-TOKEN-abc123"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
			"instance_url": "https://example.my.salesforce.com",
			"issued_at":    "1767182400000",
		})
	}))
	defer srv.Close()

	cache := newFakeCache()
	client, err := New(log, Config{
		GrantType:     GrantSalesforcePassword,
		TokenURL:      srv.URL,
		ClientID:      "client-secrecy",
		ClientSecret:  clientSecret,
		Username:      "user@example.com",
		Password:      password,
		SecurityToken: securityToken,
		Cache:         cache,
		CacheRead:     true,
		CacheWrite:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if _, err := client.Token(ctx); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if _, err := client.Token(ctx); err != nil { // cache hit path
		t.Fatalf("Token (cache hit): %v", err)
	}
	if _, err := client.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := client.Invalidate(ctx); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	// Also exercise the fetch-failure path with a distinct client.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_grant",
			"error_description": "authentication failure",
		})
	}))
	defer errSrv.Close()
	errClient, err := New(log, Config{
		GrantType:     GrantSalesforcePassword,
		TokenURL:      errSrv.URL,
		ClientID:      "client-secrecy-2",
		ClientSecret:  clientSecret,
		Username:      "user@example.com",
		Password:      password,
		SecurityToken: securityToken,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := errClient.Token(ctx); err == nil {
		t.Fatal("expected error")
	}

	out := buf.String()
	for _, secret := range []string{clientSecret, password, securityToken, accessToken, password + securityToken} {
		if strings.Contains(out, secret) {
			t.Errorf("log output contains secret %q:\n%s", secret, out)
		}
	}
}
