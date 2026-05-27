package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "debug"
	case LogLevelInfo:
		return "info"
	case LogLevelWarn:
		return "warn"
	case LogLevelError:
		return "error"
	default:
		return "unknown"
	}
}

type LogEntry struct {
	Level   string         `json:"level"`
	Time    string         `json:"time"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

type Logger struct {
	mu      sync.Mutex
	writer  io.Writer
	format  string
	level   LogLevel
}

func NewLogger(format string, level string) *Logger {
	l := &Logger{
		writer: os.Stderr,
		format: format,
	}
	switch level {
	case "debug":
		l.level = LogLevelDebug
	case "warn":
		l.level = LogLevelWarn
	case "error":
		l.level = LogLevelError
	default:
		l.level = LogLevelInfo
	}
	return l
}

func (l *Logger) log(level LogLevel, msg string, fields map[string]any) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.format == "json" {
		entry := LogEntry{
			Level:   level.String(),
			Time:    time.Now().UTC().Format(time.RFC3339Nano),
			Message: msg,
			Fields:  fields,
		}
		b, _ := json.Marshal(entry)
		l.writer.Write(append(b, '\n'))
	} else {
		prefix := ""
		switch level {
		case LogLevelDebug:
			prefix = "[DEBUG]"
		case LogLevelInfo:
			prefix = "[INFO]"
		case LogLevelWarn:
			prefix = "[WARN]"
		case LogLevelError:
			prefix = "[ERROR]"
		}
		fmt.Fprintf(l.writer, "%s %s", prefix, msg)
		if len(fields) > 0 {
			fb, _ := json.Marshal(fields)
			fmt.Fprintf(l.writer, " %s", string(fb))
		}
		fmt.Fprintln(l.writer)
	}
}

func (l *Logger) Debug(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LogLevelDebug, msg, f)
}

func (l *Logger) Info(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LogLevelInfo, msg, f)
}

func (l *Logger) Warn(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LogLevelWarn, msg, f)
}

func (l *Logger) Error(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LogLevelError, msg, f)
}
