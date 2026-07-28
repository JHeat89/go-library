package logger

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

func newRedactingLogger(t *testing.T, keys ...string) (*Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l := New(Config{
		Service:    "test-svc",
		Platform:   PlatformLocal,
		Format:     FormatJSON,
		Level:      "debug",
		Output:     &buf,
		RedactKeys: keys,
	})
	return l, &buf
}

func TestRedactTopLevelAttr(t *testing.T) {
	l, buf := newRedactingLogger(t, "password")
	l.Info(context.Background(), "login", "password", "hunter2", "user", "joey")

	m := lastLine(t, buf)
	if m["password"] != Redacted {
		t.Errorf("password = %v, want %s", m["password"], Redacted)
	}
	if m["user"] != "joey" {
		t.Errorf("user = %v, should be untouched", m["user"])
	}
}

func TestRedactNestedMapsAndSlices(t *testing.T) {
	l, buf := newRedactingLogger(t, "apiKey", "password")
	l.Info(context.Background(), "payload",
		"body", map[string]any{
			"user": map[string]any{"name": "joey", "password": "hunter2"},
			"connections": []any{
				map[string]any{"host": "db1", "apiKey": "sk-live-123"},
			},
		},
	)

	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "sk-live-123") {
		t.Fatalf("nested secret leaked: %s", out)
	}
	m := lastLine(t, buf)
	body := m["body"].(map[string]any)
	user := body["user"].(map[string]any)
	if user["password"] != Redacted {
		t.Errorf("nested password = %v", user["password"])
	}
	if user["name"] != "joey" {
		t.Errorf("nested name = %v, should be untouched", user["name"])
	}
	conn := body["connections"].([]any)[0].(map[string]any)
	if conn["apiKey"] != Redacted {
		t.Errorf("apiKey in slice = %v", conn["apiKey"])
	}
}

func TestRedactCaseInsensitiveAndGroups(t *testing.T) {
	l, buf := newRedactingLogger(t, "password")
	l.Info(context.Background(), "grouped",
		slog.Group("auth", slog.String("Password", "hunter2"), slog.String("method", "basic")),
	)

	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("secret in group leaked: %s", buf.String())
	}
	m := lastLine(t, buf)
	auth := m["auth"].(map[string]any)
	if auth["Password"] != Redacted {
		t.Errorf("group Password = %v", auth["Password"])
	}
	if auth["method"] != "basic" {
		t.Errorf("group method = %v", auth["method"])
	}
}

func TestRedactStringMapAndPrebornAttrs(t *testing.T) {
	l, buf := newRedactingLogger(t, "token")
	// With() pre-binds attrs at the handler level; they must be scrubbed too.
	child := l.With("token", "abc123")
	child.Info(context.Background(), "headers",
		"http.headers", map[string]string{"Authorization": "ok", "token": "def456"},
	)

	out := buf.String()
	if strings.Contains(out, "abc123") || strings.Contains(out, "def456") {
		t.Fatalf("secret leaked: %s", out)
	}
}

func TestPIIScrubbedFromMessageAndStrings(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{
		Service:   "test-svc",
		Platform:  PlatformLocal,
		Format:    FormatJSON,
		Output:    &buf,
		RedactPII: true,
	})

	l.Info(context.Background(), "notify joey@example.com about ssn 123-45-6789",
		"detail", "card 4111 1111 1111 1111 on file",
		"nested", map[string]any{"contact": "call 555-123-4567 tomorrow"},
	)

	out := buf.String()
	for _, secret := range []string{"joey@example.com", "123-45-6789", "4111 1111 1111 1111", "555-123-4567"} {
		if strings.Contains(out, secret) {
			t.Errorf("PII %q leaked: %s", secret, out)
		}
	}

	m := lastLine(t, &buf)
	if m["message"] != "notify [REDACTED] about ssn [REDACTED]" {
		t.Errorf("message = %v, surrounding text should be preserved", m["message"])
	}
	if m["detail"] != "card [REDACTED] on file" {
		t.Errorf("detail = %v", m["detail"])
	}
	nested := m["nested"].(map[string]any)
	if nested["contact"] != "call [REDACTED] tomorrow" {
		t.Errorf("nested contact = %v", nested["contact"])
	}
}

func TestCustomPatternKeepsContextWords(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{
		Service:  "test-svc",
		Platform: PlatformLocal,
		Format:   FormatJSON,
		Output:   &buf,
		RedactPatterns: []Pattern{
			{Regexp: regexp.MustCompile(`(?i)(customer )(\S+)`), Replacement: "${1}[REDACTED]"},
		},
	})

	l.Info(context.Background(), "Starting Execution for Customer JoeyCox")

	m := lastLine(t, &buf)
	if m["message"] != "Starting Execution for Customer [REDACTED]" {
		t.Errorf("message = %v", m["message"])
	}
}

func TestNilRedactorPassthrough(t *testing.T) {
	var r *Redactor
	m := map[string]any{"password": "hunter2"}
	if got := r.Map(m); got["password"] != "hunter2" {
		t.Error("nil redactor should pass values through")
	}
}
