// Package secrets loads secrets and parameters from a backing Source, with
// structured logging through the base logger. The core package has zero
// third-party dependencies; the AWS Secrets Manager / SSM Parameter Store
// implementation lives in the secrets/aws subpackage, so consumers who don't
// load secrets from AWS never pull in the SDK.
//
// Values are never logged — only names, paths, source, duration, and (for
// batch reads) counts.
//
// Loader does not cache: secrets and parameters are typically loaded once at
// service startup and held by the caller for the life of the process. If a
// TTL cache becomes necessary, add one on top of Loader (or in a future
// version of this package) rather than baking it into v1.
//
//	log := logger.Init(logger.Config{Service: "orders-api"})
//	cfg, err := config.LoadDefaultConfig(ctx)
//	loader := secrets.New(log, awssecrets.New(cfg))
//	creds, err := loader.SecretJSON(ctx, "orders/db")
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/JHeat89/go-library/logger"
)

const (
	sourceSecretsManager = "secretsManager"
	sourceParameterStore = "parameterStore"
)

// Loader adds uniform structured logging, JSON parsing, and request-id
// correlation on top of a Source.
type Loader struct {
	base *logger.Logger
	src  Source
}

// New builds a Loader over src. The logger is injected like
// events.New/oauth.New — never logger.Default() — so callers built on Loader
// stay testable and applications control wiring explicitly.
func New(base *logger.Logger, src Source) *Loader {
	return &Loader{base: base, src: src}
}

// Secret returns the raw value of the named secret.
func (l *Loader) Secret(ctx context.Context, name string) (string, error) {
	return l.fetchSecret(ctx, name)
}

// SecretJSON fetches a secret and parses it as a JSON object of string
// values — the common Secrets Manager key/value shape
// ({"username":"...","password":"..."}).
func (l *Loader) SecretJSON(ctx context.Context, name string) (map[string]string, error) {
	val, err := l.fetchSecret(ctx, name)
	if err != nil {
		return nil, err
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(val), &m); err != nil {
		ctx, _ = logger.EnsureRequestID(ctx)
		l.base.Error(ctx, "secret JSON parse failed",
			"secrets.name", name,
			"secrets.source", sourceSecretsManager,
			logger.Err(err),
		)
		return nil, fmt.Errorf("secrets: parsing %q as JSON: %w", name, err)
	}
	return m, nil
}

// Parameter returns the decrypted value of the named parameter.
func (l *Loader) Parameter(ctx context.Context, name string) (string, error) {
	ctx, _ = logger.EnsureRequestID(ctx)
	logger.LifecycleFrom(ctx).Stage(ctx, "secrets.load")

	start := time.Now()
	val, err := l.src.Parameter(ctx, name)
	attrs := []any{
		"secrets.name", name,
		"secrets.source", sourceParameterStore,
		"secrets.durationMs", durationMs(start),
	}

	if err != nil {
		l.base.Error(ctx, parameterErrMessage(err), append(attrs, logger.Err(err))...)
		return "", err
	}

	l.base.Debug(ctx, "parameter loaded", attrs...)
	return val, nil
}

// ParametersByPath returns every parameter under path, recursively and
// decrypted, keyed by full parameter name.
func (l *Loader) ParametersByPath(ctx context.Context, path string) (map[string]string, error) {
	ctx, _ = logger.EnsureRequestID(ctx)
	logger.LifecycleFrom(ctx).Stage(ctx, "secrets.load")

	start := time.Now()
	vals, err := l.src.ParametersByPath(ctx, path)
	attrs := []any{
		"secrets.path", path,
		"secrets.source", sourceParameterStore,
		"secrets.durationMs", durationMs(start),
	}

	if err != nil {
		l.base.Error(ctx, parameterErrMessage(err), append(attrs, logger.Err(err))...)
		return nil, err
	}

	l.base.Debug(ctx, "parameters loaded", append(attrs, "secrets.count", len(vals))...)
	return vals, nil
}

// fetchSecret is the shared implementation behind Secret and SecretJSON: it
// fetches, times, and logs a single secret read.
func (l *Loader) fetchSecret(ctx context.Context, name string) (string, error) {
	ctx, _ = logger.EnsureRequestID(ctx)
	logger.LifecycleFrom(ctx).Stage(ctx, "secrets.load")

	start := time.Now()
	val, err := l.src.Secret(ctx, name)
	attrs := []any{
		"secrets.name", name,
		"secrets.source", sourceSecretsManager,
		"secrets.durationMs", durationMs(start),
	}

	if err != nil {
		l.base.Error(ctx, secretErrMessage(err), append(attrs, logger.Err(err))...)
		return "", err
	}

	l.base.Debug(ctx, "secret loaded", attrs...)
	return val, nil
}

func secretErrMessage(err error) string {
	if errors.Is(err, ErrNotFound) {
		return "secret not found"
	}
	return "secret load failed"
}

func parameterErrMessage(err error) string {
	if errors.Is(err, ErrNotFound) {
		return "parameter not found"
	}
	return "parameter load failed"
}

func durationMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
