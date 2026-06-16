package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/manassesbinga/sonic/neuralcache"
	"github.com/manassesbinga/sonic/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrExecutionTimeout is returned when a JS script exceeds its CPU time limit.
var ErrExecutionTimeout = errors.New("execution timeout: script exceeded CPU time limit")

const maxModuleCacheSize = 256

// JSEngine manages a pool of Goja JavaScript VMs for concurrent request processing.
// It pre-compiles user scripts and provides onTraffic/onResponse execution with
// CPU watchdog timeouts and panic recovery.
//
// JSEngine implements the WorkerEngine interface.
type JSEngine struct {
	program     *goja.Program
	vmChannel   chan *goja.Runtime
	timeoutMS   time.Duration
	kvStore     *KVStore
	neuralCache *neuralcache.NeuralCache
	ncMu        sync.RWMutex
	moduleCache map[string]interface{}
	moduleMu    sync.RWMutex
	aiModels    map[string]*AIModel
	aiMu        sync.RWMutex
}

// AIModel representa a modelagem de IA importada nos workers
type AIModel struct {
	Name   string    `json:"name"`
	Layers []AILayer `json:"layers"`
}

// AILayer representa uma camada individual do modelo
type AILayer struct {
	Type    string      `json:"type"`
	Weights [][]float64 `json:"weights"`
	Biases  []float64   `json:"biases"`
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
	return NewJSEngineWithKVAndNeuralCache(jsCode, timeoutMS, poolSize, kvStore, nil)
}

// NewJSEngineWithKVAndNeuralCache creates a new JSEngine with a shared KV Store and Neural Cache.
func NewJSEngineWithKVAndNeuralCache(jsCode string, timeoutMS int, poolSize int, kvStore *KVStore, neuralCache *neuralcache.NeuralCache) (*JSEngine, error) {
	program, err := goja.Compile("worker.js", jsCode, false)
	if err != nil {
		return nil, fmt.Errorf("erro ao compilar codigo javascript: %w", err)
	}

	engine := &JSEngine{
		program:     program,
		timeoutMS:   time.Duration(timeoutMS) * time.Millisecond,
		kvStore:     kvStore,
		neuralCache: neuralCache,
		moduleCache: make(map[string]interface{}),
		aiModels:    make(map[string]*AIModel),
	}

	engine.vmChannel = make(chan *goja.Runtime, poolSize)
	for i := 0; i < poolSize; i++ {
		vm, err := engine.createVM()
		if err != nil {
			return nil, err
		}
		engine.vmChannel <- vm
	}

	return engine, nil
}

// SetNeuralCache define o Neural Cache para o JSEngine.
func (e *JSEngine) SetNeuralCache(nc *neuralcache.NeuralCache) {
	e.ncMu.Lock()
	e.neuralCache = nc
	e.ncMu.Unlock()
}

// Type implements WorkerEngine.Type.
func (e *JSEngine) Type() WorkerType {
	return WorkerTypeJavaScript
}

// Close implements WorkerEngine.Close.
func (e *JSEngine) Close() error {
	for {
		select {
		case <-e.vmChannel:
		default:
			return nil
		}
	}
}

