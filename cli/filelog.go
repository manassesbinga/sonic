package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	logFile   *os.File
	logMu     sync.Mutex
	logDir    = "logs"
	logPath   = filepath.Join(logDir, "sonic.log")
	logInit   sync.Once
)

func initLogFile() {
	_ = os.MkdirAll(logDir, 0755)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	logFile = f
}

func writeLog(level, msg string) {
	logInit.Do(initLogFile)

	logMu.Lock()
	defer logMu.Unlock()

	if logFile == nil {
		return
	}

	ts := time.Now().Format("2006-01-02 15:04:05.000")
	_, _ = fmt.Fprintf(logFile, "[%s] %-7s %s\n", ts, level, msg)
}

