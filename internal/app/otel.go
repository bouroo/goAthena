package app

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/bouroo/goAthena/internal/config"
)

// otelEnabled reports whether the config requests live trace export. It is the
// pure decision predicate; initOTel does the actual SDK wiring. Unit-tested
// without a live collector.
func otelEnabled(cfg config.OTelConfig) bool {
	return cfg.Exporter == "otlp" && cfg.Endpoint != ""
}

// initOTel initializes the OTel trace SDK when otelEnabled(cfg). It registers a
// global TracerProvider and returns a shutdown func the caller runs on graceful
// exit. Best-effort: a dial/refused exporter is logged and the warning path is
// taken (the server stays up — OTel is observability, not a startup dependency).
//
// Only traces are exported today (meter provider + OTLP/metrics are future
// work). The gRPC exporter uses an insecure dial — the endpoint typically runs
// alongside the app inside compose; a TLS flag can be added for external
// collectors.
func initOTel(ctx context.Context, cfg config.OTelConfig, log *slog.Logger) (shutdown func(), wired bool) {
	if !otelEnabled(cfg) {
		return nil, false
	}

	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	exp, err := otlptracegrpc.New(dialCtx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		log.Warn("otel: trace exporter init failed; telemetry dropped", "endpoint", cfg.Endpoint, "err", err)
		return nil, false
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
	))
	if err != nil {
		// Non-fatal: fall back to the default resource attributes.
		log.Warn("otel: resource merge failed; using defaults", "err", err)
		res = nil
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.Sampling))),
	)
	otel.SetTracerProvider(tp)

	shutdown = func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := tp.Shutdown(stopCtx); err != nil {
			log.Warn("otel: tracer shutdown", "err", err)
		}
	}
	log.Info("otel: trace export enabled",
		"endpoint", cfg.Endpoint, "service_name", cfg.ServiceName, "sampling", cfg.Sampling)
	return shutdown, true
}
