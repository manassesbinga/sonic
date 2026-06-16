package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type Telemetry struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *metric.MeterProvider
	Tracer         trace.Tracer
	MetricsServer  *http.Server
	config         TelemetryConfig
}

type TelemetryConfig struct {
	TracesEnabled   bool
	TracesEndpoint  string
	MetricsEnabled  bool
	MetricsPath     string
	MetricsPort     int
	ServiceName     string
	ServiceVersion  string
}

func DefaultTelemetryConfig() TelemetryConfig {
	return TelemetryConfig{
		TracesEnabled:  true,
		TracesEndpoint: "http://localhost:4318",
		MetricsEnabled: true,
		MetricsPath:    "/metrics",
		MetricsPort:    9090,
		ServiceName:    "sonic",
		ServiceVersion: "0.1.0",
	}
}

func NewTelemetry(cfg TelemetryConfig) (*Telemetry, error) {
	t := &Telemetry{
		config: cfg,
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		attribute.String("telemetry.sdk.language", "go"),
	)

	if cfg.TracesEnabled {
		traceExporter, err := otlptracehttp.New(context.Background(),
			otlptracehttp.WithEndpointURL(cfg.TracesEndpoint),
			otlptracehttp.WithTimeout(10*time.Second),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create trace exporter: %w", err)
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter,
				sdktrace.WithBatchTimeout(5*time.Second),
				sdktrace.WithMaxExportBatchSize(64),
			),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
		)
		t.TracerProvider = tp
		t.Tracer = tp.Tracer(cfg.ServiceName)
		otel.SetTracerProvider(tp)
	}

	mux := http.NewServeMux()

	// Health check endpoints (always available when telemetry is initialized)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"sonic"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ready","service":"sonic"}`))
	})

	if cfg.MetricsEnabled {
		promExporter, err := otelprom.New()
		if err != nil {
			return nil, fmt.Errorf("failed to create prometheus exporter: %w", err)
		}

		mp := metric.NewMeterProvider(
			metric.WithReader(promExporter),
			metric.WithResource(res),
		)
		t.MeterProvider = mp
		otel.SetMeterProvider(mp)

		mux.Handle(cfg.MetricsPath, promhttp.Handler())
	}

	t.MetricsServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: mux,
	}

	go func() {
		if err := t.MetricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		}
	}()

	return t, nil
}

func (t *Telemetry) Shutdown(ctx context.Context) {
	if t.MetricsServer != nil {
		t.MetricsServer.Shutdown(ctx)
	}
	if t.TracerProvider != nil {
		t.TracerProvider.Shutdown(ctx)
	}
}

func (t *Telemetry) StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t.Tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return t.Tracer.Start(ctx, name, opts...)
}
