// Package etl logs the lifecycle of batch and pipeline jobs: start, progress
// checkpoints with throughput, and completion or failure with totals.
//
//	ctx, job := etl.StartJob(ctx, log, "orders-nightly-export")
//	for batch := range batches {
//		n, err := process(batch)
//		job.Processed(n)
//		job.Checkpoint(ctx, "batch flushed")
//	}
//	job.Complete(ctx)
package etl

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/JHeat89/go-library/logger"
)

// Job tracks a single ETL run. A run gets its own request ID so every log
// line across the whole pipeline is correlated.
type Job struct {
	name  string
	log   *logger.Logger
	lc    *logger.Lifecycle
	start time.Time

	processed atomic.Int64
	failed    atomic.Int64
}

// StartJob begins an ETL run: it assigns a run-scoped request ID, starts a
// lifecycle trace, and logs the start. Use the returned context for all
// logging within the run.
func StartJob(ctx context.Context, log *logger.Logger, name string, attrs ...any) (context.Context, *Job) {
	ctx, lc := log.StartRequest(ctx, "etl "+name, attrs...)
	j := &Job{name: name, log: log, lc: lc, start: time.Now()}
	log.Info(ctx, "etl job started", append([]any{"etl.job", name}, attrs...)...)
	return ctx, j
}

// Processed adds n successfully processed records to the running total.
func (j *Job) Processed(n int) { j.processed.Add(int64(n)) }

// Failed adds n failed records to the running total.
func (j *Job) Failed(n int) { j.failed.Add(int64(n)) }

// Checkpoint logs current progress: totals, elapsed time, and records/sec
// throughput. Call it at natural boundaries (per batch, per file, per table)
// so a stalled or slow run is visible mid-flight.
func (j *Job) Checkpoint(ctx context.Context, name string, attrs ...any) {
	elapsed := time.Since(j.start)
	processed := j.processed.Load()
	all := []any{
		"etl.job", j.name,
		"etl.checkpoint", name,
		"etl.recordsProcessed", processed,
		"etl.recordsFailed", j.failed.Load(),
		"etl.elapsedSec", elapsed.Seconds(),
		"etl.recordsPerSec", rate(processed, elapsed),
	}
	j.lc.Stage(ctx, name)
	j.log.Info(ctx, "etl checkpoint", append(all, attrs...)...)
}

// Complete finishes the run successfully, logging totals, duration,
// throughput, and a performance snapshot.
func (j *Job) Complete(ctx context.Context, attrs ...any) {
	j.lc.End(ctx, append(j.summaryAttrs("completed"), attrs...)...)
}

// Fail finishes the run as failed, logging the error alongside totals so the
// blast radius (how many records made it) is immediately visible.
func (j *Job) Fail(ctx context.Context, err error, attrs ...any) {
	j.log.Error(ctx, "etl job failed",
		append(append(j.summaryAttrs("failed"), logger.Err(err)), attrs...)...)
	j.lc.End(ctx, append(j.summaryAttrs("failed"), attrs...)...)
}

func (j *Job) summaryAttrs(status string) []any {
	elapsed := time.Since(j.start)
	processed := j.processed.Load()
	return []any{
		"etl.job", j.name,
		"etl.status", status,
		"etl.recordsProcessed", processed,
		"etl.recordsFailed", j.failed.Load(),
		"etl.recordsPerSec", rate(processed, elapsed),
	}
}

func rate(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / d.Seconds()
}
