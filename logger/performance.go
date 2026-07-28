package logger

import (
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	processStart = time.Now()
	// coldStart is true until the first request lifecycle completes. On
	// Lambda this distinguishes cold invocations; on long-lived platforms it
	// simply marks the first request after boot.
	coldStart atomic.Bool
)

func init() { coldStart.Store(true) }

// PerformanceSnapshot is a point-in-time diagnostic view of the process,
// similar to what AWS Lambda Powertools reports. It is attached automatically
// to lifecycle completion logs and can be captured on demand with
// CapturePerformance.
type PerformanceSnapshot struct {
	HeapAllocMB    float64 `json:"heapAllocMB"`
	HeapSysMB      float64 `json:"heapSysMB"`
	TotalAllocMB   float64 `json:"totalAllocMB"`
	NumGC          uint32  `json:"numGC"`
	GCPauseTotalMs float64 `json:"gcPauseTotalMs"`
	Goroutines     int     `json:"goroutines"`
	NumCPU         int     `json:"numCPU"`
	UptimeSec      float64 `json:"uptimeSec"`
	ColdStart      bool    `json:"coldStart"`
}

// CapturePerformance reads current runtime statistics.
func CapturePerformance() PerformanceSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return PerformanceSnapshot{
		HeapAllocMB:    mb(m.HeapAlloc),
		HeapSysMB:      mb(m.HeapSys),
		TotalAllocMB:   mb(m.TotalAlloc),
		NumGC:          m.NumGC,
		GCPauseTotalMs: float64(m.PauseTotalNs) / 1e6,
		Goroutines:     runtime.NumGoroutine(),
		NumCPU:         runtime.NumCPU(),
		UptimeSec:      time.Since(processStart).Seconds(),
		ColdStart:      coldStart.Load(),
	}
}

// LogValue lets a snapshot be logged as a structured group.
func (p PerformanceSnapshot) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Float64("heapAllocMB", p.HeapAllocMB),
		slog.Float64("heapSysMB", p.HeapSysMB),
		slog.Float64("totalAllocMB", p.TotalAllocMB),
		slog.Uint64("numGC", uint64(p.NumGC)),
		slog.Float64("gcPauseTotalMs", p.GCPauseTotalMs),
		slog.Int("goroutines", p.Goroutines),
		slog.Int("numCPU", p.NumCPU),
		slog.Float64("uptimeSec", p.UptimeSec),
		slog.Bool("coldStart", p.ColdStart),
	)
}

func mb(b uint64) float64 {
	return float64(b) / (1024 * 1024)
}
