package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WASMEngine implements WorkerEngine for WebAssembly modules.
// Uses wazero (pure Go, no CGO) for safe, fast WASM execution.
type WASMEngine struct {
	runtime   wazero.Runtime
	module    api.Module
	kvStore   *KVStore
	timeoutMS time.Duration
	poolSize  int
	mu        sync.Mutex
}

// NewWasmEngine creates a new WASMEngine from a .wasm file.
func NewWasmEngine(filePath string, timeoutMS int, poolSize int, kvStore *KVStore) (*WASMEngine, error) {
	wasmBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM file: %w", err)
	}
	return NewWasmEngineFromBytes(wasmBytes, timeoutMS, poolSize, kvStore)
}

// NewWasmEngineFromBytes creates a new WASMEngine from raw WASM bytes (for testing).
func NewWasmEngineFromBytes(wasmBytes []byte, timeoutMS int, poolSize int, kvStore *KVStore) (*WASMEngine, error) {
	ctx := context.Background()

	r := wazero.NewRuntime(ctx)
	var err error

	// Instantiate WASI (for system calls)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	// Host functions for Sonic API
	hostMod := r.NewHostModuleBuilder("sonic")
	hostMod.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen uint32) uint64 {
			key, ok := m.Memory().Read(keyPtr, keyLen)
			if !ok {
				return 0
			}
			val := kvStore.Get(string(key))
			if val == nil {
				return 0
			}
			// TODO: Proper memory allocation for return values
			return 0
		}).
		WithParameterNames("key_ptr", "key_len").
		WithResultNames("val_ptr").
		Export("kv_get")

	hostMod.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen, valPtr, valLen uint32) {
			key, ok := m.Memory().Read(keyPtr, keyLen)
			if !ok {
				return
			}
			val, ok := m.Memory().Read(valPtr, valLen)
			if !ok {
				return
			}
			_ = kvStore.Set(string(key), val)
		}).
		WithParameterNames("key_ptr", "key_len", "val_ptr", "val_len").
		Export("kv_set")

	_, err = hostMod.Instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate host module: %w", err)
	}

	mod, err := r.Instantiate(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate WASM module: %w", err)
	}

	return &WASMEngine{
		runtime:   r,
		module:    mod,
		kvStore:   kvStore,
		timeoutMS: time.Duration(timeoutMS) * time.Millisecond,
		poolSize:  poolSize,
	}, nil
}

// ProcessPacket implements WorkerEngine.ProcessPacket.
func (we *WASMEngine) ProcessPacket(packet *Packet) (*PacketResult, error) {
	we.mu.Lock()
	defer we.mu.Unlock()

	// TODO: Pass packet to WASM and get result back
	// For now, return placeholder
	return &PacketResult{Allow: true, Packet: packet}, nil
}

// Type implements WorkerEngine.Type.
func (we *WASMEngine) Type() WorkerType {
	return WorkerTypeWebAssembly
}

// Close implements WorkerEngine.Close.
func (we *WASMEngine) Close() error {
	ctx := context.Background()
	var errs []error
	if we.module != nil {
		if err := we.module.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if we.runtime != nil {
		if err := we.runtime.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.New("failed to close WASM engine")
	}
	return nil
}
