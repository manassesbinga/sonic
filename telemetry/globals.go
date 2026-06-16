package telemetry

import (
	"context"
	"sync"
	"time"
)

type FunctionExecution struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Worker      string            `json:"worker"`
	Function    string            `json:"function"`
	Duration    string            `json:"duration"`
	Status      string            `json:"status"`
	ErrorMsg    string            `json:"errorMsg,omitempty"`
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	RespStatus  int               `json:"respStatus,omitempty"`
	RespHeaders map[string]string `json:"respHeaders,omitempty"`
	RespBody    string            `json:"respBody,omitempty"`
	Logs        []WorkerLog       `json:"logs,omitempty"`
}

type WorkerLog struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type SafeLogBuffer struct {
	mu   sync.Mutex
	logs []WorkerLog
}

func (sb *SafeLogBuffer) AddLog(level, msg string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.logs = append(sb.logs, WorkerLog{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
	})
}

func (sb *SafeLogBuffer) GetLogs() []WorkerLog {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.logs == nil {
		return []WorkerLog{}
	}
	copied := make([]WorkerLog, len(sb.logs))
	copy(copied, sb.logs)
	return copied
}

var (
	globalTelemetry *Telemetry
	globalMetrics   *Metrics
	globalMu        sync.RWMutex

	executionsLog   []FunctionExecution
	executionsMu    sync.RWMutex
	executionsLimit = 200

	workerLogs      []WorkerLog
	workerLogsMu    sync.RWMutex
	workerLogsLimit = 500

	requestLogBuffers   = make(map[string]*SafeLogBuffer)
	requestLogBuffersMu sync.RWMutex
)

func SetRequestLogBuffer(reqID string, buf *SafeLogBuffer) {
	requestLogBuffersMu.Lock()
	defer requestLogBuffersMu.Unlock()
	requestLogBuffers[reqID] = buf
}

func ClearRequestLogBuffer(reqID string) {
	requestLogBuffersMu.Lock()
	defer requestLogBuffersMu.Unlock()
	delete(requestLogBuffers, reqID)
}

func AddRequestLog(reqID, level, msg string) {
	requestLogBuffersMu.RLock()
	buf, ok := requestLogBuffers[reqID]
	requestLogBuffersMu.RUnlock()
	if ok && buf != nil {
		buf.AddLog(level, msg)
	}
}

func AddFunctionExecution(exec FunctionExecution) {
	// Truncar payloads na telemetria em memoria para evitar leak de RAM
	if len(exec.Body) > 4096 {
		exec.Body = exec.Body[:4096] + "... [TRUNCATED]"
	}
	if len(exec.RespBody) > 4096 {
		exec.RespBody = exec.RespBody[:4096] + "... [TRUNCATED]"
	}

	executionsMu.Lock()
	defer executionsMu.Unlock()
	executionsLog = append(executionsLog, exec)
	if len(executionsLog) > executionsLimit {
		executionsLog = executionsLog[len(executionsLog)-executionsLimit:]
	}
}

func GetFunctionExecutions() []FunctionExecution {
	executionsMu.RLock()
	defer executionsMu.RUnlock()
	copied := make([]FunctionExecution, len(executionsLog))
	copy(copied, executionsLog)
	return copied
}

func AddWorkerLog(level, msg string) {
	workerLogsMu.Lock()
	defer workerLogsMu.Unlock()
	log := WorkerLog{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
	}
	workerLogs = append(workerLogs, log)
	if len(workerLogs) > workerLogsLimit {
		workerLogs = workerLogs[len(workerLogs)-workerLogsLimit:]
	}
}

func GetWorkerLogs() []WorkerLog {
	workerLogsMu.RLock()
	defer workerLogsMu.RUnlock()
	copied := make([]WorkerLog, len(workerLogs))
	copy(copied, workerLogs)
	return copied
}

func InitFromConfig(cfg TelemetryConfig) error {
	t, err := NewTelemetry(cfg)
	if err != nil {
		return err
	}

	globalMu.Lock()
	defer globalMu.Unlock()

	var m *Metrics
	if t.MeterProvider != nil {
		m, err = NewMeterProviderMetrics(t.MeterProvider)
		if err != nil {
			t.Shutdown(context.Background())
			return err
		}
	}

	if globalTelemetry != nil {
		globalTelemetry.Shutdown(context.Background())
	}
	globalTelemetry = t
	globalMetrics = m
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

func Shutdown() {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalTelemetry != nil {
		globalTelemetry.Shutdown(context.Background())
		globalTelemetry = nil
	}
	globalMetrics = nil
}

