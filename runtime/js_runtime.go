package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/manassesbinga/sonic/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrExecutionTimeout is returned when a JS script exceeds its CPU time limit.
var ErrExecutionTimeout = errors.New("execution timeout: script exceeded CPU time limit")

// JSEngine manages a pool of Goja JavaScript VMs for concurrent request processing.
// It pre-compiles user scripts and provides onTraffic/onResponse execution with
// CPU watchdog timeouts and panic recovery.
//
// JSEngine implements the WorkerEngine interface.
type JSEngine struct {
	program   *goja.Program
	vmPool    *sync.Pool
	timeoutMS time.Duration
	kvStore   *KVStore
}

// NewJSEngine creates a new JSEngine, compiles the JS code once, and pre-warms
// a pool of VMs. Each VM has the Web Standard API bootstrap (Request, Response,
// Headers, fetch) plus user-defined onTraffic/onResponse functions.
//
// Parameters:
//   - jsCode: user JavaScript defining onTraffic and/or onResponse
//   - timeoutMS: max execution time per call (e.g. 50ms)
//   - poolSize: number of pre-warmed VMs (e.g. 64)
func NewJSEngine(jsCode string, timeoutMS int, poolSize int) (*JSEngine, error) {
	return NewJSEngineWithKV(jsCode, timeoutMS, poolSize, nil)
}

// NewJSEngineWithKV creates a new JSEngine with a shared KV Store.
func NewJSEngineWithKV(jsCode string, timeoutMS int, poolSize int, kvStore *KVStore) (*JSEngine, error) {
	program, err := goja.Compile("worker.js", jsCode, false)
	if err != nil {
		return nil, fmt.Errorf("erro ao compilar codigo javascript: %w", err)
	}

	engine := &JSEngine{
		program:   program,
		timeoutMS: time.Duration(timeoutMS) * time.Millisecond,
		kvStore:   kvStore,
	}

	engine.vmPool = &sync.Pool{
		New: func() interface{} {
			return engine.createVM()
		},
	}

	vms := make([]*goja.Runtime, poolSize)
	for i := 0; i < poolSize; i++ {
		vms[i] = engine.vmPool.Get().(*goja.Runtime)
	}
	for i := 0; i < poolSize; i++ {
		engine.vmPool.Put(vms[i])
	}

	return engine, nil
}

// Type implements WorkerEngine.Type.
func (e *JSEngine) Type() WorkerType {
	return WorkerTypeJavaScript
}

// Close implements WorkerEngine.Close.
func (e *JSEngine) Close() error {
	return nil
}

// ProcessPacket implements WorkerEngine.ProcessPacket.
// It converts the generic Packet to the HTTP-specific Request/Response and calls onTraffic/onResponse.
func (e *JSEngine) ProcessPacket(packet *Packet) (*PacketResult, error) {
	if packet.Request != nil {
		result, err := e.RunOnTraffic(packet.Request)
		if err != nil {
			return nil, err
		}
		if result.IsResponse {
			return &PacketResult{
				Allow:    true,
				Packet:   packet,
				Response: &Response{Status: result.Status, Headers: result.Headers, Body: result.Body},
			}, nil
		}
		modifiedReq := &Request{Method: result.Method, URL: result.URL, Path: result.Path, Headers: result.Headers, Body: result.Body}
		modifiedPacket := *packet
		modifiedPacket.Request = modifiedReq
		return &PacketResult{Allow: true, Packet: &modifiedPacket}, nil
	}
	if packet.Response != nil {
		resp, err := e.RunOnResponse(packet.Response)
		if err != nil {
			return nil, err
		}
		modifiedPacket := *packet
		modifiedPacket.Response = resp
		return &PacketResult{Allow: true, Packet: &modifiedPacket}, nil
	}
	return &PacketResult{Allow: true, Packet: packet}, nil
}

func (e *JSEngine) createVM() *goja.Runtime {
	vm := goja.New()

	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	e.setupBridges(vm)

	_, err := vm.RunString(JSBootstrap)
	if err != nil {
		panic(fmt.Sprintf("erro critico ao carregar bootstrap das APIs Web Standard na VM: %v", err))
	}

	_, err = vm.RunProgram(e.program)
	if err != nil {
		panic(fmt.Sprintf("erro critico ao carregar script na VM do pool: %v", err))
	}

	return vm
}

