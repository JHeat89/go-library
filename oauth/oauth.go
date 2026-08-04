// Package oauth acquires OAuth 2.0 access tokens for machine-to-machine
// calls: the standard client_credentials grant, and Salesforce's
// username/password flow. It has zero third-party dependencies — HTTP via
// net/http, JSON via encoding/json. Optional Redis-backed caching lives in
// the oauth/redis subpackage, so consumers who don't cache tokens never pull
// in go-redis.
//
// There is no in-memory token cache: every Client.Token call either reads
// Config.Cache (when CacheRead is set) or fetches a fresh token — never both,
// and never a process-local map. This is deliberate: an external process
// (a sidecar refresher, another instance of the same service) may refresh
// the cached token out-of-band, and an in-process cache would drift out of
// sync with it. For the same reason there is no singleflight/dedup for
// concurrent callers; duplicate fetches are idempotent and benign, and
// hand-rolled coordination would cut against the "no local state" decision
// while pulling the core away from zero-dependency, lock-free code.
//
//	log := logger.Init(logger.Config{Service: "orders-api"})
//	client, err := oauth.New(log, oauth.Config{
//		GrantType:    oauth.GrantClientCredentials,
//		TokenURL:     "https://auth.example.com/oauth2/token",
//		ClientID:     os.Getenv("OAUTH_CLIENT_ID"),
//		ClientSecret: os.Getenv("OAUTH_CLIENT_SECRET"),
//		Cache:        redisCache, // oauth/redis.New(redisClient)
//		CacheRead:    true,
//		CacheWrite:   true,
//	})
//	tok, err := client.Token(ctx)
//	req.Header.Set("Authorization", tok.TokenType+" "+tok.AccessToken)
package oauth

import (
	"errors"
	"net/http"
	"time"

	"github.com/JHeat89/go-library/logger"
)

// GrantType selects the OAuth 2.0 flow Client uses to acquire tokens.
type GrantType string

const (
	// GrantClientCredentials is the standard client_credentials grant.
	GrantClientCredentials GrantType = "client_credentials"
	// GrantSalesforcePassword is Salesforce's username/password flow. The
	// security token, when configured, is appended directly to the password
	// in the form body per Salesforce's convention.
	GrantSalesforcePassword GrantType = "password"
)

const (
	defaultTimeout      = 10 * time.Second
	defaultExpiryMargin = 60 * time.Second
	defaultTTL          = 15 * time.Minute
)

// Config configures a Client. TokenURL, ClientID, and ClientSecret are always
// required; Username and Password are additionally required for
// GrantSalesforcePassword.
type Config struct {
	// GrantType selects the flow. Defaults to GrantClientCredentials.
	GrantType GrantType
	// TokenURL is the token endpoint, e.g.
	// https://login.salesforce.com/services/oauth2/token.
	TokenURL string

	ClientID     string
	ClientSecret string

	// Username, Password, and SecurityToken are used by
	// GrantSalesforcePassword only. SecurityToken is appended directly to
	// Password in the form body when set, so a wrong or missing token
	// surfaces as an ordinary invalid_grant error rather than a distinct
	// field.
	Username      string
	Password      string
	SecurityToken string

	// Scopes is sent as a space-separated "scope" form field, typically used
	// with GrantClientCredentials.
	Scopes []string
	// ExtraParams adds extra form fields to the token request, e.g.
	// "audience" for providers that require it.
	ExtraParams map[string]string

	// HTTPClient performs the token request. Defaults to
	// &http.Client{Timeout: 10 * time.Second}.
	HTTPClient *http.Client

	// Cache backs CacheRead/CacheWrite. There is no in-memory cache; see the
	// package doc for why. Required when CacheRead or CacheWrite is set.
	Cache TokenCache
	// CacheRead consults Cache before fetching a fresh token.
	CacheRead bool
	// CacheWrite stores fetched tokens in Cache with a TTL derived from the
	// token's remaining lifetime and ExpiryMargin.
	CacheWrite bool
	// CacheKey overrides the derived cache key. See Client.CacheKey.
	CacheKey string

	// ExpiryMargin is subtracted from a token's remaining lifetime for both
	// cache TTL and validity checks, so a token is treated as expired (and
	// refreshed) slightly before it truly is, rather than racing a
	// downstream 401. Defaults to 60s.
	ExpiryMargin time.Duration
	// DefaultTTL is used when the token response omits expires_in, as
	// Salesforce's password flow always does. Defaults to 15m.
	DefaultTTL time.Duration
}

// Client acquires and optionally caches OAuth 2.0 access tokens.
type Client struct {
	cfg  Config
	base *logger.Logger
}

// New validates cfg, applies defaults, and returns a Client. The logger is
// injected like events.New/graphql.New — never logger.Default() — so
// libraries built on Client stay testable and applications control wiring
// explicitly.
func New(base *logger.Logger, cfg Config) (*Client, error) {
	if cfg.TokenURL == "" {
		return nil, errors.New("oauth: TokenURL is required")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("oauth: ClientID is required")
	}
	if cfg.ClientSecret == "" {
		return nil, errors.New("oauth: ClientSecret is required")
	}
	if cfg.GrantType == "" {
		cfg.GrantType = GrantClientCredentials
	}
	if cfg.GrantType == GrantSalesforcePassword && (cfg.Username == "" || cfg.Password == "") {
		return nil, errors.New("oauth: Username and Password are required for the Salesforce password grant")
	}
	if (cfg.CacheRead || cfg.CacheWrite) && cfg.Cache == nil {
		return nil, errors.New("oauth: Cache is required when CacheRead or CacheWrite is set")
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	if cfg.ExpiryMargin == 0 {
		cfg.ExpiryMargin = defaultExpiryMargin
	}
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = defaultTTL
	}
	if cfg.CacheKey == "" {
		cfg.CacheKey = defaultCacheKey(cfg)
	}

	return &Client{cfg: cfg, base: base}, nil
}

// CacheKey returns the effective cache key (Config.CacheKey if set, otherwise
// the derived default), useful for ops tooling or an external out-of-band
// writer that needs to target the same entry.
func (c *Client) CacheKey() string { return c.cfg.CacheKey }
