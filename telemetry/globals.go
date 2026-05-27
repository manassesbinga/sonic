package telemetry

import (
	"context"
	"sync"
)

var (
	globalTelemetry *Telemetry
	globalMetrics   *Metrics
	globalMu        sync.RWMutex
)

func InitFromConfig(cfg TelemetryConfig) error {
	t, err := NewTelemetry(cfg)
	if err != nil {
		return err
	}

	globalMu.Lock()
	if globalTelemetry != nil {
		globalTelemetry.Shutdown(context.Background())
	}
	globalTelemetry = t
	globalMu.Unlock()

	if t.MeterProvider != nil {
		m, err := NewMeterProviderMetrics(t.MeterProvider)
		if err != nil {
			return err
		}
		globalMu.Lock()
		globalMetrics = m
		globalMu.Unlock()
	}

	return nil
}

func GetTelemetry() *Telemetry {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalTelemetry
}

func GetMetrics() *Metrics {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalMetrics
}

func GetLogger() *Logger {
	t := GetTelemetry()
	if t == nil {
		return nil
	}
	return t.Logger
}

func Shutdown() {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalTelemetry != nil {
		globalTelemetry.Shutdown(context.Background())
		globalTelemetry = nil
	}
	globalMetrics = nil
}
