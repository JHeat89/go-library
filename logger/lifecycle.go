package logger

import (
	"context"
	"sync"
	"time"
)

// Lifecycle traces a single request from entry to completion. It is created
// at the application edge (usually by the rest, graphql, or events packages)
// and travels in the context so any layer can record a stage.
//
//	ctx, lc := log.StartRequest(ctx, "GET /orders")
//	defer lc.End()
//	...
//	lc.Stage("db.query")
type Lifecycle struct {
	name  string
	start time.Time
	log   *Logger

	mu        sync.Mutex
	lastStage time.Time
	stages    []stageRecord
	ended     bool
}

type stageRecord struct {
	Name      string  `json:"name"`
	ElapsedMs float64 `json:"elapsedMs"`
	SinceMs   float64 `json:"sinceMs"`
}

// StartRequest begins a lifecycle trace. It guarantees a request ID exists in
// the returned context, stores the Lifecycle in the context, and logs the
// request start.
func (l *Logger) StartRequest(ctx context.Context, name string, attrs ...any) (context.Context, *Lifecycle) {
	ctx, _ = EnsureRequestID(ctx)
	now := time.Now()
	lc := &Lifecycle{name: name, start: now, lastStage: now, log: l}
	ctx = context.WithValue(ctx, lifecycleKey, lc)
	l.Debug(ctx, "request started", append([]any{"request", name}, attrs...)...)
	return ctx, lc
}

// LifecycleFrom returns the Lifecycle stored in ctx, or nil. Useful for
// recording stages deep in the call stack without threading the value.
func LifecycleFrom(ctx context.Context) *Lifecycle {
	lc, _ := ctx.Value(lifecycleKey).(*Lifecycle)
	return lc
}

// Stage records a named checkpoint, logging time elapsed since the request
// started and since the previous stage. Call it after each significant unit
// of work (auth, db query, downstream call) to make slow requests diagnosable.
func (lc *Lifecycle) Stage(ctx context.Context, name string, attrs ...any) {
	if lc == nil {
		return
	}
	now := time.Now()
	lc.mu.Lock()
	rec := stageRecord{
		Name:      name,
		ElapsedMs: durMs(lc.start, now),
		SinceMs:   durMs(lc.lastStage, now),
	}
	lc.stages = append(lc.stages, rec)
	lc.lastStage = now
	lc.mu.Unlock()

	lc.log.Debug(ctx, "stage completed", append([]any{
		"request", lc.name,
		"stage", name,
		"elapsedMs", rec.ElapsedMs,
		"stageMs", rec.SinceMs,
	}, attrs...)...)
}

// End completes the trace: it logs total duration, every recorded stage, and
// a performance snapshot. Safe to call once; subsequent calls are no-ops.
// Pass extra attrs (e.g. "http.status", 200) to enrich the completion line.
func (lc *Lifecycle) End(ctx context.Context, attrs ...any) {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	if lc.ended {
		lc.mu.Unlock()
		return
	}
	lc.ended = true
	stages := lc.stages
	lc.mu.Unlock()

	perf := CapturePerformance()
	coldStart.Store(false)

	all := []any{
		"request", lc.name,
		"durationMs", durMs(lc.start, time.Now()),
		"performance", perf,
	}
	if len(stages) > 0 {
		all = append(all, "stages", stages)
	}
	lc.log.Info(ctx, "request completed", append(all, attrs...)...)
}

func durMs(from, to time.Time) float64 {
	return float64(to.Sub(from).Microseconds()) / 1000
}
