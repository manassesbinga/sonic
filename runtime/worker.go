package runtime

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manassesbinga/sonic/runtime/builder"
	"github.com/manassesbinga/sonic/security"
	"github.com/manassesbinga/sonic/telemetry"
)

// WorkerType represents the type of worker runtime.
type WorkerType string

const (
	WorkerTypeJavaScript WorkerType = "javascript" // .js files
	WorkerTypeWebAssembly WorkerType = "webassembly" // .wasm files
	WorkerTypeNative WorkerType = "native" // Executable files (no extension or .exe on Windows)
	WorkerTypeManager WorkerType = "manager" // Manager holding multiple worker engines
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
	mu         sync.RWMutex            // Protects engines map
	engines    map[string]WorkerEngine // filepath -> WorkerEngine
	cveScanner *security.CVEStreamScanner
}

// NewWorkerManager creates a new WorkerManager with the given KV Store.
func NewWorkerManager(kvStore *KVStore) *WorkerManager {
	scanner := security.NewCVEStreamScanner(func(event security.SecurityEvent, details map[string]interface{}) {
		if event == security.EventCVEDetected {
			sig, _ := details["signature"].(string)
			prev, _ := details["preview"].(string)
			security.AddSecurityIncident(sig, prev)
		}
	})
	return &WorkerManager{
		kvStore:    kvStore,
		engines:    make(map[string]WorkerEngine),
		cveScanner: scanner,
	}
}

// LoadWorker loads a worker from a file, detecting its type automatically.
func (wm *WorkerManager) LoadWorker(cfg WorkerConfig) error {
	ext := strings.ToLower(filepath.Ext(cfg.FilePath))

	// Se for código fonte (.rs, .go, .c), compila para WASM dinamicamente primeiro
	if ext == ".rs" || ext == ".go" || ext == ".c" {
		b := builder.NewBuilder()
		res, err := b.Build(cfg.FilePath)
		if err != nil {
			return fmt.Errorf("failed to build source worker %s: %w", cfg.FilePath, err)
		}
		if !res.Success {
			return fmt.Errorf("wasm build failed: %w", res.Error)
		}
		cfg.FilePath = res.WASMFile
		ext = ".wasm"
	}
	
	var engine WorkerEngine
	var errEngine error

	switch ext {
	case ".js":
		jsCode, err := os.ReadFile(cfg.FilePath)
		if err != nil {
			return fmt.Errorf("failed to read JS file: %w", err)
		}
		jsEng, errJs := NewJSEngineWithKV(string(jsCode), cfg.TimeoutMS, cfg.PoolSize, cfg.KVStore)
		engine = jsEng
		errEngine = errJs
		if errEngine != nil {
			return fmt.Errorf("failed to create JS engine: %w", errEngine)
		}

	case ".wasm":
		engine, errEngine = NewWasmEngine(cfg.FilePath, cfg.TimeoutMS, cfg.PoolSize, cfg.KVStore)
		if errEngine != nil {
			return fmt.Errorf("failed to create WASM engine: %w", errEngine)
		}

	case ".py", ".sh", ".rb", ".pl":
		engine, errEngine = NewNativeEngine(cfg.FilePath, cfg.TimeoutMS, cfg.PoolSize, cfg.KVStore)
		if errEngine != nil {
			return fmt.Errorf("failed to create native engine: %w", errEngine)
		}

	default:
		// Check if it's an executable file
		info, err := os.Stat(cfg.FilePath)
		if err != nil {
			return fmt.Errorf("failed to stat file: %w", err)
		}
		if info.Mode().Perm()&0111 != 0 || ext == ".exe" {
			engine, errEngine = NewNativeEngine(cfg.FilePath, cfg.TimeoutMS, cfg.PoolSize, cfg.KVStore)
			if errEngine != nil {
				return fmt.Errorf("failed to create native engine: %w", errEngine)
			}
		} else {
			return fmt.Errorf("unknown worker type for file: %s", cfg.FilePath)
		}
	}

	wm.mu.Lock()
	wm.engines[cfg.FilePath] = engine
	wm.mu.Unlock()
	return nil
}

// LoadAllWorkers loads all workers from a directory.
func (wm *WorkerManager) LoadAllWorkers(dir string, timeoutMS int, poolSize int) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	var errs []error
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
			errs = append(errs, fmt.Errorf("%s: %w", filePath, err))
			continue
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to load %d worker(s): %v", len(errs), errs)
	}
	return nil
}