// ProcessPacket implements WorkerEngine.ProcessPacket.
// It converts the generic Packet to the HTTP-specific Request/Response and calls onTraffic/onResponse.
func (e *JSEngine) ProcessPacket(packet *Packet) (*PacketResult, error) {
	if packet.Request != nil {
		packet.Request.ID = packet.ID
		result, err := e.RunOnTraffic(packet.Request)
		if err != nil {
			return nil, err
		}
		if result.IsResponse {
			return &PacketResult{
				Allow:    true,
				Packet:   packet,
				Response: &Response{ID: packet.ID, Status: result.Status, Headers: result.Headers, Body: result.Body},
			}, nil
		}
		modifiedReq := &Request{ID: packet.ID, Method: result.Method, URL: result.URL, Path: result.Path, Headers: result.Headers, Body: result.Body}
		modifiedPacket := *packet
		modifiedPacket.Request = modifiedReq
		return &PacketResult{Allow: true, Packet: &modifiedPacket}, nil
	}
	if packet.Response != nil {
		packet.Response.ID = packet.ID
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

func (e *JSEngine) createVM() (*goja.Runtime, error) {
	vm := goja.New()

	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	e.setupBridges(vm)

	_, err := vm.RunString(JSBootstrap)
	if err != nil {
		return nil, fmt.Errorf("erro critico ao carregar bootstrap das APIs Web Standard na VM: %w", err)
	}

	_, err = vm.RunProgram(e.program)
	if err != nil {
		return nil, fmt.Errorf("erro critico ao carregar script na VM do pool: %w", err)
	}

	return vm, nil
}

func (e *JSEngine) recreateAndQueueVM() {
	vm, err := e.createVM()
	if err == nil {
		select {
		case e.vmChannel <- vm:
		default:
			// Pool is full; discard the replacement VM to prevent deadlock.
		}
	} else {
		fmt.Fprintf(os.Stderr, "Erro ao recriar VM no pool: %v\n", err)
	}
}

// getVM retrieves a VM from the channel pool, waiting if none are available.
// If it waits longer than timeoutMS, it returns an error to protect system memory.
func (e *JSEngine) getVM() (vm *goja.Runtime, err error) {
	timer := time.NewTimer(e.timeoutMS)
	defer timer.Stop()
	select {
	case vm = <-e.vmChannel:
		return vm, nil
	case <-timer.C:
		return nil, fmt.Errorf("VM pool exhausted: timeout waiting for an available Goja VM (limit exceeded)")
	}
}

func (e *JSEngine) setupBridges(vm *goja.Runtime) {
	// ssrfSafeTransport prevents DNS Rebinding and SSRF during HTTP calls
	ssrfSafeTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.LookupIP(host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isPrivateOrLocalIP(ip) {
					return nil, fmt.Errorf("SSRF prevention: connection to private/local network address %s is blocked", ip.String())
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("failed to resolve host: %s", host)
			}
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	fetchClient := &http.Client{
		Transport: ssrfSafeTransport,
		Timeout:   10 * time.Second,
	}

	getReqID := func() string {
		if val := vm.Get("__current_req_id"); val != nil {
			s := val.String()
			if s != "" && s != "undefined" {
				return s
			}
		}
		return ""
	}

	// Global log function bridge (used by workers)
	vm.Set("log", func(call goja.FunctionCall) goja.Value {
		var msgs []string
		for _, arg := range call.Arguments {
			msgs = append(msgs, arg.String())
		}
		msgStr := strings.Join(msgs, " ")
		fmt.Printf("[WORKER LOG] %s\n", msgStr)
		if reqID := getReqID(); reqID != "" {
			telemetry.AddRequestLog(reqID, "INFO", msgStr)
		}
		telemetry.AddWorkerLog("INFO", msgStr)
		return goja.Undefined()
	})

	// console polyfill bridge for standard console.log/warn/error usage
	console := vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		var msgs []string
		for _, arg := range call.Arguments {
			msgs = append(msgs, arg.String())
		}
		msgStr := strings.Join(msgs, " ")
		fmt.Printf("[WORKER LOG] %s\n", msgStr)
		if reqID := getReqID(); reqID != "" {
			telemetry.AddRequestLog(reqID, "INFO", msgStr)
		}
		telemetry.AddWorkerLog("INFO", msgStr)
		return goja.Undefined()
	})
	console.Set("warn", func(call goja.FunctionCall) goja.Value {
		var msgs []string
		for _, arg := range call.Arguments {
			msgs = append(msgs, arg.String())
		}
		msgStr := strings.Join(msgs, " ")
		fmt.Printf("[WORKER WARN] %s\n", msgStr)
		if reqID := getReqID(); reqID != "" {
			telemetry.AddRequestLog(reqID, "WARN", msgStr)
		}
		telemetry.AddWorkerLog("WARN", msgStr)
		return goja.Undefined()
	})
	console.Set("error", func(call goja.FunctionCall) goja.Value {
		var msgs []string
		for _, arg := range call.Arguments {
			msgs = append(msgs, arg.String())
		}
		msgStr := strings.Join(msgs, " ")
		fmt.Printf("[WORKER ERROR] %s\n", msgStr)
		if reqID := getReqID(); reqID != "" {
			telemetry.AddRequestLog(reqID, "ERROR", msgStr)
		}
		telemetry.AddWorkerLog("ERROR", msgStr)
		return goja.Undefined()
	})
	vm.Set("console", console)
 
	// Secure entropy bridge (crypto/rand) for workers (ex: secure session tokens/UUIDs)
	vm.Set("_goCryptoRand", func(size int) (string, error) {
		if size <= 0 || size > 65536 {
			return "", fmt.Errorf("invalid size request: %d (max 65536)", size)
		}
		bytes := make([]byte, size)
		_, err := io.ReadFull(rand.Reader, bytes)
		if err != nil {
			return "", fmt.Errorf("entropy source failure: %w", err)
		}
		return base64.StdEncoding.EncodeToString(bytes), nil
	})

	// AI inference bridge (used by split inference worker)
	vm.Set("ai", map[string]interface{}{
		"loadModel": func(modelName string, modelJSON string) error {
			var model AIModel
			// Tenta primeiro verificar se é uma URL para baixar o modelo
			if strings.HasPrefix(modelJSON, "http://") || strings.HasPrefix(modelJSON, "https://") {
				req, err := http.NewRequest("GET", modelJSON, nil)
				if err != nil {
					return fmt.Errorf("failed to create request for model: %w", err)
				}
				resp, err := fetchClient.Do(req)
				if err != nil {
					return fmt.Errorf("failed to fetch model from URL: %w", err)
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
				if err != nil {
					return fmt.Errorf("failed to read model body from URL: %w", err)
				}
				if err := json.Unmarshal(data, &model); err != nil {
					return fmt.Errorf("failed to parse fetched model JSON: %w", err)
				}
			} else {
				// Se for um JSON string normal, faz parse direto
				if err := json.Unmarshal([]byte(modelJSON), &model); err != nil {
					return fmt.Errorf("invalid model JSON: %w", err)
				}
			}

			e.aiMu.Lock()
			e.aiModels[modelName] = &model
			e.aiMu.Unlock()
			return nil
		},
		"runLayers": func(modelName string, startLayer int, endLayer int, inputs []interface{}) ([]float64, error) {
			e.aiMu.RLock()
			model, exists := e.aiModels[modelName]
			e.aiMu.RUnlock()
			if !exists {
				return nil, fmt.Errorf("model %q not loaded", modelName)
			}

			// Converte []interface{} para []float64
			floatInputs := make([]float64, len(inputs))
			for i, v := range inputs {
				switch val := v.(type) {
				case float64:
					floatInputs[i] = val
				case int64:
					floatInputs[i] = float64(val)
				case int:
					floatInputs[i] = float64(val)
				default:
					return nil, fmt.Errorf("invalid input type at index %d: %T", i, v)
				}
			}

			current := floatInputs
			for idx := startLayer; idx <= endLayer; idx++ {
				if idx < 0 || idx >= len(model.Layers) {
					return nil, fmt.Errorf("layer index %d out of bounds (layers count: %d)", idx, len(model.Layers))
				}
				layer := model.Layers[idx]
				var err error
				current, err = executeLayer(layer, current)
				if err != nil {
					return nil, fmt.Errorf("failed to execute layer %d (%s): %w", idx, layer.Type, err)
				}
			}

			return current, nil
		},
	})

	vm.Set("jwtVerify", func(token string, secret string) bool {
		if token == "" || secret == "" {
			return false
		}
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			return false
		}
		// Recreate the signing input and verify HMAC-SHA256
		signingInput := parts[0] + "." + parts[1]
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(signingInput))
		expectedSig := mac.Sum(nil)
		// Decode the provided signature (base64url, no padding)
		providedSig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return false
		}
		return hmac.Equal(expectedSig, providedSig)
	})

	// KV Store bridge - shared state between all workers and per-client state
	if e.kvStore != nil {
		vm.Set("kv", map[string]interface{}{
			"get": func(key string) string {
				val := e.kvStore.Get(key)
				if val == nil {
					return ""
				}
				return string(val)
			},
			"set": func(key string, value string, ttlMs ...int) error {
				var err error
				if len(ttlMs) > 0 {
					err = e.kvStore.Set(key, []byte(value), time.Duration(ttlMs[0])*time.Millisecond)
				} else {
					err = e.kvStore.Set(key, []byte(value))
				}
				if err != nil {
					return fmt.Errorf("kv.set(%q) failed: %w", key, err)
				}
				return nil
			},
			"delete": func(key string) error {
				err := e.kvStore.Delete(key)
				if err != nil {
					return fmt.Errorf("kv.delete(%q) failed: %w", key, err)
				}
				return nil
			},
			"exists": func(key string) bool {
				return e.kvStore.Exists(key)
			},
			"keys": func() []string {
				return e.kvStore.Keys()
			},
			"getClient": func(clientID, key string) string {
				val := e.kvStore.GetClient(clientID, key)
				if val == nil {
					return ""
				}
				return string(val)
			},
			"setClient": func(clientID, key string, value string, ttlMs ...int) error {
				var err error
				if len(ttlMs) > 0 {
					err = e.kvStore.SetClient(clientID, key, []byte(value), time.Duration(ttlMs[0])*time.Millisecond)
				} else {
					err = e.kvStore.SetClient(clientID, key, []byte(value))
				}
				if err != nil {
					return fmt.Errorf("kv.setClient(%q, %q) failed: %w", clientID, key, err)
				}
				return nil
			},
			"deleteClient": func(clientID, key string) error {
				err := e.kvStore.DeleteClient(clientID, key)
				if err != nil {
					return fmt.Errorf("kv.deleteClient(%q, %q) failed: %w", clientID, key, err)
				}
				return nil
			},
			"existsClient": func(clientID, key string) bool {
				return e.kvStore.ExistsClient(clientID, key)
			},
			"keysClient": func(clientID string) []string {
				return e.kvStore.KeysClient(clientID)
			},
		})
	}

	// Neural Cache bridge - sistema de cache inteligente que aprende com o tráfego
	e.ncMu.RLock()
	nc := e.neuralCache
	e.ncMu.RUnlock()
	if nc != nil {
		vm.Set("neuralCache", map[string]interface{}{
			"stats": func() map[string]interface{} {
				return nc.Stats()
			},
			"clear": func() {
				nc.Clear()
			},
			"cleanupExpired": func() {
				nc.CleanupExpired()
			},
		})
	}

	// require() loads JS modules from the ./modules/ directory (with cache).
	// Modules should set module.exports = { ... }.
	// Usage in JS: var uuid = require("uuid");
	vm.Set("require", func(modulePath string) (interface{}, error) {
		e.moduleMu.RLock()
		cached, ok := e.moduleCache[modulePath]
		e.moduleMu.RUnlock()
		if ok {
			return cached, nil
		}
		moduleDir := filepath.Join(".", "modules")
		if !strings.HasSuffix(modulePath, ".js") {
			modulePath = modulePath + ".js"
		}
		fullPath := filepath.Join(moduleDir, filepath.Clean(modulePath))
		realModuleDir, errEval := filepath.EvalSymlinks(moduleDir)
		if errEval != nil {
			realModuleDir = moduleDir
		}
		absModuleDir, errAbs := filepath.Abs(realModuleDir)
		if errAbs != nil {
			return nil, fmt.Errorf("module dir resolution failed: %w", errAbs)
		}
		realFullPath, errEval := filepath.EvalSymlinks(fullPath)
		if errEval != nil {
			realFullPath = fullPath
		}
		absFullPath, errAbs := filepath.Abs(realFullPath)
		if errAbs != nil {
			return nil, fmt.Errorf("module path resolution failed: %w", errAbs)
		}
		if !strings.HasPrefix(absFullPath, absModuleDir) {
			return nil, fmt.Errorf("module path traversal denied: %s", modulePath)
		}
		code, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("module not found: %s", modulePath)
		}
		// Avalia o módulo numa VM temporária isolada para não poluir o worker
		modVM := goja.New()
		modVM.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
		modVM.RunString("var __mod = {exports:{}};")
		_, err = modVM.RunString(string(code))
		if err != nil {
			return nil, fmt.Errorf("module error %s: %v", modulePath, err)
		}
		expVal := modVM.Get("__mod")
		if expVal == nil {
			return nil, nil
		}
		obj := expVal.ToObject(modVM)
		exports := obj.Get("exports")
		if exports == nil {
			return nil, nil
		}
		result := exports.Export()
		e.moduleMu.Lock()
		if len(e.moduleCache) >= maxModuleCacheSize {
			for k := range e.moduleCache {
				delete(e.moduleCache, k)
				break
			}
		}
		e.moduleCache[modulePath] = result
		e.moduleMu.Unlock()
		return result, nil
	})

	// _goFetch is the native Go HTTP client exposed as fetch() in JS.
	// Configured with custom DialContext to prevent SSRF and DNS Rebinding.
	vm.Set("_goFetch", func(method, urlStr, headersJSON, bodyStr string) map[string]interface{} {
		failRes := map[string]interface{}{
			"status":  502,
			"headers": map[string]string{"content-type": "text/plain"},
			"body":    "Bad Gateway: outbound fetch failed",
		}

		client := fetchClient

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

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
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

// Call executes a specific function in the JS environment with the given arguments.
// This allows for extreme versatility, as the engine can call any custom function
// defined by the user in their worker scripts.
func (e *JSEngine) Call(funcName string, args ...interface{}) (goja.Value, error) {
	vm, err := e.getVM()
	if err != nil {
		return nil, err
	}

	var execErr error
	var resultVal goja.Value
	var interrupted int32
	vmPoisoned := false
	defer func() {
		if vmPoisoned || atomic.LoadInt32(&interrupted) != 0 {
			e.recreateAndQueueVM()
		} else {
			e.vmChannel <- vm
		}
	}()

	done := make(chan struct{})
	timer := time.NewTimer(e.timeoutMS)
	defer timer.Stop()

	var timerWg sync.WaitGroup
	timerWg.Add(1)
	go func() {
		defer timerWg.Done()
		select {
		case <-timer.C:
			atomic.StoreInt32(&interrupted, 1)
			vm.Interrupt(ErrExecutionTimeout)
		case <-done:
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				vmPoisoned = true
				if err, ok := r.(error); ok && errors.Is(err, ErrExecutionTimeout) {
					execErr = ErrExecutionTimeout
				} else {
					execErr = fmt.Errorf("panic in JS execution: %v", r)
				}
			}
			close(done)
		}()

		fnVal := vm.Get(funcName)
		if fnVal == nil || goja.IsUndefined(fnVal) {
			execErr = fmt.Errorf("function %s not found", funcName)
			return
		}

		fn, ok := goja.AssertFunction(fnVal)
		if !ok {
			execErr = fmt.Errorf("%s is not a valid function", funcName)
			return
		}

		jsArgs := make([]goja.Value, len(args))
		for i, arg := range args {
			jsArgs[i] = vm.ToValue(arg)
		}

		res, err := fn(goja.Undefined(), jsArgs...)
		if err != nil {
			execErr = err
			return
		}
		resultVal = res
	}()

	timerWg.Wait()

	return resultVal, execErr
}

// RunOnTraffic executes the user's onTraffic function with a Request and
// returns the modified TrafficResult. Supports CPU watchdog timeout and panic recovery.
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
	vm.Set("__current_req_id", req.ID)
	defer vm.Set("__current_req_id", goja.Undefined())

	var execErr error
	var resultVal goja.Value
	var interrupted int32
	vmPoisoned := false
	defer func() {
		if vmPoisoned || atomic.LoadInt32(&interrupted) != 0 {
			e.recreateAndQueueVM()
		} else {
			e.vmChannel <- vm
		}
	}()

	done := make(chan struct{})
	timer := time.NewTimer(e.timeoutMS)
	defer timer.Stop()

	var timerWg sync.WaitGroup
	timerWg.Add(1)
	go func() {
		defer timerWg.Done()
		select {
		case <-timer.C:
			atomic.StoreInt32(&interrupted, 1)
			vm.Interrupt(ErrExecutionTimeout)
		case <-done:
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				vmPoisoned = true
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

	timerWg.Wait()

	if execErr != nil {
		return nil, execErr
	}

	// null/undefined from JS means "preserve original request"
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

	// Normal completion: export the TrafficResult before returning VM to pool
	var result TrafficResult
	func() {
		defer func() {
			if r := recover(); r != nil {
				vmPoisoned = true
				if err2, ok := r.(error); ok && errors.Is(err2, ErrExecutionTimeout) {
					vmPoisoned = true
					execErr = ErrExecutionTimeout
				} else {
					execErr = fmt.Errorf("panic na exportacao JS: %v", r)
				}
			}
		}()
		err = vm.ExportTo(resultVal, &result)
	}()
	vmPoisoned = vmPoisoned || atomic.LoadInt32(&interrupted) != 0
	if execErr != nil {
		return nil, execErr
	}
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
	vm.Set("__current_req_id", resp.ID)
	defer vm.Set("__current_req_id", goja.Undefined())

	var execErr error
	var resultVal goja.Value
	var interrupted int32
	vmPoisoned := false
	defer func() {
		if vmPoisoned || atomic.LoadInt32(&interrupted) != 0 {
			e.recreateAndQueueVM()
		} else {
			e.vmChannel <- vm
		}
	}()

	done := make(chan struct{})
	timer := time.NewTimer(e.timeoutMS)
	defer timer.Stop()

	var timerWg sync.WaitGroup
	timerWg.Add(1)
	go func() {
		defer timerWg.Done()
		select {
		case <-timer.C:
			atomic.StoreInt32(&interrupted, 1)
			vm.Interrupt(ErrExecutionTimeout)
		case <-done:
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				vmPoisoned = true
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

	timerWg.Wait()

	if execErr != nil {
		return nil, execErr
	}

	if resultVal == nil || goja.IsUndefined(resultVal) || goja.IsNull(resultVal) {
		return resp, nil
	}

	var modifiedResp Response
	func() {
		defer func() {
			if r := recover(); r != nil {
				vmPoisoned = true
				if err2, ok := r.(error); ok && errors.Is(err2, ErrExecutionTimeout) {
					vmPoisoned = true
					execErr = ErrExecutionTimeout
				} else {
					execErr = fmt.Errorf("panic na exportacao JS: %v", r)
				}
			}
		}()
		err = vm.ExportTo(resultVal, &modifiedResp)
	}()
	vmPoisoned = vmPoisoned || atomic.LoadInt32(&interrupted) != 0
	if execErr != nil {
		return nil, execErr
	}
	if err != nil {
		return nil, fmt.Errorf("erro ao mapear retorno do JS para Response: %w", err)
	}

	return &modifiedResp, nil
}

func executeLayer(layer AILayer, inputs []float64) ([]float64, error) {
	switch layer.Type {
	case "dense":
		if len(layer.Weights) == 0 {
			return nil, fmt.Errorf("dense layer has no weights")
		}
		if len(inputs) != len(layer.Weights[0]) {
			return nil, fmt.Errorf("input dimension mismatch: got %d, expected %d", len(inputs), len(layer.Weights[0]))
		}
		if len(layer.Biases) != len(layer.Weights) {
			return nil, fmt.Errorf("bias dimension mismatch: got %d, expected %d", len(layer.Biases), len(layer.Weights))
		}
		outputs := make([]float64, len(layer.Weights))
		for j := 0; j < len(layer.Weights); j++ {
			sum := 0.0
			for i := 0; i < len(inputs); i++ {
				sum += inputs[i] * layer.Weights[j][i]
			}
			outputs[j] = sum + layer.Biases[j]
		}
		return outputs, nil

	case "relu":
		outputs := make([]float64, len(inputs))
		for i, x := range inputs {
			if x > 0 {
				outputs[i] = x
			} else {
				outputs[i] = 0
			}
		}
		return outputs, nil

	case "softmax":
		if len(inputs) == 0 {
			return inputs, nil
		}
		outputs := make([]float64, len(inputs))
		maxVal := inputs[0]
		for _, x := range inputs {
			if x > maxVal {
				maxVal = x
			}
		}
		sum := 0.0
		for i, x := range inputs {
			outputs[i] = math.Exp(x - maxVal)
			sum += outputs[i]
		}
		for i := range outputs {
			outputs[i] /= sum
		}
		return outputs, nil

	default:
		return nil, fmt.Errorf("unsupported AI layer type: %s", layer.Type)
	}
}

func isPrivateOrLocalIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// Redes privadas IPv4 (RFC 1918 e RFCs de redes especiais/reservadas)
	if ip4 := ip.To4(); ip4 != nil {
		// 10.0.0.0/8
		if ip4[0] == 10 {
			return true
		}
		// 172.16.0.0/12
		if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return true
		}
		// 192.168.0.0/16
		if ip4[0] == 192 && ip4[1] == 168 {
			return true
		}
		// AWS/GCP/OpenStack Metadata IP (169.254.169.254)
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		// Carrier-Grade NAT (100.64.0.0/10 - RFC 6598)
		if ip4[0] == 100 && (ip4[1] >= 64 && ip4[1] <= 127) {
			return true
		}
		// IETF Protocol Assignments (192.0.0.0/24 - RFC 6890)
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
		// TEST-NET-1 (192.0.2.0/24 - RFC 5737)
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2 {
			return true
		}
		// Network Interconnect Device Benchmark Testing (198.18.0.0/15 - RFC 2544)
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			return true
		}
		// TEST-NET-2 (198.51.100.0/22 - RFC 5737)
		if ip4[0] == 198 && ip4[1] == 51 && (ip4[2] >= 100 && ip4[2] <= 103) {
			return true
		}
		// TEST-NET-3 (203.0.113.0/24 - RFC 5737)
		if ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 {
			return true
		}
		// Class E / Reserved (240.0.0.0/4 - RFC 1112)
		if (ip4[0] & 0xf0) == 240 {
			return true
		}
	} else {
		// IPv6 redes locais/ULA (fc00::/7)
		if len(ip) >= 2 && (ip[0]&0xfe) == 0xfc {
			return true
		}
	}
	return false
}
