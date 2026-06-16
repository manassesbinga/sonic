package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manassesbinga/sonic/telemetry"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WASMEngine implements WorkerEngine for WebAssembly modules.
// Uses wazero (pure Go, no CGO) for safe, fast WASM execution.
type WASMEngine struct {
	runtime        wazero.Runtime
	compiledModule wazero.CompiledModule
	module         api.Module
	modulePool     chan api.Module
	wasmBytes      []byte
	kvStore        *KVStore
	timeoutMS      time.Duration
	poolSize       int
	mu             sync.Mutex
	quit           chan struct{}
	closed         bool
	ctx            context.Context

	// Circuit breaker pattern (same as NativeEngine)
	consecutiveFailures int32
	lastFailureTime     time.Time
	disabled            bool

	// Cap on-demand module creation to prevent unbounded memory growth
	onDemandCount int32
}

// NewWasmEngine creates a new WASMEngine from a .wasm file.
func NewWasmEngine(filePath string, timeoutMS int, poolSize int, kvStore *KVStore) (*WASMEngine, error) {
	return NewWasmEngineWithContext(filePath, timeoutMS, poolSize, kvStore, context.Background())
}

// NewWasmEngineFromBytes creates a new WASMEngine from raw WASM bytes (for testing).
func NewWasmEngineFromBytes(wasmBytes []byte, timeoutMS int, poolSize int, kvStore *KVStore) (*WASMEngine, error) {
	return NewWasmEngineFromBytesWithContext(wasmBytes, timeoutMS, poolSize, kvStore, context.Background())
}

// NewWasmEngineWithContext creates a new WASMEngine from a .wasm file with a parent context.
func NewWasmEngineWithContext(filePath string, timeoutMS int, poolSize int, kvStore *KVStore, parentCtx context.Context) (*WASMEngine, error) {
	wasmBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM file: %w", err)
	}
	return NewWasmEngineFromBytesWithContext(wasmBytes, timeoutMS, poolSize, kvStore, parentCtx)
}

// NewWasmEngineFromBytesWithContext creates a new WASMEngine from raw WASM bytes with a parent context.
func NewWasmEngineFromBytesWithContext(wasmBytes []byte, timeoutMS int, poolSize int, kvStore *KVStore, parentCtx context.Context) (*WASMEngine, error) {
	ctx := parentCtx

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
			malloc := m.ExportedFunction("malloc")
			if malloc == nil {
				return 0
			}
			valLen := uint64(len(val))
			results, err := malloc.Call(ctx, valLen)
			if err != nil || len(results) == 0 {
				return 0
			}
			valPtr := uint32(results[0])
			m.Memory().Write(valPtr, val)
			return (valLen << 32) | uint64(valPtr)
		}).
		WithParameterNames("key_ptr", "key_len").
		WithResultNames("val_ptr_len").
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

	compiledModule, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = r.Close(ctx)
		return nil, fmt.Errorf("failed to compile WASM module: %w", err)
	}

	if poolSize < 1 {
		poolSize = 4
	}
	engine := &WASMEngine{
		runtime:        r,
		compiledModule: compiledModule,
		wasmBytes:      wasmBytes,
		kvStore:        kvStore,
		timeoutMS:      time.Duration(timeoutMS) * time.Millisecond,
		poolSize:       poolSize,
		modulePool:     make(chan api.Module, poolSize),
		quit:           make(chan struct{}),
		ctx:            ctx,
	}
	for i := 0; i < poolSize; i++ {
		mod, err := r.InstantiateModule(ctx, compiledModule, wazero.NewModuleConfig())
		if err != nil {
			// cleanup already created modules
			for j := 0; j < i; j++ {
				m := <-engine.modulePool
				_ = m.Close(ctx)
			}
			_ = compiledModule.Close(ctx)
			_ = r.Close(ctx)
			return nil, fmt.Errorf("failed to instantiate WASM module %d/%d: %w", i+1, poolSize, err)
		}
		engine.modulePool <- mod
	}

	return engine, nil
}

const maxWASMOnDemand = 2

