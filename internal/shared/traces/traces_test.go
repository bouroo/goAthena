//go:build unit

package traces

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// setupExporter swaps in a recording tracer provider and restores the globals.
func setupExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return exp
}

// TestInjectExtractAcrossLowercasedHeaders is the regression test for the bug
// this carrier exists to fix: NATS restores header keys lowercased, and the
// http.Header-backed carrier the propagator ships with canonicalizes lookups
// (traceparent → Traceparent), so extraction silently produced an unrelated
// root span. Simulating the wire lowering here proves the carrier round-trips.
func TestInjectExtractAcrossLowercasedHeaders(t *testing.T) {
	exp := setupExporter(t)

	ctx, header, producer := InjectPublish(context.Background(), "subject.a")
	_ = ctx
	producer.End(nil)

	// Round-trip the headers the way NATS does: keys come back lowercased.
	onWire := nats.Header{}
	for k, v := range header {
		onWire[strings.ToLower(k)] = v
	}

	_, consumer := ExtractSubscribe(context.Background(), "subject.a", onWire)
	consumer.End(nil)

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if spans[0].SpanContext.TraceID() != spans[1].SpanContext.TraceID() {
		t.Fatalf("trace IDs differ after a lowercased round-trip: %s vs %s",
			spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID())
	}
	if spans[1].Parent.SpanID() != spans[0].SpanContext.SpanID() {
		t.Errorf("consumer parent = %s, want the producer span %s",
			spans[1].Parent.SpanID(), spans[0].SpanContext.SpanID())
	}
}

// TestExtractSubscribeWithoutContextStartsRoot pins the no-carrier case: a
// request that arrives without trace headers must still be recorded, as its own
// root, rather than dropped.
func TestExtractSubscribeWithoutContextStartsRoot(t *testing.T) {
	exp := setupExporter(t)
	_, span := ExtractSubscribe(context.Background(), "subject.b", nats.Header{})
	span.End(nil)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if !spans[0].Parent.IsValid() {
		return // a root, as intended
	}
	t.Errorf("consumer span has parent %s, want a root", spans[0].Parent.SpanID())
}

// TestFrameRecordsOpcodeAndName pins the frame span's attributes, which is what
// makes a slow-frame alert identifiable in a trace backend.
func TestFrameRecordsOpcodeAndName(t *testing.T) {
	exp := setupExporter(t)
	_, span := Frame(context.Background(), "CZ_ENTER", 0x0072)
	span.End(nil)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != "frame CZ_ENTER" {
		t.Errorf("span name = %q, want %q", got, "frame CZ_ENTER")
	}
	if spans[0].SpanKind != trace.SpanKindServer {
		t.Errorf("span kind = %v, want server", spans[0].SpanKind)
	}
	attrs := map[string]string{}
	for _, a := range spans[0].Attributes {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	if attrs["packet.opcode"] != "0x0072" || attrs["packet.name"] != "CZ_ENTER" {
		t.Errorf("attributes = %v, want opcode 0x0072 + name CZ_ENTER", attrs)
	}
}

// TestFrameWithoutKnownName pins the fallback for an opcode the packet DB does
// not define: still a span, still the opcode, no empty name segment.
func TestFrameWithoutKnownName(t *testing.T) {
	exp := setupExporter(t)
	_, span := Frame(context.Background(), "", 0x0bad)
	span.End(nil)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != "frame 0x0bad" {
		t.Errorf("span name = %q, want %q", got, "frame 0x0bad")
	}
}

// TestSpanEndRecordsError pins the failure path: a span ends even when the work
// failed, and carries both the error and the error status.
func TestSpanEndRecordsError(t *testing.T) {
	exp := setupExporter(t)
	boom := errors.New("boom")
	_, span := Frame(context.Background(), "CZ_ENTER", 0x0072)
	span.End(boom)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("status = %v, want Error", spans[0].Status.Code)
	}
	if len(spans[0].Events) == 0 {
		t.Error("no recorded error event")
	}
}
