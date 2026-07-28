// Package rest provides net/http middleware that wires the base logger's
// request lifecycle into any HTTP server (chi, gorilla, gin via wrapper, or
// plain net/http).
//
//	handler := rest.Middleware(log)(mux)
package rest

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/JHeat89/go-library/logger"
)

// RequestIDHeader is the header used to accept an inbound request ID from an
// upstream service and to echo the ID back to the caller.
const RequestIDHeader = "X-Request-ID"

type options struct {
	skipPaths map[string]bool
}

// Option customizes the middleware.
type Option func(*options)

// WithSkipPaths disables logging for the given exact paths, typically health
// checks and readiness probes that would otherwise flood the logs.
func WithSkipPaths(paths ...string) Option {
	return func(o *options) {
		for _, p := range paths {
			o.skipPaths[p] = true
		}
	}
}

// Middleware returns standard net/http middleware that, for every request:
//
//   - reuses the inbound X-Request-ID or generates a new UUID
//   - stores the ID in the request context for the life of the request
//   - echoes the ID in the response so callers can correlate
//   - starts a lifecycle trace and logs completion with method, path, status,
//     response size, duration, and a performance snapshot
//   - recovers panics, logging them with a stack trace and returning 500
func Middleware(log *logger.Logger, opts ...Option) func(http.Handler) http.Handler {
	o := &options{skipPaths: map[string]bool{}}
	for _, opt := range opts {
		opt(o)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o.skipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			if id := r.Header.Get(RequestIDHeader); id != "" {
				ctx = logger.WithRequestID(ctx, id)
			}
			ctx, id := logger.EnsureRequestID(ctx)
			w.Header().Set(RequestIDHeader, id)

			ctx, lc := log.StartRequest(ctx, r.Method+" "+r.URL.Path,
				"http.method", r.Method,
				"http.path", r.URL.Path,
			)
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			defer func() {
				if rec := recover(); rec != nil {
					log.Error(ctx, "panic recovered",
						"panic", rec,
						"stack", string(debug.Stack()),
						"http.method", r.Method,
						"http.path", r.URL.Path,
					)
					if !rw.wroteHeader {
						http.Error(rw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}
				lc.End(ctx,
					"http.method", r.Method,
					"http.path", r.URL.Path,
					"http.query", r.URL.RawQuery,
					"http.status", rw.status,
					"http.responseBytes", rw.bytes,
					"http.durationMs", float64(time.Since(start).Microseconds())/1000,
					"http.userAgent", r.UserAgent(),
					"http.remoteAddr", r.RemoteAddr,
				)
			}()

			next.ServeHTTP(rw, r.WithContext(ctx))
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap supports http.ResponseController (flush, hijack, etc.).
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
