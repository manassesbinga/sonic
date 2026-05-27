package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkerType represents the type of worker runtime.
type WorkerType string

const (
	WorkerTypeJavaScript WorkerType = "javascript" // .js files
	WorkerTypeWebAssembly WorkerType = "webassembly" // .wasm files
	WorkerTypeNative WorkerType = "native" // Executable files (no extension or .exe on Windows)
)

// WorkerEngine is the interface that all worker runtimes must implement.
// This allows Sonic to support multiple languages: JavaScript, WebAssembly, native, etc.
type WorkerEngine interface {
	// ProcessPacket processes a protocol-agnostic Packet and returns a PacketResult.
	ProcessPacket(packet *Packet) (*PacketResult, error)

	// Type returns the type of this worker engine.
	Type() WorkerType

	// Close cleans up any resources used by the engine.
	Close() error
}

// WorkerConfig holds configuration for a worker.
type WorkerConfig struct {
	FilePath    string
	TimeoutMS   int
	PoolSize    int
	KVStore     *KVStore
}

// WorkerManager manages multiple WorkerEngines and routes Packets to the appropriate one.
// It detects worker types automatically based on file extensions.
type WorkerManager struct {
	kvStore    *KVStore
	engines    map[string]WorkerEngine // filepath -> WorkerEngine
}

// NewWorkerManager creates a new WorkerManager with the given KV Store.
func NewWorkerManager(kvStore *KVStore) *WorkerManager {
	return &WorkerManager{
		kvStore: kvStore,
		engines: make(map[string]WorkerEngine),
	}
}

// LoadWorker loads a worker from a file, detecting its type automatically.
func (wm *WorkerManager) LoadWorker(cfg WorkerConfig) error {
	ext := strings.ToLower(filepath.Ext(cfg.FilePath))
	
	var engine WorkerEngine
	var errEngine error

	switch ext {
	case ".js":
		jsCode, err := os.ReadFile(cfg.FilePath)
		if err != nil {
			return fmt.Errorf("failed to read JS file: %w", err)
		}
		engine, errEngine = NewJSEngineWithKV(string(jsCode), cfg.TimeoutMS, cfg.PoolSize, cfg.KVStore)
		if errEngine != nil {
			return fmt.Errorf("failed to create JS engine: %w", errEngine)
		}

	case ".wasm":
		engine, errEngine = NewWasmEngine(cfg.FilePath, cfg.TimeoutMS, cfg.PoolSize, cfg.KVStore)
		if errEngine != nil {
			return fmt.Errorf("failed to create WASM engine: %w", errEngine)
		}

	default:
		// Check if it's an executable file
		info, err := os.Stat(cfg.FilePath)
		if err != nil {
			return fmt.Errorf("failed to stat file: %w", err)
		}
		if info.Mode().Perm()&0111 != 0 || ext == ".exe" {
			return errors.New("native executable support coming soon")
		}
		return fmt.Errorf("unknown worker type for file: %s", cfg.FilePath)
	}

	wm.engines[cfg.FilePath] = engine
	return nil
}

// LoadAllWorkers loads all workers from a directory.
func (wm *WorkerManager) LoadAllWorkers(dir string, timeoutMS int, poolSize int) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		filePath := filepath.Join(dir, file.Name())
		err := wm.LoadWorker(WorkerConfig{
			FilePath:  filePath,
			TimeoutMS: timeoutMS,
			PoolSize:  poolSize,
			KVStore:   wm.kvStore,
		})
		if err != nil {
			// Skip files we can't load
			continue
		}
	}
	return nil
}

// ProcessPacket routes a Packet to all loaded worker engines.
// For now, it processes with the first available engine (simple strategy).
func (wm *WorkerManager) ProcessPacket(packet *Packet) (*PacketResult, error) {
	for _, engine := range wm.engines {
		result, err := engine.ProcessPacket(packet)
		if err != nil {
			continue
		}
		return result, nil
	}

	// If no workers processed it, just allow the packet
	return &PacketResult{Allow: true, Packet: packet}, nil
}

// Close closes all loaded worker engines.
func (wm *WorkerManager) Close() error {
	var errs []error
	for path, engine := range wm.engines {
		if err := engine.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to close some engines: %v", errs)
	}
	return nil
}

// KVStore returns the shared KV Store.
func (wm *WorkerManager) KVStore() *KVStore {
	return wm.kvStore
}
