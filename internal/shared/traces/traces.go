// Package traces wraps boundary work in an OpenTelemetry span and carries
// context across the NATS hop.
//
// Instrumentation is confined to process boundaries — a decoded client frame
// and a bus request. A span per game-loop step or per entity mutation would cost
// more than the work it measures, so the 50 Hz tick and the AOI broadcast
// fan-out stay uninstrumented by design.
package traces

import (
	"context"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// scope is the instrumentation scope name every span in this codebase carries.
const scope = "github.com/bouroo/goAthena"

// Tracer returns the shared tracer.
func Tracer() trace.Tracer { return otel.Tracer(scope) }

// Span is one in-flight span. Its methods tolerate a nil receiver so a caller
// that skips instrumentation needs no branch.
type Span struct{ span trace.Span }

// Start opens a span and returns the context derived from it. That context is
// what the caller must pass to the work being measured, so anything the work
// opens nests underneath.
func Start(ctx context.Context, name string, kind trace.SpanKind, attrs ...attribute.KeyValue) (context.Context, *Span) {
	ctx, span := Tracer().Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
	return ctx, &Span{span: span}
}

// Set records attributes only known once the work is under way — an outcome, a
// resolved identity.
func (s *Span) Set(attrs ...attribute.KeyValue) {
	if s == nil {
		return
	}
	s.span.SetAttributes(attrs...)
}

// End closes the span. A non-nil err is recorded on it and marks it failed.
func (s *Span) End(err error) {
	if s == nil {
		return
	}
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
	}
	s.span.End()
}

// Frame opens a span for one decoded inbound client frame and returns the
// finish func the dispatch goroutine defers. name is the rAthena packet name
// when the packet DB knows the opcode (empty is fine — the opcode is always
// recorded).
//
// The span is a root: a frame arrives on a bare TCP connection with no carrier
// to parent it to. Slow-handler alerting keys off its duration.
func Frame(ctx context.Context, name string, opcode uint16) (context.Context, *Span) {
	spanName := fmt.Sprintf("frame 0x%04x", opcode)
	if name != "" {
		spanName = "frame " + name
	}
	return Start(ctx, spanName, trace.SpanKindServer,
		attribute.String("packet.opcode", fmt.Sprintf("0x%04x", opcode)),
		attribute.String("packet.name", name),
	)
}

// carrier adapts NATS message headers to the OTel propagation API.
//
// It exists because NATS lowercases header keys on the wire while
// propagation.HeaderCarrier is backed by http.Header, which canonicalizes on
// every Get (traceparent → Traceparent) and therefore misses. Going through a
// case-insensitive carrier is the difference between a linked trace and two
// unrelated roots.
type carrier nats.Header

func (c carrier) Get(key string) string {
	for k, v := range c {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func (c carrier) Set(key, value string) { nats.Header(c).Set(key, value) }

func (c carrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectPublish opens a producer span for one outbound bus request and injects
// its context into NATS headers, so the serving process continues this trace
// instead of starting an unrelated one.
func InjectPublish(ctx context.Context, subject string) (context.Context, nats.Header, *Span) {
	ctx, span := Start(ctx, "bus "+subject, trace.SpanKindProducer,
		attribute.String("messaging.subject", subject))
	header := nats.Header{}
	otel.GetTextMapPropagator().Inject(ctx, carrier(header))
	return ctx, header, span
}

// ExtractSubscribe opens a consumer span for a bus request this process serves,
// continuing the producer's trace from the request headers.
func ExtractSubscribe(ctx context.Context, subject string, header nats.Header) (context.Context, *Span) {
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier(header))
	return Start(ctx, "bus "+subject, trace.SpanKindConsumer,
		attribute.String("messaging.subject", subject))
}