// ProcessPacket passes a packet through all loaded workers in alphabetical order, or routes through configured visual pipelines if matched.
func (wm *WorkerManager) ProcessPacket(packet *Packet) (*PacketResult, error) {
	// 1. Verificar se existe algum pipeline que corresponda ao pacote (se for HTTP)
	if packet.Request != nil {
		pipelines, err := GetPipelines(wm.kvStore)
		if err == nil {
			// Ordena os pipelines por prioridade decrescente (maior prioridade executa primeiro)
			sort.Slice(pipelines, func(i, j int) bool {
				return pipelines[i].Priority > pipelines[j].Priority
			})

			var matchedPipeline *Pipeline
			for _, p := range pipelines {
				if p.Match(packet.Request) {
					matchedPipeline = &p
					break
				}
			}

			// Se houver um pipeline correspondente, executa as etapas configuradas visualmente
			if matchedPipeline != nil {
				currentResult := &PacketResult{Allow: true, Packet: packet}
				var lastErr error

				for _, step := range matchedPipeline.Steps {
					if !step.Enabled {
						continue
					}

					// Se um passo anterior gerou uma resposta direta (ex: 403, 429), interrompe o pipeline
					if currentResult.Response != nil {
						return currentResult, nil
					}

					// Determinar o pacote a ser processado neste passo (usando o modificado pelo passo anterior)
					var p *Packet
					if currentResult.Packet != nil {
						p = currentResult.Packet
					} else {
						p = packet
					}

					if p.ID == "" {
						p.ID = fmt.Sprintf("req-%d", time.Now().UnixNano())
					}

					switch step.Type {
					case StepTypeCVEScanner:
						var bodyText []byte
						if p.Request != nil {
							bodyText = []byte(p.Request.Body)
						} else if p.Response != nil {
							bodyText = []byte(p.Response.Body)
						}

						if len(bodyText) > 0 {
							if matched, sig := wm.cveScanner.Scan(bodyText); matched {
								blockedResp := &Response{
									Status:  403,
									Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
									Body:    "Forbidden: Security rule violation (CVE: " + sig + ")",
								}
								// Salvar log de bloqueio de CVE no SQLite
								QueueAuditLog(
									p.ID, time.Now(), "cve_scanner", "Scan", "0.5ms", "blocked",
									"Security rule violation (CVE: "+sig+")",
									p.Request.Method, p.Request.URL, p.Request.Headers, p.Request.Body,
									403, blockedResp.Headers, blockedResp.Body, nil,
								)
								return &PacketResult{
									Allow:    false,
									Response: blockedResp,
								}, nil
							}
						}

					case StepTypeRateLimiter:
						clientIP := "127.0.0.1"
						if p.Request != nil && p.Request.ClientAddr != "" {
							clientIP = p.Request.ClientAddr
						}
						if host, _, errSplit := net.SplitHostPort(clientIP); errSplit == nil {
							clientIP = host
						}

						if !ExecuteRateLimiter(wm.kvStore, clientIP, matchedPipeline.ID, step.Limit, step.Window) {
							blockedResp := &Response{
								Status:  429,
								Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8", "Retry-After": "60"},
								Body:    "Too Many Requests: Rate limit exceeded (Visual Pipeline Shield)",
							}
							// Salvar log de rate limiting no SQLite
							QueueAuditLog(
								p.ID, time.Now(), "rate_limiter", "LimitCheck", "0.2ms", "blocked",
								"Rate limit exceeded (limit: "+fmt.Sprintf("%d", step.Limit)+")",
								p.Request.Method, p.Request.URL, p.Request.Headers, p.Request.Body,
								429, blockedResp.Headers, blockedResp.Body, nil,
							)
							return &PacketResult{
								Allow:    false,
								Response: blockedResp,
							}, nil
						}

					case StepTypeWorker:
						// Localizar o worker correspondente na lista
						var targetEngine WorkerEngine
						var targetPath string
						wm.mu.RLock()
						for engPath, eng := range wm.engines {
							if filepath.Base(engPath) == step.Name {
								targetEngine = eng
								targetPath = engPath
								break
							}
						}
						wm.mu.RUnlock()

						funcName := "onTraffic"
						if packet.Response != nil {
							funcName = "onResponse"
						}

						method := ""
						url := ""
						if packet.Request != nil {
							method = packet.Request.Method
							url = packet.Request.URL
						} else if packet.Response != nil {
							method = "HTTP Response"
							url = fmt.Sprintf("Status %d", packet.Response.Status)
						}

						if targetEngine == nil {
							// Registrar erro de worker ausente na auditoria persistente
							QueueAuditLog(
								p.ID, time.Now(), step.Name, funcName, "0ms", "error",
								"Worker file '"+step.Name+"' was not found in functions directory",
								method, url, nil, "", 500, nil, "", nil,
							)
							continue
						}

						res, errEngine := wm.executeEngineWithTelemetry(p, targetEngine, targetPath, funcName)
						if errEngine != nil {
							lastErr = errEngine
							continue
						}

						if res != nil {
							currentResult = res
						}
					}
				}

				return currentResult, lastErr
			}
		}
	}

	// 2. Comportamento padrão (fallback): Executa todos os workers carregados em ordem alfabética
	wm.mu.RLock()
	enginesCount := len(wm.engines)
	var paths []string
	if enginesCount > 0 {
		paths = make([]string, 0, enginesCount)
		for path := range wm.engines {
			paths = append(paths, path)
		}
	}
	wm.mu.RUnlock()

	if enginesCount == 0 {
		return &PacketResult{Allow: true, Packet: packet}, nil
	}

	sort.Strings(paths)

	var currentResult *PacketResult
	var lastErr error

	for _, path := range paths {
		wm.mu.RLock()
		engine, exists := wm.engines[path]
		wm.mu.RUnlock()
		if !exists {
			continue
		}

		var p *Packet
		if currentResult == nil {
			p = packet
		} else {
			if currentResult.Response != nil {
				return currentResult, nil
			}
			if currentResult.Packet != nil {
				p = currentResult.Packet
			} else {
				p = packet
			}
		}

		funcName := "onTraffic"
		if packet.Response != nil {
			funcName = "onResponse"
		}

		res, err := wm.executeEngineWithTelemetry(p, engine, path, funcName)
		if err != nil {
			lastErr = err
			continue
		}

		if res != nil {
			currentResult = res
		}
	}

	if currentResult == nil {
		return &PacketResult{Allow: true, Packet: packet}, lastErr
	}

	return currentResult, lastErr
}

