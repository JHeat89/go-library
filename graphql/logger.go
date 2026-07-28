// Package graphql is a server-agnostic GraphQL operation logger built on the
// base logger. It knows how to log operations (name, type, query, variables
// with redaction) and resolver errors, but has no dependency on any GraphQL
// server library. Use the graphql/gqlgen subpackage for a drop-in gqlgen
// integration, or wire OperationStart into any other server yourself.
package graphql

import (
	"context"

	"github.com/JHeat89/go-library/logger"
)

const redactedPlaceholder = "[REDACTED]"

// Operation describes a GraphQL operation about to execute.
type Operation struct {
	// Name is the operation name, e.g. "GetOrders". May be empty for
	// anonymous operations.
	Name string
	// Type is "query", "mutation", or "subscription".
	Type string
	// Query is the raw GraphQL document.
	Query string
	// Variables are the operation variables. Redaction rules apply before
	// logging.
	Variables map[string]any
}

// Logger logs GraphQL operations using the base logger.
type Logger struct {
	base         *logger.Logger
	logVariables bool
	redact       map[string]bool
	maxQueryLen  int
}

// Option customizes the GraphQL logger.
type Option func(*Logger)

// WithVariables enables or disables logging of operation variables entirely.
// Enabled by default; disable in services handling sensitive payloads.
func WithVariables(enabled bool) Option {
	return func(l *Logger) { l.logVariables = enabled }
}

// WithRedactedVariables replaces the named top-level variables with
// "[REDACTED]" in logs, e.g. WithRedactedVariables("password", "ssn", "token").
func WithRedactedVariables(names ...string) Option {
	return func(l *Logger) {
		for _, n := range names {
			l.redact[n] = true
		}
	}
}

// WithMaxQueryLength truncates logged query documents beyond n characters to
// keep log lines bounded. Default 2000; 0 disables query logging.
func WithMaxQueryLength(n int) Option {
	return func(l *Logger) { l.maxQueryLen = n }
}

// New builds a GraphQL logger on top of the base logger.
func New(base *logger.Logger, opts ...Option) *Logger {
	l := &Logger{
		base:         base,
		logVariables: true,
		redact:       map[string]bool{},
		maxQueryLen:  2000,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// OperationStart begins the lifecycle trace for a GraphQL operation. It
// guarantees a request ID, logs the operation with redacted variables, and
// returns a finish func to call when execution completes with any errors the
// server produced. The finish func logs each error and the completion line
// with duration and performance diagnostics.
func (l *Logger) OperationStart(ctx context.Context, op Operation) (context.Context, func(errs ...error)) {
	name := op.Name
	if name == "" {
		name = "anonymous"
	}
	opType := op.Type
	if opType == "" {
		opType = "query"
	}

	attrs := []any{
		"graphql.operation", name,
		"graphql.type", opType,
	}
	if l.maxQueryLen > 0 && op.Query != "" {
		attrs = append(attrs, "graphql.query", truncate(op.Query, l.maxQueryLen))
	}
	if l.logVariables && len(op.Variables) > 0 {
		attrs = append(attrs, "graphql.variables", l.redactVariables(op.Variables))
	}

	ctx, lc := l.base.StartRequest(ctx, "graphql "+opType+" "+name, attrs...)
	l.base.Info(ctx, "graphql operation started", attrs...)

	return ctx, func(errs ...error) {
		errCount := 0
		for _, err := range errs {
			if err == nil {
				continue
			}
			errCount++
			l.base.Error(ctx, "graphql operation error",
				"graphql.operation", name,
				"graphql.type", opType,
				logger.Err(err),
			)
		}
		lc.End(ctx,
			"graphql.operation", name,
			"graphql.type", opType,
			"graphql.errorCount", errCount,
		)
	}
}

// Stage records a resolver-level checkpoint on the current operation's
// lifecycle, e.g. gql.Stage(ctx, "resolver.orders.items").
func (l *Logger) Stage(ctx context.Context, name string, attrs ...any) {
	logger.LifecycleFrom(ctx).Stage(ctx, name, attrs...)
}

// Base exposes the underlying base logger for ad-hoc logging in resolvers.
func (l *Logger) Base() *logger.Logger { return l.base }

func (l *Logger) redactVariables(vars map[string]any) map[string]any {
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		if l.redact[k] {
			out[k] = redactedPlaceholder
		} else {
			out[k] = v
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
