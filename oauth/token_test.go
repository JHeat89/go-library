package oauth

import (
	"testing"
	"time"
)

func TestParseTokenResponseExpiresInAsNumber(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	raw := map[string]any{
		"access_token": "tok-1",
		"token_type":   "Bearer",
		"expires_in":   float64(3600),
	}
	tok := parseTokenResponse(raw, now, 15*time.Minute)

	if tok.AccessToken != "tok-1" {
		t.Errorf("AccessToken = %q, want tok-1", tok.AccessToken)
	}
	want := now.Add(3600 * time.Second)
	if !tok.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", tok.ExpiresAt, want)
	}
}

func TestParseTokenResponseExpiresInAsString(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	raw := map[string]any{
		"access_token": "tok-2",
		"expires_in":   "1800",
	}
	tok := parseTokenResponse(raw, now, 15*time.Minute)

	want := now.Add(1800 * time.Second)
	if !tok.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", tok.ExpiresAt, want)
	}
}

func TestParseTokenResponseMissingExpiresInUsesDefaultTTL(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	raw := map[string]any{
		"access_token": "tok-3",
		"instance_url": "https://example.my.salesforce.com",
	}
	tok := parseTokenResponse(raw, now, 15*time.Minute)

	want := now.Add(15 * time.Minute)
	if !tok.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v (default TTL)", tok.ExpiresAt, want)
	}
	if tok.InstanceURL != "https://example.my.salesforce.com" {
		t.Errorf("InstanceURL = %q", tok.InstanceURL)
	}
}

func TestParseTokenResponseIssuedAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Salesforce issued_at is epoch millis as a string.
	raw := map[string]any{
		"access_token": "tok-4",
		"issued_at":    "1767182400000",
	}
	tok := parseTokenResponse(raw, now, 15*time.Minute)

	wantMs := int64(1767182400000)
	if tok.IssuedAt.UnixMilli() != wantMs {
		t.Errorf("IssuedAt = %v (unixMs %d), want unixMs %d", tok.IssuedAt, tok.IssuedAt.UnixMilli(), wantMs)
	}
}

func TestTokenValidBoundaries(t *testing.T) {
	tests := []struct {
		name string
		tok  *Token
		want bool
	}{
		{"nil token", nil, false},
		{"empty access token", &Token{}, false},
		{"zero ExpiresAt trusted", &Token{AccessToken: "a"}, true},
		{"future ExpiresAt", &Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}, true},
		{"past ExpiresAt", &Token{AccessToken: "a", ExpiresAt: time.Now().Add(-time.Hour)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCacheJSONRoundTrip(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	tok := &Token{
		AccessToken: "tok-5",
		TokenType:   "Bearer",
		ExpiresAt:   expiresAt,
		InstanceURL: "https://example.my.salesforce.com",
		Scope:       "api refresh_token",
	}

	data, err := tok.marshalCache()
	if err != nil {
		t.Fatalf("marshalCache: %v", err)
	}

	got, err := unmarshalCache(data)
	if err != nil {
		t.Fatalf("unmarshalCache: %v", err)
	}
	if got.AccessToken != tok.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, tok.AccessToken)
	}
	if got.TokenType != tok.TokenType {
		t.Errorf("TokenType = %q, want %q", got.TokenType, tok.TokenType)
	}
	if !got.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expiresAt)
	}
	if got.InstanceURL != tok.InstanceURL {
		t.Errorf("InstanceURL = %q, want %q", got.InstanceURL, tok.InstanceURL)
	}
	if got.Scope != tok.Scope {
		t.Errorf("Scope = %q, want %q", got.Scope, tok.Scope)
	}
}

func TestCacheJSONMissingExpiresAtTreatedAsValid(t *testing.T) {
	// Simulates an external process writing a token without an expiry,
	// trusting the store's own TTL.
	data := []byte(`{"accessToken":"tok-6","tokenType":"Bearer"}`)

	tok, err := unmarshalCache(data)
	if err != nil {
		t.Fatalf("unmarshalCache: %v", err)
	}
	if !tok.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v, want zero", tok.ExpiresAt)
	}
	if !tok.Valid() {
		t.Error("Valid() = false, want true for a zero-ExpiresAt cache entry")
	}
}

func TestMarshalCacheOmitsZeroExpiresAt(t *testing.T) {
	tok := &Token{AccessToken: "tok-7", TokenType: "Bearer"}
	data, err := tok.marshalCache()
	if err != nil {
		t.Fatalf("marshalCache: %v", err)
	}
	got, err := unmarshalCache(data)
	if err != nil {
		t.Fatalf("unmarshalCache: %v", err)
	}
	if !got.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want zero", got.ExpiresAt)
	}
}
