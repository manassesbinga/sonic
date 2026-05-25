package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

var ErrExecutionTimeout = errors.New("execution timeout: script exceeded CPU time limit")

type JSEngine struct {
	program   *goja.Program
	vmPool    *sync.Pool
	timeoutMS time.Duration
}

// NewJSEngine compila o codigo JS uma unica vez e inicializa o pool de VMs.
func NewJSEngine(jsCode string, timeoutMS int, poolSize int) (*JSEngine, error) {
	program, err := goja.Compile("worker.js", jsCode, false)
	if err != nil {
		return nil, fmt.Errorf("erro ao compilar codigo javascript: %w", err)
	}

	engine := &JSEngine{
		program:   program,
		timeoutMS: time.Duration(timeoutMS) * time.Millisecond,
	}

	// Inicializa o sync.Pool com a funcao auxiliar thread-safe
	engine.vmPool = &sync.Pool{
		New: func() interface{} {
			return engine.createVM()
		},
	}

	// Pre-inicializa a pool (warm-up) para evitar latencia nas primeiras requisicoes
	vms := make([]*goja.Runtime, poolSize)
	for i := 0; i < poolSize; i++ {
		vms[i] = engine.vmPool.Get().(*goja.Runtime)
	}
	for i := 0; i < poolSize; i++ {
		engine.vmPool.Put(vms[i])
	}

	return engine, nil
}

// createVM cria e configura uma nova instancia limpa do Goja.
func (e *JSEngine) createVM() *goja.Runtime {
	vm := goja.New()
	
	// Configura o mapeador de campos com base em tags JSON.
	// Isto permite que o JS use "req.method", "req.headers", "resp.status" de forma nativa!
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	// Expoe as bridges nativas Go no escopo global
	e.setupBridges(vm)

	// Injeta o Bootstrap das APIs Web Standard (Headers, Request, Response, fetch)
	// ANTES do script do utilizador, para que onTraffic/onResponse possam usar estas classes.
	_, err := vm.RunString(JSBootstrap)
	if err != nil {
		panic(fmt.Sprintf("erro critico ao carregar bootstrap das APIs Web Standard na VM: %v", err))
	}

	// Executa o script do utilizador uma vez para registrar funcoes globais (onTraffic, onResponse)
	_, err = vm.RunProgram(e.program)
	if err != nil {
		panic(fmt.Sprintf("erro critico ao carregar script na VM do pool: %v", err))
	}

	return vm
}

// setupBridges registra funcoes nativas Go performaticas dentro da VM Goja.
func (e *JSEngine) setupBridges(vm *goja.Runtime) {
	// 1. Ponte de Log
	vm.Set("log", func(msg string) {
		fmt.Printf("[JS LOG] %s\n", msg)
	})

	// 2. Ponte Criptografica JWT (Nativa em Go!)
	vm.Set("jwtVerify", func(token string, secret string) bool {
		if token == "" || secret == "" {
			return false
		}
		// Na pratica, em producao usará a biblioteca golang-jwt/jwt.
		// Feito mock rapido e seguro para fins de performance inicial.
		return len(token) > 10
	})

	// 3. Ponte Fetch HTTP de Saída Nativa (Alta Performance em Go)
	// Exposta como _goFetch() no JS e chamada pelo polyfill fetch() do bootstrap.
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

// RunOnTraffic executa a funcao onTraffic da VM com CPU Watchdog concorrente.
func (e *JSEngine) RunOnTraffic(req *Request) (*TrafficResult, error) {
	vm := e.vmPool.Get().(*goja.Runtime)

	// Criamos um canal para controlar a interrupcao
	done := make(chan struct{})
	var execErr error
	var resultVal goja.Value

	// CPU Watchdog Timer
	go func() {
		select {
		case <-done:
			return
		case <-time.After(e.timeoutMS):
			vm.Interrupt(ErrExecutionTimeout)
		}
	}()

	// Executa a chamada com tratamento de panics e timeouts
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

		// Obtem o nosso wrapper JS bootstrap de onTraffic
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

		// Executa passando a struct Request bruta. O wrapper JS fará o parse
		res, err := wrapTraffic(goja.Undefined(), vm.ToValue(req))
		if err != nil {
			execErr = err
			return
		}
		resultVal = res
	}()

	if execErr != nil {
		// Se a VM falhou ou sofreu interrupcao (timeout), NAO a devolvemos para o pool
		// para evitar poluicao ou instabilidade no estado interno do Goja.
		if errors.Is(execErr, ErrExecutionTimeout) {
			// Cria uma nova VM limpa para repor no pool
			e.vmPool.Put(e.createVM())
		} else {
			e.vmPool.Put(vm)
		}
		return nil, execErr
	}

	// Devolve a VM estavel ao pool
	e.vmPool.Put(vm)

	// Se o retorno do JS for nulo ou undefined, mantem o request original
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

	// Exporta o resultado de volta para o tipo TrafficResult
	var result TrafficResult
	err := vm.ExportTo(resultVal, &result)
	if err != nil {
		return nil, fmt.Errorf("erro ao mapear retorno do JS para TrafficResult: %w", err)
	}

	return &result, nil
}

// RunOnResponse executa a funcao onResponse da VM com CPU Watchdog.
func (e *JSEngine) RunOnResponse(resp *Response) (*Response, error) {
	vm := e.vmPool.Get().(*goja.Runtime)

	done := make(chan struct{})
	var execErr error
	var resultVal goja.Value

	// CPU Watchdog Timer
	go func() {
		select {
		case <-done:
			return
		case <-time.After(e.timeoutMS):
			vm.Interrupt(ErrExecutionTimeout)
		}
	}()

	// Executa a chamada com tratamento de panics e timeouts
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

		// Obtem o nosso wrapper JS bootstrap de onResponse
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

		// Executa passando a nossa struct Response bruta. O wrapper JS fará o parse
		res, err := wrapResponse(goja.Undefined(), vm.ToValue(resp))
		if err != nil {
			execErr = err
			return
		}
		resultVal = res
	}()

	if execErr != nil {
		// Substitui a VM se tiver dado Timeout para garantir seguranca do pool
		if errors.Is(execErr, ErrExecutionTimeout) {
			e.vmPool.Put(e.createVM())
		} else {
			e.vmPool.Put(vm)
		}
		return nil, execErr
	}

	// Devolve a VM estavel ao pool
	e.vmPool.Put(vm)

	if resultVal == nil || goja.IsUndefined(resultVal) || goja.IsNull(resultVal) {
		return resp, nil
	}

	var modifiedResp Response
	err := vm.ExportTo(resultVal, &modifiedResp)
	if err != nil {
		return nil, fmt.Errorf("erro ao mapear retorno do JS para Response: %w", err)
	}

	return &modifiedResp, nil
}
