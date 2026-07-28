package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestLogger(t *testing.T) (*Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l := New(Config{
		Service:     "test-svc",
		Version:     "1.2.3",
		Environment: "test",
		Platform:    PlatformLocal,
		Format:      FormatJSON,
		Level:       "debug",
		Output:      &buf,
	})
	return l, &buf
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("invalid JSON log line %q: %v", lines[len(lines)-1], err)
	}
	return m
}

func TestBaseFieldsAndRenamedKeys(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Info(context.Background(), "hello", "extra", "value")

	m := lastLine(t, buf)
	for k, want := range map[string]string{
		"service":     "test-svc",
		"version":     "1.2.3",
		"environment": "test",
		"platform":    "local",
		"message":     "hello",
		"level":       "info",
		"extra":       "value",
	} {
		if got := m[k]; got != want {
			t.Errorf("field %q = %v, want %v", k, got, want)
		}
	}
	if _, ok := m["timestamp"]; !ok {
		t.Error("missing timestamp field")
	}
	if _, ok := m["msg"]; ok {
		t.Error("built-in msg key should be renamed to message")
	}
}

func TestRequestIDFromContext(t *testing.T) {
	l, buf := newTestLogger(t)

	ctx := WithRequestID(context.Background(), "abc-123")
	l.Info(ctx, "with id")
	if m := lastLine(t, buf); m["requestId"] != "abc-123" {
		t.Errorf("requestId = %v, want abc-123", m["requestId"])
	}

	buf.Reset()
	l.Info(context.Background(), "without id")
	if m := lastLine(t, buf); m["requestId"] != nil {
		t.Errorf("requestId should be absent, got %v", m["requestId"])
	}
}

func TestEnsureRequestIDIsStable(t *testing.T) {
	ctx, id := EnsureRequestID(context.Background())
	if id == "" {
		t.Fatal("expected generated id")
	}
	ctx2, id2 := EnsureRequestID(ctx)
	if id2 != id {
		t.Errorf("EnsureRequestID regenerated: %s != %s", id2, id)
	}
	if ctx2 != ctx {
		t.Error("context should be unchanged when id already present")
	}
}

func TestLifecycle(t *testing.T) {
	l, buf := newTestLogger(t)

	ctx, lc := l.StartRequest(context.Background(), "GET /orders")
	if RequestID(ctx) == "" {
		t.Fatal("StartRequest should ensure a request id")
	}
	if LifecycleFrom(ctx) != lc {
		t.Fatal("lifecycle should be stored in context")
	}

	lc.Stage(ctx, "db.query")
	buf.Reset()
	lc.End(ctx, "http.status", 200)

	m := lastLine(t, buf)
	if m["message"] != "request completed" {
		t.Errorf("message = %v", m["message"])
	}
	if _, ok := m["durationMs"]; !ok {
		t.Error("missing durationMs")
	}
	perf, ok := m["performance"].(map[string]any)
	if !ok {
		t.Fatalf("missing performance group: %v", m["performance"])
	}
	if _, ok := perf["goroutines"]; !ok {
		t.Error("performance missing goroutines")
	}
	if _, ok := m["stages"]; !ok {
		t.Error("missing stages")
	}

	// End is idempotent.
	buf.Reset()
	lc.End(ctx)
	if buf.Len() != 0 {
		t.Error("second End should not log")
	}
}

func TestSetLevel(t *testing.T) {
	l, buf := newTestLogger(t)
	l.SetLevel("error")
	l.Info(context.Background(), "hidden")
	if buf.Len() != 0 {
		t.Error("info should be suppressed at error level")
	}
	l.Error(context.Background(), "shown")
	if buf.Len() == 0 {
		t.Error("error should be emitted")
	}
}