// getVM retrieves a VM from the pool, recovering from any pool creation panics.
func (e *JSEngine) getVM() (vm *goja.Runtime, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("VM creation failed: %v", r)
		}
	}()
	vm = e.vmPool.Get().(*goja.Runtime)
	return vm, nil
}

// setupBridges registers native Go bridge functions inside the JS VM.
func (e *JSEngine) setupBridges(vm *goja.Runtime) {
	vm.Set("log", func(msg string) {
		fmt.Fprintf(os.Stderr, "[JS LOG] %s\n", msg)
	})

	vm.Set("jwtVerify", func(token string, secret string) bool {
		if token == "" || secret == "" {
			return false
		}
		return len(token) > 10
	})

	// KV Store bridge - shared state between all workers
	if e.kvStore != nil {
		vm.Set("kv", map[string]interface{}{
			"get": func(key string) string {
				val := e.kvStore.Get(key)
				if val == nil {
					return ""
				}
				return string(val)
			},
			"set": func(key string, value string) {
				_ = e.kvStore.Set(key, []byte(value))
			},
			"delete": func(key string) {
				_ = e.kvStore.Delete(key)
			},
			"exists": func(key string) bool {
				return e.kvStore.Exists(key)
			},
			"keys": func() []string {
				return e.kvStore.Keys()
			},
			"clear": func() {
				_ = e.kvStore.Clear()
			},
		})
	}

	// require() loads JS modules from the ./modules/ directory.
	// Modules should set module.exports = { ... }.
	// Usage in JS: var uuid = require("uuid");
	vm.Set("require", func(modulePath string) (interface{}, error) {
		moduleDir := filepath.Join(".", "modules")
		if !strings.HasSuffix(modulePath, ".js") {
			modulePath = modulePath + ".js"
		}
		fullPath := filepath.Join(moduleDir, filepath.Clean(modulePath))
		code, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("module not found: %s", modulePath)
		}
		vm.RunString("var __mod = {exports:{}};")
		_, err = vm.RunString(string(code))
		if err != nil {
			return nil, fmt.Errorf("module error %s: %v", modulePath, err)
		}
		expVal := vm.Get("__mod")
		if expVal == nil {
			return nil, nil
		}
		obj := expVal.ToObject(vm)
		exports := obj.Get("exports")
		if exports == nil {
			return nil, nil
		}
		return exports.Export(), nil
	})

	// _goFetch is the native Go HTTP client exposed as fetch() in JS.
	vm.Set("_goFetch", func(method, urlStr, headersJSON, bodyStr string) map[string]interface{} {
		failRes := map[string]interface{}{
			"status":  502,
			"headers": map[string]string{"content-type": "text/plain"},
			"body":    "Bad Gateway: outbound fetch failed",
		}

		client := &http.Client{Timeout: 10 * time.Second}

		var bodyReader io.Reader
		if bodyStr != "" {
			bodyReader = strings.NewReader(bodyStr)
		}

		req, err := http.NewRequest(method, urlStr, bodyReader)
		if err != nil {
			return failRes
		}

		var headersMap map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &headersMap); err == nil {
			for k, v := range headersMap {
				req.Header.Set(k, v)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return failRes
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return failRes
		}

		respHeaders := make(map[string]string)
		for k, v := range resp.Header {
			if len(v) > 0 {
				respHeaders[strings.ToLower(k)] = v[0]
			}
		}

		return map[string]interface{}{
			"status":  resp.StatusCode,
			"headers": respHeaders,
			"body":    string(respBody),
		}
	})
}

