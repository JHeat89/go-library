// Package events logs Kafka message consumption and production without
// depending on any Kafka client. It works with segmentio/kafka-go, Sarama,
// confluent-kafka-go, or anything else: translate the client's message type
// into events.Message and call ConsumeStart / Produced.
//
// Request IDs propagate across services through the message header named
// RequestIDHeader: consumers pick it up automatically, and producers should
// stamp outgoing messages with HeaderCarrier / OutgoingHeaders.
package events

import (
	"context"
	"time"

	"github.com/JHeat89/go-library/logger"
)

// RequestIDHeader is the Kafka message header used to carry the request ID
// across service boundaries.
const RequestIDHeader = "x-request-id"

// Message is client-agnostic Kafka message metadata. Fill in what your client
// provides; zero values are omitted from logs where they would be noise.
type Message struct {
	Topic         string
	Partition     int
	Offset        int64
	Key           string
	ConsumerGroup string
	Headers       map[string]string
	// Timestamp is the broker/producer timestamp of the message. When set on
	// a consumed message, consumer lag is logged.
	Timestamp time.Time
}

// Logger logs event consumption and production using the base logger.
type Logger struct {
	base *logger.Logger
}

// New builds an events logger on top of the base logger.
func New(base *logger.Logger) *Logger {
	return &Logger{base: base}
}

// ConsumeStart begins the lifecycle for one consumed message. The request ID
// is taken from the message's x-request-id header when present (preserving
// end-to-end correlation with the producing service) or generated fresh.
// Call the returned done func when processing finishes, passing any error.
func (l *Logger) ConsumeStart(ctx context.Context, msg Message) (context.Context, func(err error)) {
	if id := msg.Headers[RequestIDHeader]; id != "" {
		ctx = logger.WithRequestID(ctx, id)
	}

	attrs := msgAttrs("kafka.", msg)
	if !msg.Timestamp.IsZero() {
		attrs = append(attrs, "kafka.lagMs", float64(time.Since(msg.Timestamp).Microseconds())/1000)
	}

	ctx, lc := l.base.StartRequest(ctx, "consume "+msg.Topic, attrs...)
	l.base.Info(ctx, "event received", attrs...)

	return ctx, func(err error) {
		if err != nil {
			l.base.Error(ctx, "event processing failed", append(attrs, logger.Err(err))...)
			lc.End(ctx, append(attrs, "kafka.result", "error")...)
			return
		}
		lc.End(ctx, append(attrs, "kafka.result", "success")...)
	}
}

// Produced logs the outcome of producing a message. Call it after the client
// confirms (or fails) the write, with partition/offset filled in if known.
func (l *Logger) Produced(ctx context.Context, msg Message, err error) {
	attrs := msgAttrs("kafka.", msg)
	if err != nil {
		l.base.Error(ctx, "event produce failed", append(attrs, logger.Err(err))...)
		return
	}
	l.base.Info(ctx, "event produced", attrs...)
}

// Base exposes the underlying base logger.
func (l *Logger) Base() *logger.Logger { return l.base }

// OutgoingHeaders returns headers to attach to a produced message so the
// current request ID follows the event to downstream consumers. Merge the
// result into your client's header format.
func OutgoingHeaders(ctx context.Context) map[string]string {
	if id := logger.RequestID(ctx); id != "" {
		return map[string]string{RequestIDHeader: id}
	}
	return map[string]string{}
}

func msgAttrs(prefix string, msg Message) []any {
	attrs := []any{
		prefix + "topic", msg.Topic,
		prefix + "partition", msg.Partition,
		prefix + "offset", msg.Offset,
	}
	if msg.Key != "" {
		attrs = append(attrs, prefix+"key", msg.Key)
	}
	if msg.ConsumerGroup != "" {
		attrs = append(attrs, prefix+"consumerGroup", msg.ConsumerGroup)
	}
	return attrs
}
