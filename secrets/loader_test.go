package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// fakeSource is a settable Source for unit tests.
type fakeSource struct {
	secretVal string
	secretErr error

	paramVal string
	paramErr error

	byPath    map[string]string
	byPathErr error
}

func (f *fakeSource) Secret(ctx context.Context, name string) (string, error) {
	return f.secretVal, f.secretErr
}

func (f *fakeSource) Parameter(ctx context.Context, name string) (string, error) {
	return f.paramVal, f.paramErr
}

func (f *fakeSource) ParametersByPath(ctx context.Context, path string) (map[string]string, error) {
	return f.byPath, f.byPathErr
}

var _ Source = (*fakeSource)(nil)

// secretValues collects every secret/parameter value used across the tests
// in this file, so a single assertion at the end can prove none of them ever
// reach the log output.
var secretValues = []string{"hunter2", "sw0rdf1sh", "correct-horse-battery-staple"}

func findLine(lines []map[string]any, message string) map[string]any {
	for _, l := range lines {
		if l["message"] == message {
			return l
		}
	}
	return nil
}

func TestLoaderSecretSuccess(t *testing.T) {
	var buf bytes.Buffer
	loader := New(newTestLogger(&buf), &fakeSource{secretVal: "hunter2"})

	val, err := loader.Secret(context.Background(), "orders/db-password")
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if val != "hunter2" {
		t.Errorf("Secret() = %q, want hunter2", val)
	}

	lines := logLines(t, &buf)
	line := findLine(lines, "secret loaded")
	if line == nil {
		t.Fatal(`missing "secret loaded" log line`)
	}
	if line["level"] != "debug" {
		t.Errorf("level = %v, want debug", line["level"])
	}
	if line["secrets.name"] != "orders/db-password" {
		t.Errorf("secrets.name = %v", line["secrets.name"])
	}
	if line["secrets.source"] != sourceSecretsManager {
		t.Errorf("secrets.source = %v, want %s", line["secrets.source"], sourceSecretsManager)
	}
	if _, ok := line["secrets.durationMs"].(float64); !ok {
		t.Errorf("secrets.durationMs missing or not a number: %v", line["secrets.durationMs"])
	}
}

func TestLoaderSecretJSONSuccess(t *testing.T) {
	var buf bytes.Buffer
	loader := New(newTestLogger(&buf), &fakeSource{secretVal: `{"username":"svc","password":"sw0rdf1sh"}`})

	m, err := loader.SecretJSON(context.Background(), "orders/db")
	if err != nil {
		t.Fatalf("SecretJSON() error = %v", err)
	}
	if m["username"] != "svc" || m["password"] != "sw0rdf1sh" {
		t.Errorf("SecretJSON() = %v", m)
	}

	lines := logLines(t, &buf)
	if findLine(lines, "secret loaded") == nil {
		t.Error(`missing "secret loaded" log line`)
	}
}

func TestLoaderSecretJSONParseFailure(t *testing.T) {
	var buf bytes.Buffer
	loader := New(newTestLogger(&buf), &fakeSource{secretVal: "correct-horse-battery-staple"})

	_, err := loader.SecretJSON(context.Background(), "orders/db")
	if err == nil {
		t.Fatal("SecretJSON() error = nil, want a parse error")
	}

	lines := logLines(t, &buf)
	line := findLine(lines, "secret JSON parse failed")
	if line == nil {
		t.Fatal(`missing "secret JSON parse failed" log line`)
	}
	if line["level"] != "error" {
		t.Errorf("level = %v, want error", line["level"])
	}
	if line["secrets.name"] != "orders/db" {
		t.Errorf("secrets.name = %v", line["secrets.name"])
	}
	if line["error"] == nil {
		t.Error("missing error field")
	}
}

func TestLoaderParameterSuccess(t *testing.T) {
	var buf bytes.Buffer
	loader := New(newTestLogger(&buf), &fakeSource{paramVal: "hunter2"})

	val, err := loader.Parameter(context.Background(), "/orders/db-host")
	if err != nil {
		t.Fatalf("Parameter() error = %v", err)
	}
	if val != "hunter2" {
		t.Errorf("Parameter() = %q, want hunter2", val)
	}

	lines := logLines(t, &buf)
	line := findLine(lines, "parameter loaded")
	if line == nil {
		t.Fatal(`missing "parameter loaded" log line`)
	}
	if line["secrets.name"] != "/orders/db-host" {
		t.Errorf("secrets.name = %v", line["secrets.name"])
	}
	if line["secrets.source"] != sourceParameterStore {
		t.Errorf("secrets.source = %v, want %s", line["secrets.source"], sourceParameterStore)
	}
}

