package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JHeat89/go-library/logger"
)

func newTestLogger(buf *bytes.Buffer) *logger.Logger {
	return logger.New(logger.Config{
		Service:  "test-svc",
		Platform: logger.PlatformLocal,
		Format:   logger.FormatJSON,
		Level:    "debug",
		Output:   buf,
	})
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestMiddlewareGeneratesAndEchoesRequestID(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	var seenID string
	h := Middleware(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = logger.RequestID(r.Context())
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/orders", nil))

	if seenID == "" {
		t.Fatal("handler context missing request id")
	}
	if got := rec.Header().Get(RequestIDHeader); got != seenID {
		t.Errorf("response header id %q != context id %q", got, seenID)
	}

	lines := logLines(t, &buf)
	last := lines[len(lines)-1]
	if last["message"] != "request completed" {
		t.Errorf("message = %v", last["message"])
	}
	if last["requestId"] != seenID {
		t.Errorf("logged requestId = %v, want %s", last["requestId"], seenID)
	}
	if last["http.status"] != float64(http.StatusCreated) {
		t.Errorf("http.status = %v, want 201", last["http.status"])
	}
}

func TestMiddlewarePropagatesInboundRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := Middleware(newTestLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, "upstream-id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(RequestIDHeader); got != "upstream-id" {
		t.Errorf("echoed id = %q, want upstream-id", got)
	}
}

func TestMiddlewareRecoversPanics(t *testing.T) {
	var buf bytes.Buffer
	h := Middleware(newTestLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/explode", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	var foundPanic bool
	for _, m := range logLines(t, &buf) {
		if m["message"] == "panic recovered" && m["panic"] == "boom" {
			foundPanic = true
		}
	}
	if !foundPanic {
		t.Error("panic was not logged")
	}
}

func TestMiddlewareSkipPaths(t *testing.T) {
	var buf bytes.Buffer
	h := Middleware(newTestLogger(&buf), WithSkipPaths("/health"))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if buf.Len() != 0 {
		t.Errorf("skipped path should not log, got: %s", buf.String())
	}
}