// executeEngineWithTelemetry executa um worker de forma thread-safe e centraliza a recolha de telemetria e logs de auditoria.
func (wm *WorkerManager) executeEngineWithTelemetry(
	p *Packet,
	engine WorkerEngine,
	path string,
	funcName string,
) (*PacketResult, error) {
	method := ""
	url := ""
	if p.Request != nil {
		method = p.Request.Method
		url = p.Request.URL
	} else if p.Response != nil {
		method = "HTTP Response"
		url = fmt.Sprintf("Status %d", p.Response.Status)
	}

	var reqHeaders map[string]string
	var reqBody string
	if p.Request != nil {
		reqHeaders = p.Request.Headers
		reqBody = p.Request.Body
	}

	var logBuffer telemetry.SafeLogBuffer
	telemetry.SetRequestLogBuffer(p.ID, &logBuffer)

	startExec := time.Now()
	res, err := engine.ProcessPacket(p)
	duration := time.Since(startExec)

	telemetry.ClearRequestLogBuffer(p.ID)

	respStatus := 0
	var respHeaders map[string]string
	var respBody string
	if res != nil && res.Response != nil {
		respStatus = res.Response.Status
		respHeaders = res.Response.Headers
		respBody = res.Response.Body
	} else if p.Response != nil {
		respStatus = p.Response.Status
		respHeaders = p.Response.Headers
		respBody = p.Response.Body
	}

	status := "success"
	errMsg := ""
	if err != nil {
		status = "error"
		errMsg = err.Error()
	}

	logs := logBuffer.GetLogs()

	// Telemetria em memória
	telemetry.AddFunctionExecution(telemetry.FunctionExecution{
		ID:          p.ID,
		Timestamp:   startExec,
		Worker:      filepath.Base(path),
		Function:    funcName,
		Duration:    fmt.Sprintf("%.2fms", float64(duration.Microseconds())/1000.0),
		Status:      status,
		ErrorMsg:    errMsg,
		Method:      method,
		URL:         url,
		Headers:     reqHeaders,
		Body:        reqBody,
		RespStatus:  respStatus,
		RespHeaders: respHeaders,
		RespBody:    respBody,
		Logs:        logs,
	})

	// Auditoria persistente SQLite
	QueueAuditLog(
		p.ID, startExec, filepath.Base(path), funcName,
		fmt.Sprintf("%.2fms", float64(duration.Microseconds())/1000.0),
		status, errMsg, method, url, reqHeaders, reqBody, respStatus, respHeaders, respBody, logs,
	)

	return res, err
}

// Type returns the type of this worker engine.
func (wm *WorkerManager) Type() WorkerType {
	return WorkerTypeManager
}

// Close closes all managed worker engines.
func (wm *WorkerManager) Close() error {
	var errs []error
	wm.mu.Lock()
	enginesCopy := make(map[string]WorkerEngine, len(wm.engines))
	for path, engine := range wm.engines {
		enginesCopy[path] = engine
	}
	wm.engines = make(map[string]WorkerEngine) // Esvazia o mapa para prevenir uso concorrente/posterior
	wm.mu.Unlock()

	for path, engine := range enginesCopy {
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
