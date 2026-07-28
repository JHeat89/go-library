package logger

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

// Redacted is the placeholder written in place of sensitive values.
const Redacted = "[REDACTED]"

// Pattern scrubs matches of Regexp out of string values, replacing only the
// matched portion so the rest of the text stays readable. Replacement
// defaults to "[REDACTED]" and supports capture-group references ($1, ${name})
// per regexp.ReplaceAllString, which lets you keep context words:
//
//	logger.Pattern{
//		Regexp:      regexp.MustCompile(`(?i)(customer )(\S+)`),
//		Replacement: "${1}[REDACTED]",
//	}
//	// "Starting Execution for Customer Joey" -> "Starting Execution for Customer [REDACTED]"
type Pattern struct {
	Regexp      *regexp.Regexp
	Replacement string
}

// PIIPatterns returns built-in detectors for structured PII that is reliably
// recognizable in free text: email addresses, US SSNs, credit card numbers,
// and phone numbers. Arbitrary personal names are NOT detectable by rules —
// use a custom Pattern for known message shapes, or redact the field by key.
func PIIPatterns() []Pattern {
	return []Pattern{
		{Regexp: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
		{Regexp: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)},
		{Regexp: regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)},
		{Regexp: regexp.MustCompile(`\b(?:\+?\d{1,2}[ .-]?)?\(?\d{3}\)?[ .-]?\d{3}[ .-]?\d{4}\b`)},
	}
}

// Redactor replaces the values of sensitive keys with "[REDACTED]" anywhere
// they appear: top-level attributes, slog groups, and arbitrarily nested
// maps/slices. Key matching is case-insensitive ("Password" == "password").
//
// Set Config.RedactKeys to apply a redactor to every log line the logger
// emits; use NewRedactor directly for ad-hoc scrubbing.
type Redactor struct {
	keys     map[string]bool
	patterns []Pattern
}

// NewRedactor builds a Redactor for the given key names. Returns nil when no
// keys are given; a nil Redactor passes values through untouched.
func NewRedactor(keys ...string) *Redactor {
	return newRedactor(keys, nil)
}

func newRedactor(keys []string, patterns []Pattern) *Redactor {
	if len(keys) == 0 && len(patterns) == 0 {
		return nil
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[strings.ToLower(k)] = true
	}
	return &Redactor{keys: set, patterns: patterns}
}

// scrub applies every pattern to s, replacing only matched portions.
func (r *Redactor) scrub(s string) string {
	if r == nil || len(r.patterns) == 0 {
		return s
	}
	for _, p := range r.patterns {
		repl := p.Replacement
		if repl == "" {
			repl = Redacted
		}
		s = p.Regexp.ReplaceAllString(s, repl)
	}
	return s
}

func (r *Redactor) match(key string) bool {
	return r != nil && r.keys[strings.ToLower(key)]
}

// Attr returns a copy of a with sensitive values redacted, recursing into
// groups and nested any-typed values. LogValuers are resolved first so their
// expanded form is scrubbed too.
func (r *Redactor) Attr(a slog.Attr) slog.Attr {
	if r == nil {
		return a
	}
	if r.match(a.Key) {
		a.Value = slog.StringValue(Redacted)
		return a
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		group := v.Group()
		out := make([]slog.Attr, len(group))
		for i, ga := range group {
			out[i] = r.Attr(ga)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindAny:
		a.Value = slog.AnyValue(r.Any(v.Any()))
	case slog.KindString:
		a.Value = slog.StringValue(r.scrub(v.String()))
	default:
		a.Value = v
	}
	return a
}

// Map returns a deep copy of m with sensitive keys redacted at any depth.
func (r *Redactor) Map(m map[string]any) map[string]any {
	if r == nil || m == nil {
		return m
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		if r.match(k) {
			out[k] = Redacted
		} else {
			out[k] = r.Any(val)
		}
	}
	return out
}

// Any redacts within the supported container types (map[string]any,
// map[string]string, []any); other values pass through unchanged.
func (r *Redactor) Any(v any) any {
	if r == nil {
		return v
	}
	switch t := v.(type) {
	case string:
		return r.scrub(t)
	case map[string]any:
		return r.Map(t)
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, val := range t {
			if r.match(k) {
				out[k] = Redacted
			} else {
				out[k] = r.scrub(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = r.Any(e)
		}
		return out
	default:
		return v
	}
}

// redactHandler scrubs every record (and pre-bound attrs) before the inner
// handler serializes them, so redaction applies no matter which package or
// raw slog call produced the log line.
type redactHandler struct {
	inner slog.Handler
	r     *Redactor
}

func (h redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.scrub(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.r.Attr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		scrubbed[i] = h.r.Attr(a)
	}
	return redactHandler{inner: h.inner.WithAttrs(scrubbed), r: h.r}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{inner: h.inner.WithGroup(name), r: h.r}
}
