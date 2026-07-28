package graphql

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

func TestOperationLoggingWithRedaction(t *testing.T) {
	var buf bytes.Buffer
	gl := New(newTestLogger(&buf), WithRedactedVariables("password"))

	ctx, finish := gl.OperationStart(context.Background(), Operation{
		Name:  "Login",
		Type:  "mutation",
		Query: "mutation Login($email: String!, $password: String!) { login(email: $email, password: $password) { token } }",
		Variables: map[string]any{
			"email":    "joey@example.com",
			"password": "hunter2",
		},
	})
	if logger.RequestID(ctx) == "" {
		t.Fatal("operation should ensure a request id")
	}
	finish()

	if strings.Contains(buf.String(), "hunter2") {
		t.Fatal("redacted variable value leaked into logs")
	}

	lines := logLines(t, &buf)
	var started map[string]any
	for _, m := range lines {
		if m["message"] == "graphql operation started" {
			started = m
		}
	}
	if started == nil {
		t.Fatal("missing operation started line")
	}
	vars, _ := started["graphql.variables"].(map[string]any)
	if vars["password"] != "[REDACTED]" {
		t.Errorf("password = %v, want [REDACTED]", vars["password"])
	}
	if vars["email"] != "joey@example.com" {
		t.Errorf("email = %v, should not be redacted", vars["email"])
	}

	last := lines[len(lines)-1]
	if last["message"] != "request completed" {
		t.Errorf("last message = %v", last["message"])
	}
	if last["graphql.errorCount"] != float64(0) {
		t.Errorf("errorCount = %v, want 0", last["graphql.errorCount"])
	}
}

func TestNestedVariableRedaction(t *testing.T) {
	var buf bytes.Buffer
	gl := New(newTestLogger(&buf), WithRedactedVariables("password", "creditCard"))

	_, finish := gl.OperationStart(context.Background(), Operation{
		Name: "Signup",
		Type: "mutation",
		Variables: map[string]any{
			"input": map[string]any{
				"email":    "joey@example.com",
				"password": "hunter2",
				"billing":  map[string]any{"creditCard": "4111-1111-1111-1111"},
				"contacts": []any{
					map[string]any{"name": "alt", "password": "hunter3"},
				},
			},
		},
	})
	finish()

	out := buf.String()
	for _, secret := range []string{"hunter2", "hunter3", "4111-1111-1111-1111"} {
		if strings.Contains(out, secret) {
			t.Errorf("nested secret %q leaked into logs", secret)
		}
	}
	if !strings.Contains(out, "joey@example.com") {
		t.Error("non-sensitive nested value should still be logged")
	}
}

func TestOperationErrors(t *testing.T) {
	var buf bytes.Buffer
	gl := New(newTestLogger(&buf))

	_, finish := gl.OperationStart(context.Background(), Operation{Name: "GetOrders", Type: "query"})
	finish(errors.New("resolver blew up"), nil)

	lines := logLines(t, &buf)
	var errLine map[string]any
	for _, m := range lines {
		if m["message"] == "graphql operation error" {
			errLine = m
		}
	}
	if errLine == nil {
		t.Fatal("missing operation error line")
	}
	if errLine["error"] != "resolver blew up" {
		t.Errorf("error = %v", errLine["error"])
	}
	last := lines[len(lines)-1]
	if last["graphql.errorCount"] != float64(1) {
		t.Errorf("errorCount = %v, want 1 (nil errors must not count)", last["graphql.errorCount"])
	}
}

func TestQueryTruncation(t *testing.T) {
	var buf bytes.Buffer
	gl := New(newTestLogger(&buf), WithMaxQueryLength(10))

	_, finish := gl.OperationStart(context.Background(), Operation{
		Name:  "Big",
		Query: strings.Repeat("x", 50),
	})
	finish()

	if !strings.Contains(buf.String(), "xxxxxxxxxx...(truncated)") {
		t.Error("query was not truncated")
	}
	if strings.Contains(buf.String(), strings.Repeat("x", 11)) {
		t.Error("more than max query length was logged")
	}
}
