package logger

import (
	"context"

	"github.com/JHeat89/go-library/requestid"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	lifecycleKey
)

// WithRequestID returns a context carrying the given request ID. Every log
// line written with this context automatically includes it.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request ID stored in ctx, or "" if none is set.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// EnsureRequestID returns a context that is guaranteed to carry a request ID,
// generating a new UUID if one is not already present. Call this at the very
// edge of the application (HTTP middleware, Kafka consumer, Lambda handler)
// so the ID persists for the life of the request.
func EnsureRequestID(ctx context.Context) (context.Context, string) {
	if id := RequestID(ctx); id != "" {
		return ctx, id
	}
	id := requestid.New()
	return WithRequestID(ctx, id), id
}
