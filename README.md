# go-library

> **Note for AI assistants:** read [CLAUDE.md](CLAUDE.md) first — it contains
> the condensed architecture, conventions, and commands for this repo. Prefer
> it over re-deriving context from the source tree to keep token usage low.

Reusable Go utilities for enterprise services. The centerpiece is a universal
structured logger built on the standard library's `log/slog` with:

- **One-call init** — config-driven setup with sane defaults
- **Request lifecycle tracing** — a UUID request ID is generated the moment a
  request enters the app and persists (via `context.Context`) for its entire
  life, across REST, GraphQL, ETL, and Kafka boundaries
- **Performance diagnostics** — Powertools-style runtime snapshot (heap, GC,
  goroutines, cold start, uptime) attached to every completed request
- **Platform aware** — auto-detects Lambda, ECS, Kubernetes, container, or
  local and enriches logs accordingly (region, function name, pod, ...)
- **Zero dependencies** for the core; only `graphql/gqlgen` pulls in gqlgen

## Packages

| Package | Purpose |
|---|---|
| `logger` | Base logger: config, levels, request IDs, lifecycle, performance |
| `requestid` | Dependency-free UUIDv4 generator |
| `rest` | `net/http` middleware (works with chi, gorilla, plain mux) |
| `graphql` | Server-agnostic GraphQL operation logger with variable redaction |
| `graphql/gqlgen` | Drop-in gqlgen handler extension |
| `etl` | Batch/pipeline job logging with checkpoints and throughput |
| `events` | Client-agnostic Kafka consume/produce logging with header-based request ID propagation |

## Quickstart

```go
log := logger.Init(logger.Config{
    Service:     "orders-graph",   // required
    Version:     "1.4.2",          // build tag or commit SHA
    Environment: "prod",
    Team:        "commerce",
    // Platform, Format, Level, Output all default sensibly:
    // platform auto-detected, JSON on deployed platforms / text locally, info level, stdout.
})

log.Info(ctx, "server listening", "port", 8080)
log.Error(ctx, "save failed", logger.Err(err))
```

Every line automatically carries `service`, `version`, `environment`,
`platform`, `hostname`, `pid`, `goVersion`, region/function/pod when
applicable, and — once a request ID is in the context — `requestId`.

### REST

```go
mux := http.NewServeMux()
mux.HandleFunc("/orders", ordersHandler)

handler := rest.Middleware(log, rest.WithSkipPaths("/health"))(mux)
http.ListenAndServe(":8080", handler)
```

The middleware accepts an inbound `X-Request-ID` (or generates one), echoes it
in the response, logs completion with method/path/status/bytes/duration plus a
performance snapshot, and recovers panics with a stack trace.

### GraphQL (gqlgen)

```go
import (
    graphqllog "github.com/JHeat89/go-library/graphql"
    gqlgenlog "github.com/JHeat89/go-library/graphql/gqlgen"
)

gl := graphqllog.New(log,
    graphqllog.WithRedactedVariables("password", "token"),
    graphqllog.WithMaxQueryLength(2000),
)

srv := handler.NewDefaultServer(generated.NewExecutableSchema(cfg))
srv.Use(gqlgenlog.New(gl))
```

Using a different GraphQL server? Call `gl.OperationStart(ctx, op)` yourself —
the core has no gqlgen dependency.

### ETL

```go
ctx, job := etl.StartJob(ctx, log, "orders-nightly-export")
for batch := range batches {
    n, failed := process(batch)
    job.Processed(n)
    job.Failed(failed)
    job.Checkpoint(ctx, "batch flushed")
}
job.Complete(ctx) // or job.Fail(ctx, err)
```

Checkpoints log running totals, elapsed time, and records/sec so stalled runs
are visible mid-flight. Each run gets its own request ID.

### Kafka events (any client)

```go
ev := events.New(log)

// Consuming — request ID is picked up from the x-request-id message header,
// preserving correlation with the producing service:
ctx, done := ev.ConsumeStart(ctx, events.Message{
    Topic:         m.Topic,
    Partition:     m.Partition,
    Offset:        m.Offset,
    Key:           string(m.Key),
    ConsumerGroup: "orders-consumer",
    Headers:       headerMap(m.Headers),
    Timestamp:     m.Time, // enables consumer-lag logging
})
err := handle(ctx, m)
done(err)

// Producing — stamp outgoing messages so downstream consumers correlate:
for k, v := range events.OutgoingHeaders(ctx) {
    msg.Headers = append(msg.Headers, kafka.Header{Key: k, Value: []byte(v)})
}
ev.Produced(ctx, events.Message{Topic: msg.Topic, Key: string(msg.Key)}, writeErr)
```

## Request lifecycle tracing

Any layer can add stages to the current request without plumbing extra
arguments — the trace lives in the context:

```go
ctx, lc := log.StartRequest(ctx, "GET /orders") // done for you by rest/graphql/etl/events
defer lc.End(ctx)

logger.LifecycleFrom(ctx).Stage(ctx, "db.query")
logger.LifecycleFrom(ctx).Stage(ctx, "downstream.inventory")
```

The completion line includes total `durationMs`, every stage with per-stage
timing, and a `performance` group:

```json
{
  "timestamp": "2026-07-24T20:15:01Z",
  "level": "info",
  "message": "request completed",
  "service": "orders-graph",
  "requestId": "9f8b7c6d-1a2b-4c3d-8e9f-0a1b2c3d4e5f",
  "request": "GET /orders",
  "durationMs": 42.7,
  "stages": [{"name": "db.query", "elapsedMs": 12.1, "sinceMs": 12.1}],
  "performance": {
    "heapAllocMB": 12.4, "heapSysMB": 24.0, "numGC": 3,
    "gcPauseTotalMs": 0.8, "goroutines": 14, "numCPU": 8,
    "uptimeSec": 1042.5, "coldStart": false
  }
}
```

## Runtime level changes

```go
log.SetLevel("debug") // flip a live instance to debug while triaging
```

## Requirements

Go 1.26+.