// RunOnTraffic executes the user's onTraffic function with a Request and
// returns the modified TrafficResult. Supports CPU watchdog timeout and panic recovery.
//
// The JS function can return:
//   - a modified Request (normal flow)
//   - a Response directly (WAF block, redirect, etc.)
//   - null/undefined (preserves original request)
func (e *JSEngine) RunOnTraffic(req *Request) (*TrafficResult, error) {
	start := time.Now()
	if t := telemetry.GetTelemetry(); t != nil {
		_, span := t.StartSpan(context.Background(), "js.run_on_traffic",
			trace.WithAttributes(
				attribute.String("sonic.js_function", "onTraffic"),
				attribute.String("http.method", req.Method),
				attribute.String("http.url", req.URL),
			),
		)
		defer span.End()
	}
	defer func() {
		if m := telemetry.GetMetrics(); m != nil {
			telemetry.RecordJSDuration(m, context.Background(), "onTraffic", time.Since(start))
		}
	}()

	vm, err := e.getVM()
	if err != nil {
		return nil, err
	}

	done := make(chan struct{})
	var execErr error
	var resultVal goja.Value

	go func() {
		select {
		case <-done:
			return
		case <-time.After(e.timeoutMS):
			vm.Interrupt(ErrExecutionTimeout)
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && errors.Is(err, ErrExecutionTimeout) {
					execErr = ErrExecutionTimeout
				} else {
					execErr = fmt.Errorf("panic na execucao JS: %v", r)
				}
			}
			close(done)
		}()

		wrapTrafficVal := vm.Get("_wrapOnTraffic")
		if wrapTrafficVal == nil {
			execErr = fmt.Errorf("_wrapOnTraffic nao encontrada no bootstrap")
			return
		}

		wrapTraffic, ok := goja.AssertFunction(wrapTrafficVal)
		if !ok {
			execErr = fmt.Errorf("_wrapOnTraffic nao e uma funcao valida")
			return
		}

		res, err := wrapTraffic(goja.Undefined(), vm.ToValue(req))
		if err != nil {
			execErr = err
			return
		}
		resultVal = res
	}()

	if execErr != nil {
		if errors.Is(execErr, ErrExecutionTimeout) {
			e.vmPool.Put(e.createVM())
		} else {
			e.vmPool.Put(vm)
		}
		return nil, execErr
	}

	e.vmPool.Put(vm)

	if resultVal == nil || goja.IsUndefined(resultVal) || goja.IsNull(resultVal) {
		return &TrafficResult{
			IsResponse: false,
			Method:     req.Method,
			URL:        req.URL,
			Path:       req.Path,
			Headers:    req.Headers,
			Body:       req.Body,
		}, nil
	}

	var result TrafficResult
	err = vm.ExportTo(resultVal, &result)
	if err != nil {
		return nil, fmt.Errorf("erro ao mapear retorno do JS para TrafficResult: %w", err)
	}

	return &result, nil
}

// RunOnResponse executes the user's onResponse function with a Response and
// returns the (possibly modified) Response. Supports CPU watchdog timeout.
func (e *JSEngine) RunOnResponse(resp *Response) (*Response, error) {
	start := time.Now()
	if t := telemetry.GetTelemetry(); t != nil {
		_, span := t.StartSpan(context.Background(), "js.run_on_response",
			trace.WithAttributes(
				attribute.String("sonic.js_function", "onResponse"),
				attribute.Int("http.status_code", resp.Status),
			),
		)
		defer span.End()
	}
	defer func() {
		if m := telemetry.GetMetrics(); m != nil {
			telemetry.RecordJSDuration(m, context.Background(), "onResponse", time.Since(start))
		}
	}()

	vm, err := e.getVM()
	if err != nil {
		return nil, err
	}

	done := make(chan struct{})
	var execErr error
	var resultVal goja.Value

	go func() {
		select {
		case <-done:
			return
		case <-time.After(e.timeoutMS):
			vm.Interrupt(ErrExecutionTimeout)
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && errors.Is(err, ErrExecutionTimeout) {
					execErr = ErrExecutionTimeout
				} else {
					execErr = fmt.Errorf("panic na execucao JS: %v", r)
				}
			}
			close(done)
		}()

		wrapResponseVal := vm.Get("_wrapOnResponse")
		if wrapResponseVal == nil {
			execErr = fmt.Errorf("_wrapOnResponse nao encontrada no bootstrap")
			return
		}

		wrapResponse, ok := goja.AssertFunction(wrapResponseVal)
		if !ok {
			execErr = fmt.Errorf("_wrapOnResponse nao e uma funcao valida")
			return
		}

		res, err := wrapResponse(goja.Undefined(), vm.ToValue(resp))
		if err != nil {
			execErr = err
			return
		}
		resultVal = res
	}()

	if execErr != nil {
		if errors.Is(execErr, ErrExecutionTimeout) {
			e.vmPool.Put(e.createVM())
		} else {
			e.vmPool.Put(vm)
		}
		return nil, execErr
	}

	e.vmPool.Put(vm)

	if resultVal == nil || goja.IsUndefined(resultVal) || goja.IsNull(resultVal) {
		return resp, nil
	}

	var modifiedResp Response
	err = vm.ExportTo(resultVal, &modifiedResp)
	if err != nil {
		return nil, fmt.Errorf("erro ao mapear retorno do JS para Response: %w", err)
	}

	return &modifiedResp, nil
}
