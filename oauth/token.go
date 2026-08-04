package oauth

import (
	"encoding/json"
	"strconv"
	"time"
)

// Token is an OAuth 2.0 access token plus the metadata needed to know when it
// expires and, for Salesforce, where to send subsequent API calls.
type Token struct {
	AccessToken string
	TokenType   string
	// ExpiresAt is now+expires_in from the token response, or
	// now+Config.DefaultTTL when the response omits expires_in (Salesforce's
	// password flow always omits it).
	ExpiresAt   time.Time
	Scope       string
	InstanceURL string    // Salesforce
	IssuedAt    time.Time // Salesforce issued_at (epoch-millis string)

	// Raw is the full decoded token response. It is never logged — treat any
	// value reachable through it as a secret.
	Raw map[string]any
}

// Valid reports whether t carries an access token and has not expired. A
// zero ExpiresAt is treated as valid: a cache entry written without an
// expiry (see the cache JSON contract in the package doc) trusts the store's
// own TTL instead of an embedded timestamp, so an external refresher only
// needs to set a sensible TTL when it writes the entry.
func (t *Token) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Before(t.ExpiresAt)
}

// parseTokenResponse builds a Token from a decoded token endpoint response.
// now is injected so tests are deterministic. expires_in is read loosely
// (JSON number or a numeric string) since not every provider encodes it the
// same way; when absent entirely, defaultTTL applies.
func parseTokenResponse(raw map[string]any, now time.Time, defaultTTL time.Duration) *Token {
	t := &Token{Raw: raw}
	t.AccessToken, _ = raw["access_token"].(string)
	t.TokenType, _ = raw["token_type"].(string)
	t.Scope, _ = raw["scope"].(string)
	t.InstanceURL, _ = raw["instance_url"].(string)

	ttl := defaultTTL
	if secs, ok := numericField(raw["expires_in"]); ok {
		ttl = time.Duration(secs * float64(time.Second))
	}
	t.ExpiresAt = now.Add(ttl)

	if s, ok := raw["issued_at"].(string); ok && s != "" {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			t.IssuedAt = time.UnixMilli(ms)
		}
	}
	return t
}

// numericField reads a JSON-decoded value that may be a float64 (ordinary
// JSON number) or a string containing one — some OAuth proxies send
// expires_in as a quoted string.
func numericField(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// cacheEntry is the JSON shape written to and read from TokenCache. It is a
// stable, documented contract — an external process (a sidecar refresher,
// another service instance) may write it directly — so field names and the
// missing/zero-expiresAt behavior must not change without a migration plan.
type cacheEntry struct {
	AccessToken string `json:"accessToken"`
	TokenType   string `json:"tokenType"`
	// ExpiresAt is unix seconds; 0 or absent means "trust the store's TTL"
	// (see Token.Valid).
	ExpiresAt   int64  `json:"expiresAt,omitempty"`
	InstanceURL string `json:"instanceUrl,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// marshalCache serializes t into the cache JSON contract.
func (t *Token) marshalCache() ([]byte, error) {
	e := cacheEntry{
		AccessToken: t.AccessToken,
		TokenType:   t.TokenType,
		InstanceURL: t.InstanceURL,
		Scope:       t.Scope,
	}
	if !t.ExpiresAt.IsZero() {
		e.ExpiresAt = t.ExpiresAt.Unix()
	}
	return json.Marshal(e)
}

// unmarshalCache parses the cache JSON contract into a Token.
func unmarshalCache(data []byte) (*Token, error) {
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	t := &Token{
		AccessToken: e.AccessToken,
		TokenType:   e.TokenType,
		InstanceURL: e.InstanceURL,
		Scope:       e.Scope,
	}
	if e.ExpiresAt != 0 {
		t.ExpiresAt = time.Unix(e.ExpiresAt, 0)
	}
	return t, nil
}