func TestLoaderParametersByPathSuccess(t *testing.T) {
	var buf bytes.Buffer
	byPath := map[string]string{
		"/orders/db-host": "db.internal",
		"/orders/db-port": "5432",
	}
	loader := New(newTestLogger(&buf), &fakeSource{byPath: byPath})

	got, err := loader.ParametersByPath(context.Background(), "/orders")
	if err != nil {
		t.Fatalf("ParametersByPath() error = %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ParametersByPath() returned %d entries, want 2", len(got))
	}

	lines := logLines(t, &buf)
	line := findLine(lines, "parameters loaded")
	if line == nil {
		t.Fatal(`missing "parameters loaded" log line`)
	}
	if line["secrets.path"] != "/orders" {
		t.Errorf("secrets.path = %v", line["secrets.path"])
	}
	if line["secrets.count"] != float64(2) {
		t.Errorf("secrets.count = %v, want 2", line["secrets.count"])
	}
	if line["secrets.source"] != sourceParameterStore {
		t.Errorf("secrets.source = %v, want %s", line["secrets.source"], sourceParameterStore)
	}
}

func TestLoaderSecretNotFound(t *testing.T) {
	var buf bytes.Buffer
	loader := New(newTestLogger(&buf), &fakeSource{secretErr: ErrNotFound})

	_, err := loader.Secret(context.Background(), "orders/missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Secret() error = %v, want ErrNotFound", err)
	}

	lines := logLines(t, &buf)
	line := findLine(lines, "secret not found")
	if line == nil {
		t.Fatal(`missing "secret not found" log line`)
	}
	if line["level"] != "error" {
		t.Errorf("level = %v, want error", line["level"])
	}
}

func TestLoaderParameterNotFound(t *testing.T) {
	var buf bytes.Buffer
	loader := New(newTestLogger(&buf), &fakeSource{paramErr: ErrNotFound})

	_, err := loader.Parameter(context.Background(), "/orders/missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Parameter() error = %v, want ErrNotFound", err)
	}

	lines := logLines(t, &buf)
	if findLine(lines, "parameter not found") == nil {
		t.Fatal(`missing "parameter not found" log line`)
	}
}

func TestLoaderSecretLoadFailed(t *testing.T) {
	var buf bytes.Buffer
	boom := errors.New("connection reset")
	loader := New(newTestLogger(&buf), &fakeSource{secretErr: boom})

	_, err := loader.Secret(context.Background(), "orders/db-password")
	if !errors.Is(err, boom) {
		t.Fatalf("Secret() error = %v, want %v", err, boom)
	}

	lines := logLines(t, &buf)
	line := findLine(lines, "secret load failed")
	if line == nil {
		t.Fatal(`missing "secret load failed" log line`)
	}
	if line["error"] != "connection reset" {
		t.Errorf("error field = %v", line["error"])
	}
}

func TestLoaderParametersByPathLoadFailed(t *testing.T) {
	var buf bytes.Buffer
	boom := errors.New("throttled")
	loader := New(newTestLogger(&buf), &fakeSource{byPathErr: boom})

	_, err := loader.ParametersByPath(context.Background(), "/orders")
	if !errors.Is(err, boom) {
		t.Fatalf("ParametersByPath() error = %v, want %v", err, boom)
	}

	lines := logLines(t, &buf)
	if findLine(lines, "parameter load failed") == nil {
		t.Fatal(`missing "parameter load failed" log line`)
	}
}

// TestLoaderNeverLogsValues is the secrecy assertion: across every case
// above, none of the actual secret/parameter values appear anywhere in the
// combined log output — only names, paths, source, and duration.
func TestLoaderNeverLogsValues(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	New(log, &fakeSource{secretVal: "hunter2"}).Secret(context.Background(), "s1")
	New(log, &fakeSource{secretVal: `{"password":"sw0rdf1sh"}`}).SecretJSON(context.Background(), "s2")
	New(log, &fakeSource{paramVal: "correct-horse-battery-staple"}).Parameter(context.Background(), "p1")
	New(log, &fakeSource{byPath: map[string]string{"/a": "hunter2", "/b": "sw0rdf1sh"}}).ParametersByPath(context.Background(), "/")

	out := buf.String()
	for _, v := range secretValues {
		if strings.Contains(out, v) {
			t.Errorf("log output contains secret value %q:\n%s", v, out)
		}
	}
}
