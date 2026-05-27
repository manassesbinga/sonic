package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/manassesbinga/sonic/runtime"
	"github.com/manassesbinga/sonic/telemetry"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type WASMEngine struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	pool     *sync.Pool
	timeout  time.Duration
	kvStore  *runtime.KVStore
}

func NewWASMEngine(wasmBytes []byte, kvStore *runtime.KVStore) (*WASMEngine, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)

	if _, err := rt.NewHostModuleBuilder("sonic").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, ptr uint32, length uint32) {
		if buf, ok := m.Memory().Read(ptr, length); ok {
			os.Stderr.Write(append([]byte("[WASM] "), buf...))
		}
	}).Export("log").
		Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("failed to create host module: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("failed to compile wasm: %w", err)
	}

	engine := &WASMEngine{
		rt:      rt,
		compiled: compiled,
		timeout: time.Duration(cfg.timeoutMS) * time.Millisecond,
		kvStore: kvStore,
	}

	engine.pool = &sync.Pool{
		New: func() interface{} {
			inst, err := engine.instantiate()
			if err != nil {
				return nil
			}
			return inst
		},
	}

	poolSize := cfg.poolSize
	warmed := make([]api.Module, poolSize)
	for i := 0; i < poolSize; i++ {
		warmed[i] = engine.getInstance()
	}
	for _, inst := range warmed {
		if inst != nil {
			engine.pool.Put(inst)
		}
	}

	return engine, nil
}

type engineConfig struct {
	timeoutMS int
	poolSize  int
}

func loadConfig() (*engineConfig, error) {
	return &engineConfig{
		timeoutMS: 50,
		poolSize:  4,
	}, nil
}

func (e *WASMEngine) instantiate() (api.Module, error) {
	return e.rt.InstantiateModule(context.Background(), e.compiled, wazero.NewModuleConfig().WithName(""))
}

func (e *WASMEngine) getInstance() api.Module {
	inst := e.pool.Get()
	if inst == nil {
		return nil
	}
	return inst.(api.Module)
}

func (e *WASMEngine) returnInstance(inst api.Module) {
	if inst != nil {
		e.pool.Put(inst)
	}
}

func (e *WASMEngine) Type() runtime.WorkerType {
	return runtime.WorkerTypeWebAssembly
}

func (e *WASMEngine) Close() error {
	e.rt.Close(context.Background())
	return nil
}

func (e *WASMEngine) ProcessPacket(packet *runtime.Packet) (*runtime.PacketResult, error) {
	start := time.Now()
	if t := telemetry.GetTelemetry(); t != nil {
		_, span := t.StartSpan(context.Background(), "wasm.process_packet",
			trace.WithAttributes(
				attribute.String("sonic.protocol", string(packet.Protocol)),
			),
		)
		defer span.End()
	}
	defer func() {
		if m := telemetry.GetMetrics(); m != nil {
			m.WASMExecutionDuration.Record(context.Background(), float64(time.Since(start).Microseconds())/1000.0)
		}
	}()

	inst := e.getInstance()
	if inst == nil {
		return nil, errors.New("no available wasm instance")
	}
	defer e.returnInstance(inst)

	inputJSON, err := json.Marshal(packet)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal packet: %w", err)
	}

	malloc := inst.ExportedFunction("malloc")
	if malloc == nil {
		return nil, errors.New("wasm module does not export 'malloc'")
	}

	inputLen := uint64(len(inputJSON))
	allocResults, err := malloc.Call(context.Background(), inputLen)
	if err != nil {
		return nil, fmt.Errorf("malloc failed: %w", err)
	}
	inputPtr := uint32(allocResults[0])

	if !inst.Memory().Write(uint32(inputPtr), inputJSON) {
		return nil, errors.New("failed to write to wasm memory")
	}

	procCtx := context.Background()
	var cancel context.CancelFunc
	if e.timeout > 0 {
		procCtx, cancel = context.WithTimeout(context.Background(), e.timeout)
		defer cancel()
	}

	processFn := inst.ExportedFunction("process_packet")
	if processFn == nil {
		return nil, errors.New("wasm module does not export 'process_packet'")
	}

	results, err := processFn.Call(procCtx, uint64(inputPtr), uint64(inputLen))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			inst.Close(context.Background())
			if newInst, newErr := e.instantiate(); newErr == nil {
				e.pool.Put(newInst)
			}
			return nil, runtime.ErrExecutionTimeout
		}
		return nil, fmt.Errorf("process_packet failed: %w", err)
	}

	resultPacked := results[0]
	resultPtr := uint32(resultPacked & 0xFFFFFFFF)
	resultLen := uint32((resultPacked >> 32) & 0xFFFFFFFF)

	resultJSON, ok := inst.Memory().Read(resultPtr, resultLen)
	if !ok {
		return nil, errors.New("failed to read result from wasm memory")
	}

	if free := inst.ExportedFunction("free"); free != nil {
		_, _ = free.Call(context.Background(), uint64(inputPtr))
		_, _ = free.Call(context.Background(), uint64(resultPtr))
	}

	var packetResult runtime.PacketResult
	if err := json.Unmarshal(resultJSON, &packetResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return &packetResult, nil
}
