package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Metrics struct {
	RequestsTotal         metric.Int64Counter
	RequestDuration       metric.Float64Histogram
	ConnectionsActive     metric.Int64UpDownCounter
	JSScriptDuration      metric.Float64Histogram
	WASMExecutionDuration metric.Float64Histogram
	VMPoolSize            metric.Int64Gauge
	ErrorsTotal           metric.Int64Counter
	CertGenDuration       metric.Float64Histogram
	BytesTransferred      metric.Int64Counter
}

func NewMeterProviderMetrics(mp metric.MeterProvider) (*Metrics, error) {
	meter := mp.Meter("sonic")

	requestsTotal, err := meter.Int64Counter(
		"sonic.requests.total",
		metric.WithDescription("Total number of requests processed"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram(
		"sonic.request.duration_ms",
		metric.WithDescription("Request processing duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	connectionsActive, err := meter.Int64UpDownCounter(
		"sonic.connections.active",
		metric.WithDescription("Number of active connections"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	jsScriptDuration, err := meter.Float64Histogram(
		"sonic.js.execution_duration_ms",
		metric.WithDescription("JavaScript worker execution duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	wasmDuration, err := meter.Float64Histogram(
		"sonic.wasm.execution_duration_ms",
		metric.WithDescription("WASM worker execution duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	vmPoolSize, err := meter.Int64Gauge(
		"sonic.vm.pool_size",
		metric.WithDescription("Number of VM instances in pool"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	errorsTotal, err := meter.Int64Counter(
		"sonic.errors.total",
		metric.WithDescription("Total number of errors by type"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	certGenDuration, err := meter.Float64Histogram(
		"sonic.certgen.duration_ms",
		metric.WithDescription("Certificate generation duration"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	bytesTransferred, err := meter.Int64Counter(
		"sonic.bytes.transferred",
		metric.WithDescription("Total bytes transferred"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		RequestsTotal:         requestsTotal,
		RequestDuration:       requestDuration,
		ConnectionsActive:     connectionsActive,
		JSScriptDuration:      jsScriptDuration,
		WASMExecutionDuration: wasmDuration,
		VMPoolSize:            vmPoolSize,
		ErrorsTotal:           errorsTotal,
		CertGenDuration:       certGenDuration,
		BytesTransferred:      bytesTransferred,
	}, nil
}

func RecordRequest(m *Metrics, ctx context.Context, domain, mode string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	m.RequestsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("sonic.domain", domain),
			attribute.String("sonic.mode", mode),
			attribute.Int("http.status_code", status),
		),
	)
	m.RequestDuration.Record(ctx, float64(duration.Microseconds())/1000.0,
		metric.WithAttributes(
			attribute.String("sonic.domain", domain),
			attribute.String("sonic.mode", mode),
		),
	)
}

func RecordJSDuration(m *Metrics, ctx context.Context, funcName string, duration time.Duration) {
	if m == nil {
		return
	}
	m.JSScriptDuration.Record(ctx, float64(duration.Microseconds())/1000.0,
		metric.WithAttributes(
			attribute.String("sonic.js_function", funcName),
		),
	)
}

func RecordError(m *Metrics, ctx context.Context, errType, domain string) {
	if m == nil {
		return
	}
	m.ErrorsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("sonic.error_type", errType),
			attribute.String("sonic.domain", domain),
		),
	)
}
