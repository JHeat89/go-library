package logger

import (
	"context"
	"log/slog"
)

// contextHandler decorates an slog.Handler so that any record logged with a
// context automatically gains the request ID and current lifecycle name.
// Because it sits at the handler level, even raw slog usage via Logger.Slog()
// keeps request correlation.
type contextHandler struct {
	inner slog.Handler
}

func (h contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("requestId", id))
	}
	return h.inner.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{inner: h.inner.WithGroup(name)}
}

// renameAttrs maps slog's built-in keys to the field names our log platform
// expects: time -> timestamp, msg -> message, level -> lowercase string.
func renameAttrs(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		a.Key = "timestamp"
	case slog.MessageKey:
		a.Key = "message"
	case slog.LevelKey:
		if lvl, ok := a.Value.Any().(slog.Level); ok {
			a.Value = slog.StringValue(levelString(lvl))
		}
	}
	return a
}

func levelString(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}
