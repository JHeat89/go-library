package logger

import (
	"io"
	"log/slog"
	"strings"
)

// Format controls the output encoding of the logger.
type Format string

const (
	// FormatJSON emits one JSON object per line. This is the default on all
	// deployed platforms and is what log aggregators (CloudWatch, Datadog,
	// Splunk, ELK) expect.
	FormatJSON Format = "json"
	// FormatText emits human-readable key=value output, the default when
	// running locally.
	FormatText Format = "text"
)

// Config drives Init/New. Only Service is required; everything else has a
// sensible default so a new application can be wired up in one call.
type Config struct {
	// Service is the application name, e.g. "orders-graph". Required.
	Service string
	// Version is the application version or build tag, e.g. "1.4.2" or a
	// short commit SHA.
	Version string
	// Environment is the deployment environment, e.g. "dev", "staging", "prod".
	Environment string
	// Platform is the infrastructure the app runs on. Leave empty to
	// auto-detect (Lambda, ECS, Kubernetes, container, local).
	Platform Platform
	// Level is the minimum level to emit: "debug", "info", "warn", "error".
	// Defaults to "info". Can be changed at runtime with Logger.SetLevel.
	Level string
	// Format selects JSON or text output. Defaults to text on local,
	// JSON everywhere else.
	Format Format
	// Output is where logs are written. Defaults to os.Stdout.
	Output io.Writer
	// Team identifies the owning team, useful for routing alerts. Optional.
	Team string
	// StaticFields are extra key/value pairs attached to every log line,
	// e.g. {"costCenter": "1234"}. Optional.
	StaticFields map[string]any
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "", "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