// ProcessPacket implements WorkerEngine.ProcessPacket.
func (we *WASMEngine) ProcessPacket(packet *Packet) (result *PacketResult, err error) {
	start := time.Now()
	defer func() {
		if m := telemetry.GetMetrics(); m != nil {
			telemetry.RecordWASMDuration(m, we.ctx, time.Since(start))
		}
	}()

	mod, err := we.leaseModule()
	if err != nil {
		return nil, err
	}
	defer we.releaseModule(mod)

	failed := true
	defer func() {
		if failed {
			we.recordFailure()
		} else {
			we.recordSuccess()
		}
	}()

	ctx, cancel := context.WithTimeout(we.ctx, we.timeoutMS)
	defer cancel()

	malloc := mod.ExportedFunction("malloc")
	free := mod.ExportedFunction("free")
	processPacket := mod.ExportedFunction("process_packet")

	var ptr uint64
	dataLen := uint64(len(packet.Data))
	if malloc != nil && dataLen > 0 {
		results, err := malloc.Call(ctx, dataLen)
		if err != nil {
			return nil, fmt.Errorf("wasm malloc failed: %w", err)
		}
		if len(results) > 0 {
			ptr = results[0]
		}
	}

	if dataLen > 0 && ptr != 0 {
		ok := mod.Memory().Write(uint32(ptr), packet.Data)
		if !ok {
			if free != nil && ptr != 0 {
				_, _ = free.Call(ctx, ptr)
			}
			return nil, fmt.Errorf("failed to write packet data to WASM memory at pointer %d", ptr)
		}
	}

	if processPacket != nil {
		results, err := processPacket.Call(ctx, ptr, dataLen)
		if err != nil {
			if free != nil && ptr != 0 {
				_, _ = free.Call(ctx, ptr)
			}
			return nil, fmt.Errorf("wasm process_packet failed: %w", err)
		}
		if len(results) > 0 {
			retVal := results[0]
			outPtr := uint32(retVal & 0xffffffff)
			outLen := uint32(retVal >> 32)

			var modifiedData []byte
			if outLen > 0 {
				modifiedData = make([]byte, outLen)
				data, ok := mod.Memory().Read(outPtr, outLen)
				if !ok {
					if free != nil && ptr != 0 {
						_, _ = free.Call(ctx, ptr)
					}
					return nil, fmt.Errorf("failed to read modified data from WASM memory at %d with len %d", outPtr, outLen)
				}
				copy(modifiedData, data)
			}

			if free != nil && ptr != 0 {
				_, _ = free.Call(ctx, ptr)
			}
			if free != nil && outPtr != 0 && outPtr != uint32(ptr) {
				_, _ = free.Call(ctx, uint64(outPtr))
			}

			newPacket := *packet
			newPacket.Data = modifiedData
			failed = false
			return &PacketResult{
				Allow:  true,
				Packet: &newPacket,
			}, nil
		}
	}

	if free != nil && ptr != 0 {
		_, _ = free.Call(ctx, ptr)
	}
	failed = false
	return &PacketResult{Allow: true, Packet: packet}, nil
}

func (we *WASMEngine) leaseModule() (api.Module, error) {
	we.mu.Lock()

	if we.closed {
		we.mu.Unlock()
		return nil, errors.New("WASM engine is closed")
	}

	if we.disabled {
		if time.Since(we.lastFailureTime) > 30*time.Second {
			we.disabled = false
			atomic.StoreInt32(&we.consecutiveFailures, 0)
		} else {
			we.mu.Unlock()
			return nil, errors.New("WASM engine temporarily disabled due to consecutive failures")
		}
	}

	we.mu.Unlock()

	select {
	case mod := <-we.modulePool:
		return mod, nil
	default:
		// Se o pool estiver vazio, aguarda até we.timeoutMS por um módulo liberado
		timer := time.NewTimer(we.timeoutMS)
		defer timer.Stop()
		select {
		case mod := <-we.modulePool:
			return mod, nil
		case <-timer.C:
			onDemand := atomic.AddInt32(&we.onDemandCount, 1)
			if onDemand > int32(we.poolSize*maxWASMOnDemand) {
				atomic.AddInt32(&we.onDemandCount, -1)
				return nil, errors.New("WASM engine overloaded: too many on-demand modules (pool exhausted and wait timed out)")
			}
			mod, err := we.runtime.InstantiateModule(we.ctx, we.compiledModule, wazero.NewModuleConfig())
			if err != nil {
				atomic.AddInt32(&we.onDemandCount, -1)
				return nil, fmt.Errorf("failed to create temp WASM module after wait: %w", err)
			}
			return mod, nil
		}
	}
}

func (we *WASMEngine) recordFailure() {
	failures := atomic.AddInt32(&we.consecutiveFailures, 1)
	we.mu.Lock()
	we.lastFailureTime = time.Now()
	if failures >= 5 && !we.disabled {
		we.disabled = true
	}
	we.mu.Unlock()
}

func (we *WASMEngine) recordSuccess() {
	atomic.StoreInt32(&we.consecutiveFailures, 0)
}

func (we *WASMEngine) releaseModule(mod api.Module) {
	we.mu.Lock()
	defer we.mu.Unlock()
	if we.closed {
		_ = mod.Close(context.Background())
		return
	}
	select {
	case we.modulePool <- mod:
	default:
		// Pool is full; this was an on-demand module — close and decrement counter
		atomic.AddInt32(&we.onDemandCount, -1)
		_ = mod.Close(context.Background())
	}
}


// Type implements WorkerEngine.Type.
func (we *WASMEngine) Type() WorkerType {
	return WorkerTypeWebAssembly
}

// Close implements WorkerEngine.Close.
func (we *WASMEngine) Close() error {
	we.mu.Lock()
	defer we.mu.Unlock()

	if we.closed {
		return nil
	}
	we.closed = true
	close(we.quit)

	ctx := context.Background()
	var errs []error

drainLoop:
	for {
		select {
		case mod := <-we.modulePool:
			if err := mod.Close(ctx); err != nil {
				errs = append(errs, err)
			}
		default:
			break drainLoop
		}
	}

	if we.compiledModule != nil {
		if err := we.compiledModule.Close(ctx); err != nil {
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
