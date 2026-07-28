// Package logger is the base structured logger for all JHeat89 Go services.
// It wraps the standard library's log/slog with:
//
//   - one-call, config-driven initialization
//   - automatic platform detection (Lambda, ECS, Kubernetes, container, local)
//   - request ID generation and context propagation
//   - request lifecycle tracing (start / stages / end with duration)
//   - Powertools-style performance diagnostics on every completed request
//
// The rest, graphql, etl, and events packages build on this base.
//
// Minimal integration:
//
//	log := logger.Init(logger.Config{
//		Service:     "orders-graph",
//		Version:     "1.4.2",
//		Environment: "prod",
//	})
//	log.Info(ctx, "server listening", "port", 8080)
package logger

import (
	"context"
	"log/slog"
	"os"
	"runtime"
)

// Logger is the base structured logger. All methods take a context so request
// IDs and lifecycle data propagate automatically.
type Logger struct {
	slog  *slog.Logger
	level *slog.LevelVar
	cfg   Config
}

var defaultLogger *Logger

// Init builds a Logger from cfg, stores it as the package default (retrievable
// with Default), and points the standard library's slog default at it so any
// stray slog calls in dependencies stay consistent.
func Init(cfg Config) *Logger {
	l := New(cfg)
	defaultLogger = l
	slog.SetDefault(l.slog)
	return l
}

// Default returns the Logger created by the most recent Init call. It panics
// if Init has not been called, which surfaces wiring mistakes immediately at
// startup rather than losing logs silently.
func Default() *Logger {
	if defaultLogger == nil {
		panic("logger: Default called before Init")
	}
	return defaultLogger
}

// New builds a Logger from cfg without touching package-level state. Prefer
// Init in applications; New is useful in tests and libraries.
func New(cfg Config) *Logger {
	if cfg.Platform == "" {
		cfg.Platform = DetectPlatform()
	}
	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}
	if cfg.Format == "" {
		if cfg.Platform == PlatformLocal {
			cfg.Format = FormatText
		} else {
			cfg.Format = FormatJSON
		}
	}

	level := new(slog.LevelVar)
	level.Set(parseLevel(cfg.Level))

	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: renameAttrs}
	var inner slog.Handler
	if cfg.Format == FormatText {
		inner = slog.NewTextHandler(cfg.Output, opts)
	} else {
		inner = slog.NewJSONHandler(cfg.Output, opts)
	}
	patterns := cfg.RedactPatterns
	if cfg.RedactPII {
		patterns = append(PIIPatterns(), patterns...)
	}
	if r := newRedactor(cfg.RedactKeys, patterns); r != nil {
		inner = redactHandler{inner: inner, r: r}
	}

	base := slog.New(contextHandler{inner: inner}).With(baseAttrs(cfg)...)
	return &Logger{slog: base, level: level, cfg: cfg}
}

func baseAttrs(cfg Config) []any {
	attrs := []any{"service", cfg.Service}
	if cfg.Version != "" {
		attrs = append(attrs, "version", cfg.Version)
	}
	if cfg.Environment != "" {
		attrs = append(attrs, "environment", cfg.Environment)
	}
	attrs = append(attrs, "platform", string(cfg.Platform))
	if cfg.Team != "" {
		attrs = append(attrs, "team", cfg.Team)
	}
	if host, err := os.Hostname(); err == nil {
		attrs = append(attrs, "hostname", host)
	}
	attrs = append(attrs, "pid", os.Getpid(), "goVersion", runtime.Version())
	attrs = append(attrs, platformAttrs(cfg.Platform)...)
	for k, v := range cfg.StaticFields {
		attrs = append(attrs, k, v)
	}
	return attrs
}

// Debug logs at debug level. args are alternating key/value pairs, as in slog.
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.slog.DebugContext(ctx, msg, args...)
}

// Info logs at info level.
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.slog.InfoContext(ctx, msg, args...)
}

// Warn logs at warn level.
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.slog.WarnContext(ctx, msg, args...)
}

// Error logs at error level. Use logger.Err(err) to attach the error value.
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.slog.ErrorContext(ctx, msg, args...)
}

// With returns a child logger that always carries the given key/value pairs.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{slog: l.slog.With(args...), level: l.level, cfg: l.cfg}
}

// SetLevel changes the minimum level at runtime, e.g. to enable debug logging
// on a live instance while triaging.
func (l *Logger) SetLevel(level string) {
	l.level.Set(parseLevel(level))
}

// Slog exposes the underlying *slog.Logger for libraries that require one.
// Context-based request correlation still applies.
func (l *Logger) Slog() *slog.Logger { return l.slog }

// Config returns the configuration the logger was built with.
func (l *Logger) Config() Config { return l.cfg }

// LogPerformance emits an on-demand performance diagnostic line.
func (l *Logger) LogPerformance(ctx context.Context, msg string) {
	l.Info(ctx, msg, "performance", CapturePerformance())
}

// Err is a convenience for attaching errors: log.Error(ctx, "save failed", logger.Err(err)).
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.String("error", err.Error())
}
