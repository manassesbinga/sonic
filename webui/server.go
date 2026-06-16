package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/ebpf"
	"github.com/manassesbinga/sonic/mitm"
	"github.com/manassesbinga/sonic/runtime"
	"github.com/manassesbinga/sonic/security"
	"github.com/manassesbinga/sonic/telemetry"
)

//go:embed frontend
var embeddedFS embed.FS

var webuiFS fs.FS

var startTime time.Time

func init() {
	startTime = time.Now()
	var err error
	webuiFS, err = fs.Sub(embeddedFS, "frontend")
	if err != nil {
		panic(err)
	}
	startAuthLimiterCleanup()
}

// AdminServer representa o servidor administrativo seguro da Web UI.
type AdminServer struct {
	cfg           *config.Config
	server        *http.Server
	token         string
	port          int
	functions     string
	kvStore       *runtime.KVStore
	workerManager *runtime.WorkerManager
	sandbox       *SandboxCoordinator
	aiGateway     *AIGateway
	tlsEnabled    bool
	tlsCert       *tls.Certificate
	devMode       bool
}

// NewAdminServer cria uma nova instância do AdminServer.
func NewAdminServer(cfg *config.Config) (*AdminServer, error) {
	token := cfg.WebUI.Token
	if token == "" {
		// Tentar ler da variável de ambiente
		token = os.Getenv("SONIC_WEBUI_TOKEN")
	}

	if token == "" {
		// Gerar um token criptograficamente seguro e impossível de prever
		generated, err := generateSecureToken()
		if err != nil {
			return nil, fmt.Errorf("falha ao gerar token de seguranca: %w", err)
		}
		token = generated
	}

	return &AdminServer{
		cfg:       cfg,
		token:     token,
		port:      cfg.WebUI.Port,
		functions: "./functions",
	}, nil
}

// SetKVStore injeta a instância do KVStore no AdminServer e inicializa o Sandbox e o AI Gateway.
func (s *AdminServer) SetKVStore(kv *runtime.KVStore) {
	s.kvStore = kv
	s.sandbox = NewSandboxCoordinator(kv)
	s.aiGateway = NewAIGateway(kv, s.token)
}

// SetCA configura a CA raiz para habilitar HTTPS na WebUI.
// Se enabled for true, um certificado servidor será gerado para o bind address.
func (s *AdminServer) SetCA(ca *mitm.CA, enabled bool) error {
	s.tlsEnabled = enabled
	if !enabled || ca == nil {
		return nil
	}

	host := fmt.Sprintf("localhost")
	// Tenta resolver o bind address para gerar um certificado adequado
	bindAddr := fmt.Sprintf(":%d", s.port)
	if addr, err := net.ResolveTCPAddr("tcp", bindAddr); err == nil && addr.IP != nil && !addr.IP.IsUnspecified() {
		host = addr.IP.String()
	}

	cert, err := ca.GenerateServerCert(host)
	if err != nil {
		return fmt.Errorf("falha ao gerar certificado TLS para WebUI: %w", err)
	}
	s.tlsCert = cert
	return nil
}

// SetWorkerEngine injeta a instância do WorkerManager no AdminServer.
func (s *AdminServer) SetWorkerEngine(wm *runtime.WorkerManager) {
	s.workerManager = wm
}

// Token retorna o token de acesso administrativo atual.
func (s *AdminServer) Token() string {
	return s.token
}

// Sandbox retorna o coordenador do Sandbox de tráfego.
func (s *AdminServer) Sandbox() *SandboxCoordinator {
	return s.sandbox
}

// Start inicia o servidor Web UI em segundo plano.
func (s *AdminServer) Start() error {
	mux := http.NewServeMux()

	// Handler principal que serve o frontend
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		
		filePath := strings.TrimPrefix(path, "/")
		content, err := fs.ReadFile(webuiFS, filePath)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		contentType := "text/html; charset=utf-8"
		if strings.HasSuffix(filePath, ".js") {
			contentType = "application/javascript"
		} else if strings.HasSuffix(filePath, ".css") {
			contentType = "text/css"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; script-src 'self' 'unsafe-inline' 'unsafe-eval'; img-src 'self' data:; connect-src 'self' ws: wss:;")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Write(content)
	})

	// APIs REST
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/status", s.handleStatus)
	apiMux.HandleFunc("/api/workers", s.handleWorkers)
	apiMux.HandleFunc("/api/cves", s.handleCVEs)
	apiMux.HandleFunc("/api/executions", s.handleExecutions)
	apiMux.HandleFunc("/api/logs", s.handleLogs)
	apiMux.HandleFunc("/api/shutdown", s.handleShutdown)
	apiMux.Handle("/api/metrics", promhttp.Handler())

	// APIs REST e SSE para Pipeline, Sandbox, AI Gateway e Auditoria
	apiMux.HandleFunc("/api/pipelines", s.handlePipelines)
	apiMux.HandleFunc("/api/pipelines/test", s.handlePipelinesTest)
	apiMux.HandleFunc("/api/sandbox/config", s.handleSandboxConfig)
	apiMux.HandleFunc("/api/sandbox/active", s.handleSandboxActive)
	apiMux.HandleFunc("/api/sandbox/resume", s.handleSandboxResume)
	apiMux.HandleFunc("/api/sandbox/events", s.handleSandboxEvents)
	apiMux.HandleFunc("/api/ai/config", s.handleAIConfig)
	apiMux.HandleFunc("/api/ai/stats", s.handleAIStats)
	apiMux.HandleFunc("/api/ai/clear-cache", s.handleAIClearCache)
	apiMux.HandleFunc("/api/ai/cache", s.handleAICacheList)
	apiMux.HandleFunc("/api/ai/cache/evict", s.handleAICacheEvict)
	apiMux.HandleFunc("/api/ai/completion", s.handleAICompletion)
	apiMux.HandleFunc("/api/audit", s.handleAudit)
	apiMux.HandleFunc("/api/test/generate-traffic", s.handleGenerateTraffic)

	// Endpoints de health check públicos (bypassam authMiddleware)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Middleware de autenticação protegendo toda a API
	mux.Handle("/api/", s.authMiddleware(apiMux))

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // Desativado: conexões SSE do Sandbox necessitam permanecer abertas indefinidamente
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		var err error
		if s.tlsEnabled && s.tlsCert != nil {
			s.server.TLSConfig = &tls.Config{
				Certificates: []tls.Certificate{*s.tlsCert},
				MinVersion:   tls.VersionTLS12,
			}
			err = s.server.ListenAndServeTLS("", "")
		} else {
			err = s.server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			// Erro silencioso ou logado no console se necessário
		}
	}()

	return nil
}

// Stop desliga o servidor administrativamente.
func (s *AdminServer) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(resp)
}

type authState struct {
	failedAttempts int
	lastAttempt    time.Time
	lockoutUntil   time.Time
}

var (
	authLimiterMu sync.Mutex
	authStates    = make(map[string]*authState)
)

func checkAuthRateLimit(ip string) error {
	authLimiterMu.Lock()
	defer authLimiterMu.Unlock()

	state, ok := authStates[ip]
	if !ok {
		return nil
	}

	if !state.lockoutUntil.IsZero() {
		if time.Now().Before(state.lockoutUntil) {
			return fmt.Errorf("too many failed login attempts. Please wait %d seconds", int(time.Until(state.lockoutUntil).Seconds()))
		}
		// Reset/clean up lockout since it has expired
		state.lockoutUntil = time.Time{}
		state.failedAttempts = 0
	}
	return nil
}

func recordFailedAuth(ip string) {
	authLimiterMu.Lock()
	defer authLimiterMu.Unlock()

	state, ok := authStates[ip]
	if !ok {
		state = &authState{}
		authStates[ip] = state
	}

	state.failedAttempts++
	state.lastAttempt = time.Now()
	if state.failedAttempts >= 5 {
		state.lockoutUntil = time.Now().Add(1 * time.Minute)
	}
}

func recordSuccessfulAuth(ip string) {
	authLimiterMu.Lock()
	defer authLimiterMu.Unlock()
	delete(authStates, ip)
}

func startAuthLimiterCleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		for range ticker.C {
			authLimiterMu.Lock()
			now := time.Now()
			for ip, state := range authStates {
				if now.Sub(state.lastAttempt) > 1*time.Hour {
					delete(authStates, ip)
				}
			}
			authLimiterMu.Unlock()
		}
	}()
}

func (s *AdminServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			ip = host
		}

		if err := checkAuthRateLimit(ip); err != nil {
			writeJSONError(w, http.StatusTooManyRequests, err.Error())
			return
		}

		givenToken := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			givenToken = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// Permite token no URL apenas para a rota de eventos SSE, pois EventSource no JS não suporta headers customizados de forma nativa
		if givenToken == "" && r.URL.Path == "/api/sandbox/events" {
			givenToken = r.URL.Query().Get("token")
		}

		if givenToken == "" || subtle.ConstantTimeCompare([]byte(givenToken), []byte(s.token)) != 1 {
			recordFailedAuth(ip)
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		recordSuccessfulAuth(ip)

		// Validação Anti-CSRF simples baseada em Origin/Referer para requisições de escrita (POST, PUT, DELETE)
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			origin := r.Header.Get("Origin")
			referer := r.Header.Get("Referer")
			host := r.Host

			if origin != "" {
				parsedOrigin := origin
				if idx := strings.Index(origin, "://"); idx >= 0 {
					parsedOrigin = origin[idx+3:]
				}
				if parsedOrigin != host && !strings.HasPrefix(parsedOrigin, "localhost:") && !strings.HasPrefix(parsedOrigin, "127.0.0.1:") {
					writeJSONError(w, http.StatusForbidden, "Cross-Origin request blocked")
					return
				}
			} else if referer != "" {
				parsedReferer := referer
				if idx := strings.Index(referer, "://"); idx >= 0 {
					parsedReferer = referer[idx+3:]
				}
				if !strings.HasPrefix(parsedReferer, host) && !strings.HasPrefix(parsedReferer, "localhost:") && !strings.HasPrefix(parsedReferer, "127.0.0.1:") {
					writeJSONError(w, http.StatusForbidden, "Cross-Origin request blocked")
					return
				}
			}
		}

		// Proteções HTTP extras contra vazamentos e hijacking
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; script-src 'self' 'unsafe-inline' 'unsafe-eval'; img-src 'self' data:; connect-src 'self' ws: wss:;")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r)
	})
}

func generateSecureToken() (string, error) {
	bytes := make([]byte, 32) // 256 bits de segurança
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// REST Handlers

type DomainStat struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

type StatusResponse struct {
	UptimeSeconds     int64        `json:"uptimeSeconds"`
	TotalRequests     int64        `json:"totalRequests"`
	ActiveConnections int64        `json:"activeConnections"`
	VmPoolSize        int          `json:"vmPoolSize"`
	TotalErrors       int64        `json:"totalErrors"`
	TopDomains        []DomainStat `json:"topDomains"`
	EBPFActive        bool         `json:"ebpfActive"`
	WSLAvailable      bool         `json:"wslAvailable"`
	GoMemoryAlloc     uint64       `json:"goMemoryAlloc"`
	GoGoroutines      int          `json:"goGoroutines"`
	NumCPU            int          `json:"numCPU"`
}

func (s *AdminServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	metricsText := fetchLocalMetrics()
	metrics := parseMetricsText(metricsText)

	reqTotal := int64(sumMetrics(metrics, "sonic_requests_total"))
	activeConns := int64(sumMetrics(metrics, "sonic_connections_active"))
	errorsTotal := int64(sumMetrics(metrics, "sonic_errors_total"))
	
	// Tentar obter pool size do Prometheus ou da configuração
	vmPoolSize := s.cfg.Runtime.PoolSize
	if vmsMetric := sumMetrics(metrics, "sonic_vm_pool_size"); vmsMetric > 0 {
		vmPoolSize = int(vmsMetric)
	}

	topD := getTopDomains(metrics, 10)

	var m goRuntime.MemStats
	goRuntime.ReadMemStats(&m)

	resp := StatusResponse{
		UptimeSeconds:     int64(time.Since(startTime).Seconds()),
		TotalRequests:     reqTotal,
		ActiveConnections: activeConns,
		VmPoolSize:        vmPoolSize,
		TotalErrors:       errorsTotal,
		TopDomains:        topD,
		EBPFActive:        ebpf.IsActive(),
		WSLAvailable:      isWSLAvailable(),
		GoMemoryAlloc:     m.Alloc,
		GoGoroutines:      goRuntime.NumGoroutine(),
		NumCPU:            goRuntime.NumCPU(),
	}

	json.NewEncoder(w).Encode(resp)
}

type WorkerInfo struct {
	Name       string `json:"name"`
	Size       string `json:"size"`
	OnTraffic  bool   `json:"onTraffic"`
	OnResponse bool   `json:"onResponse"`
	Language   string `json:"language"`
	Protocol   string `json:"protocol"`
}

func isValidWorkerFilename(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	supported := map[string]bool{
		".js": true, ".py": true, ".rb": true, ".sh": true,
		".wasm": true, ".rs": true, ".go": true, ".c": true, ".pl": true,
	}
	return supported[ext] || ext == ""
}

func getWorkerLanguage(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".js":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rb":
		return "Ruby"
	case ".sh":
		return "Shell Script"
	case ".pl":
		return "Perl"
	case ".wasm":
		return "WebAssembly"
	case ".go":
		return "Go (WASM)"
	case ".rs":
		return "Rust (WASM)"
	case ".c":
		return "C (WASM)"
	default:
		return "Native Binary"
	}
}

func getWorkerProtocol(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".js":
		return "HTTP / HTTPS"
	case ".wasm", ".go", ".rs", ".c":
		return "HTTP / HTTPS / WASM"
	default:
		return "Multi-Protocol (TCP/HTTP)"
	}
}

func (s *AdminServer) handleWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		name := r.URL.Query().Get("name")
		if name != "" {
			if !isValidWorkerFilename(name) {
				writeJSONError(w, http.StatusBadRequest, "nome de arquivo invalido ou nao permitido")
				return
			}
			filePath := filepath.Join(s.functions, name)
			content, err := os.ReadFile(filePath)
			if err != nil {
				writeJSONError(w, http.StatusNotFound, "worker nao encontrado")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"name": name,
				"code": string(content),
			})
			return
		}

		files, err := os.ReadDir(s.functions)
		if err != nil {
			json.NewEncoder(w).Encode([]WorkerInfo{})
			return
		}

		var workers []WorkerInfo
		for _, f := range files {
			if f.IsDir() || !isValidWorkerFilename(f.Name()) {
				continue
			}

			filePath := filepath.Join(s.functions, f.Name())
			info, err := os.Stat(filePath)
			sizeStr := "-"
			if err == nil {
				sizeStr = formatBytes(info.Size())
			}

			content, err := os.ReadFile(filePath)
			hasOnTraffic := false
			hasOnResponse := false
			if err == nil {
				code := string(content)
				hasOnTraffic = strings.Contains(code, "onTraffic")
				hasOnResponse = strings.Contains(code, "onResponse")
			}

			workers = append(workers, WorkerInfo{
				Name:       f.Name(),
				Size:       sizeStr,
				OnTraffic:  hasOnTraffic,
				OnResponse: hasOnResponse,
				Language:   getWorkerLanguage(f.Name()),
				Protocol:   getWorkerProtocol(f.Name()),
			})
		}

		json.NewEncoder(w).Encode(workers)
		return
	}

	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		var req struct {
			Name string `json:"name"`
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "JSON invalido")
			return
		}
		if !isValidWorkerFilename(req.Name) {
			writeJSONError(w, http.StatusBadRequest, "nome de arquivo invalido. Deve ser seguro e terminar com extensao de script suportada (ex: .js)")
			return
		}

		filePath := filepath.Join(s.functions, req.Name)
		// Garantir que a pasta functions exista
		if err := os.MkdirAll(s.functions, 0755); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "falha ao criar pasta de funcoes: "+err.Error())
			return
		}

		if err := os.WriteFile(filePath, []byte(req.Code), 0644); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "falha ao escrever arquivo: "+err.Error())
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"saved"}`))
		return
	}

	if r.Method == http.MethodDelete {
		name := r.URL.Query().Get("name")
		if name == "" || !isValidWorkerFilename(name) {
			writeJSONError(w, http.StatusBadRequest, "nome de arquivo invalido ou ausente")
			return
		}

		filePath := filepath.Join(s.functions, name)
		if err := os.Remove(filePath); err != nil {
			if os.IsNotExist(err) {
				writeJSONError(w, http.StatusNotFound, "arquivo nao existe")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "falha ao remover arquivo: "+err.Error())
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"deleted"}`))
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *AdminServer) handleCVEs(w http.ResponseWriter, r *http.Request) {
	incidents := security.GetSecurityIncidents()
	
	// Retornar em ordem reversa (mais recentes primeiro)
	reversed := make([]security.SecurityIncident, len(incidents))
	for i := range incidents {
		reversed[i] = incidents[len(incidents)-1-i]
	}
	
	json.NewEncoder(w).Encode(reversed)
}

func (s *AdminServer) handleExecutions(w http.ResponseWriter, r *http.Request) {
	execs := telemetry.GetFunctionExecutions()
	
	reversed := make([]telemetry.FunctionExecution, len(execs))
	for i := range execs {
		reversed[i] = execs[len(execs)-1-i]
	}
	
	json.NewEncoder(w).Encode(reversed)
}

func (s *AdminServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	logs := telemetry.GetWorkerLogs()
	
	reversed := make([]telemetry.WorkerLog, len(logs))
	for i := range logs {
		reversed[i] = logs[len(logs)-1-i]
	}
	
	json.NewEncoder(w).Encode(reversed)
}

func (s *AdminServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"shutting_down"}`))

	// Envia sinal de encerramento em uma goroutine separada para dar tempo de retornar a resposta HTTP
	go func() {
		time.Sleep(500 * time.Millisecond)
		p, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = p.Signal(os.Interrupt)
		}
	}()
}

// Auxiliares de Métricas

type tempMetric struct {
	Name   string
	Labels map[string]string
	Value  float64
}

func fetchLocalMetrics() string {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	promhttp.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}

func parseMetricsText(body string) []tempMetric {
	var metrics []tempMetric
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		openBrace := strings.Index(line, "{")
		closeBrace := strings.Index(line, "}")
		
		var name string
		var labels map[string]string
		var rest string
		
		if openBrace >= 0 && closeBrace > openBrace {
			name = line[:openBrace]
			labels = parseMetricLabels(line[openBrace+1 : closeBrace])
			rest = strings.TrimSpace(line[closeBrace+1:])
		} else {
			spaceIdx := strings.Index(line, " ")
			if spaceIdx < 0 {
				continue
			}
			name = line[:spaceIdx]
			rest = strings.TrimSpace(line[spaceIdx+1:])
		}
		
		if rest == "" {
			continue
		}
		
		val, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			continue
		}
		
		if labels == nil {
			labels = make(map[string]string)
		}
		
		metrics = append(metrics, tempMetric{Name: name, Labels: labels, Value: val})
	}
	return metrics
}

func parseMetricLabels(s string) map[string]string {
	labels := make(map[string]string)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.Trim(strings.TrimSpace(part[eq+1:]), "\"")
		labels[key] = val
	}
	return labels
}

func sumMetrics(metrics []tempMetric, name string) float64 {
	var sum float64
	for _, m := range metrics {
		if m.Name == name {
			sum += m.Value
		}
	}
	return sum
}

func getTopDomains(metrics []tempMetric, limit int) []DomainStat {
	counts := make(map[string]int)
	for _, m := range metrics {
		if m.Name == "sonic_requests_total" {
			d := m.Labels["sonic_domain"]
			if d == "" {
				d = "unknown"
			}
			counts[d] += int(m.Value)
		}
	}

	var stats []DomainStat
	for d, c := range counts {
		stats = append(stats, DomainStat{Domain: d, Count: c})
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})

	if len(stats) > limit {
		stats = stats[:limit]
	}
	return stats
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// REST Handlers para o Audit Trail (Logs de Auditoria)

func (s *AdminServer) handleAudit(w http.ResponseWriter, r *http.Request) {
	db := runtime.GetDB()
	if db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not initialized")
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	logs, err := db.GetAuditLogs(limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if logs == nil {
		logs = []map[string]interface{}{}
	}

	json.NewEncoder(w).Encode(logs)
}

// REST Handlers para o Visual Pipeline Builder

func (s *AdminServer) handlePipelines(w http.ResponseWriter, r *http.Request) {
	if s.kvStore == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "KV store not initialized")
		return
	}

	if r.Method == http.MethodGet {
		list, err := runtime.GetPipelines(s.kvStore)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if list == nil {
			list = []runtime.Pipeline{}
		}
		json.NewEncoder(w).Encode(list)
		return
	}

	if r.Method == http.MethodPost {
		var p runtime.Pipeline
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if p.ID == "" {
			writeJSONError(w, http.StatusBadRequest, "pipeline ID is required")
			return
		}
		if err := runtime.SavePipeline(s.kvStore, &p); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to save: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"saved"}`))
		return
	}

	if r.Method == http.MethodDelete {
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSONError(w, http.StatusBadRequest, "pipeline ID is required")
			return
		}
		if err := runtime.DeletePipeline(s.kvStore, id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to delete: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"deleted"}`))
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

// REST Handlers para o Interactive Sandbox & Breakpoints

func (s *AdminServer) handleSandboxConfig(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Sandbox not initialized")
		return
	}

	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(s.sandbox.Config())
		return
	}

	if r.Method == http.MethodPost {
		var cfg SandboxConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := s.sandbox.SaveConfig(cfg); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"saved"}`))
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *AdminServer) handleSandboxActive(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Sandbox not initialized")
		return
	}
	list := s.sandbox.GetActiveTransactions()
	if list == nil {
		list = []*SandboxTransaction{}
	}
	json.NewEncoder(w).Encode(list)
}

func (s *AdminServer) handleSandboxResume(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Sandbox not initialized")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		ID         string            `json:"id"`
		Action     string            `json:"action"` // "allow", "block", "modify"
		Headers    map[string]string `json:"headers"`
		Body       string            `json:"body"`
		Status     int               `json:"status"`
		RespDirect *struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		} `json:"respDirect,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	action := ResumeAction{
		Action:  body.Action,
		Headers: body.Headers,
		Body:    body.Body,
		Status:  body.Status,
	}

	if body.RespDirect != nil {
		action.Response = &runtime.Response{
			Status:  body.RespDirect.Status,
			Headers: body.RespDirect.Headers,
			Body:    body.RespDirect.Body,
		}
	}

	if s.sandbox.Resume(body.ID, action) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"resumed"}`))
	} else {
		writeJSONError(w, http.StatusNotFound, "transaction not found or already resumed")
	}
}

func (s *AdminServer) handleSandboxEvents(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan string, 10)
	s.sandbox.RegisterChannel(ch)
	defer s.sandbox.UnregisterChannel(ch)

	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"ready\"}\n\n")
	flusher.Flush()

	notifyChan := r.Context().Done()
	for {
		select {
		case msg := <-ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-notifyChan:
			return
		}
	}
}

// REST Handlers para o AI Proxy Gateway

func isMaskedKey(key string) bool {
	return strings.Contains(key, "[REDACTED]") || strings.Contains(key, "...")
}

func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 20 {
		return "[REDACTED]"
	}
	prefixLen := 7
	if prefixLen > len(key) {
		prefixLen = len(key) / 2
	}
	return key[:prefixLen] + "...[REDACTED]"
}

func (s *AdminServer) handleAIConfig(w http.ResponseWriter, r *http.Request) {
	if s.aiGateway == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI Gateway not initialized")
		return
	}

	if r.Method == http.MethodGet {
		cfg := s.aiGateway.GetConfig()
		cfg.OpenAIKey = maskAPIKey(cfg.OpenAIKey)
		cfg.AnthropicKey = maskAPIKey(cfg.AnthropicKey)
		json.NewEncoder(w).Encode(cfg)
		return
	}

	if r.Method == http.MethodPost {
		var cfg AIGatewayConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Se as chaves enviadas forem as mascaradas, mantemos as chaves salvas anteriormente
		currentCfg := s.aiGateway.GetConfig()
		if isMaskedKey(cfg.OpenAIKey) {
			cfg.OpenAIKey = currentCfg.OpenAIKey
		}
		if isMaskedKey(cfg.AnthropicKey) {
			cfg.AnthropicKey = currentCfg.AnthropicKey
		}

		if err := s.aiGateway.SaveConfig(cfg); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"saved"}`))
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *AdminServer) handleAIStats(w http.ResponseWriter, r *http.Request) {
	if s.aiGateway == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI Gateway not initialized")
		return
	}
	json.NewEncoder(w).Encode(s.aiGateway.GetStats())
}

func (s *AdminServer) handleAIClearCache(w http.ResponseWriter, r *http.Request) {
	if s.aiGateway == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI Gateway not initialized")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.aiGateway.ClearCache()
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"cache_cleared"}`))
}

func (s *AdminServer) handleAICompletion(w http.ResponseWriter, r *http.Request) {
	if s.aiGateway == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"AI Gateway not initialized"}`))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"failed to read request body"}`))
		return
	}

	// 1. Verificar se a requisição possui flag stream
	var streamCheck struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(bodyBytes, &streamCheck)

	if streamCheck.Stream {
		// Checar se dá HIT no cache semântico
		hit := s.aiGateway.CheckCacheHit(r.Context(), bodyBytes)
		if hit != nil {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Sonic-Cache", "HIT")

			flusher, ok := w.(http.Flusher)

			// Envia a resposta simulando o stream de forma ultra-rápida (chunks de palavras)
			words := strings.Fields(hit.Response)
			for i, word := range words {
				chunk := word
				if i < len(words)-1 {
					chunk += " "
				}

				// OpenAI SSE stream payload
				payload, _ := json.Marshal(map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-sonic-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   "sonic-semantic-cache",
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"content": chunk,
							},
							"finish_reason": nil,
						},
					},
				})

				fmt.Fprintf(w, "data: %s\n\n", string(payload))
				if ok {
					flusher.Flush()
				}
				time.Sleep(5 * time.Millisecond) // Simula latência instantânea de stream de cache
			}

			// Sinaliza término
			fmt.Fprintf(w, "data: [DONE]\n\n")
			if ok {
				flusher.Flush()
			}
			return
		}

		// Se deu MISS, encaminha o stream real
		w.Header().Set("X-Sonic-Cache", "MISS")
		errStream := s.aiGateway.ForwardStream(r.Context(), bodyBytes, w)
		if errStream != nil {
			writeJSONError(w, http.StatusBadGateway, "Stream forwarding failed: "+errStream.Error())
		}
		return
	}

	// Não é stream: fluxo normal
	entry, respBytes, err := s.aiGateway.ProcessChatCompletion(r.Context(), bodyBytes)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "AI API call failed: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if entry != nil {
		w.Header().Set("X-Sonic-Cache", "HIT")
	} else {
		w.Header().Set("X-Sonic-Cache", "MISS")
	}

	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}

func (s *AdminServer) handleAICacheEvict(w http.ResponseWriter, r *http.Request) {
	if s.aiGateway == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI Gateway not initialized")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var parsed struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&parsed); err != nil || parsed.Prompt == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON or prompt is empty")
		return
	}

	if err := s.aiGateway.EvictCacheEntry(parsed.Prompt); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"evicted"}`))
}

type WebuiAICacheEntry struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
	Tokens   int    `json:"tokens"`
}

func (s *AdminServer) handleAICacheList(w http.ResponseWriter, r *http.Request) {
	db := runtime.GetDB()
	if db == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not initialized")
		return
	}

	rows, err := db.DB().Query("SELECT id, prompt, response, tokens FROM ai_cache ORDER BY created DESC LIMIT 50;")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var list []WebuiAICacheEntry
	for rows.Next() {
		var e WebuiAICacheEntry
		if err := rows.Scan(&e.ID, &e.Prompt, &e.Response, &e.Tokens); err == nil {
			list = append(list, e)
		}
	}
	if list == nil {
		list = []WebuiAICacheEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}


func (s *AdminServer) handlePipelinesTest(w http.ResponseWriter, r *http.Request) {
	if s.kvStore == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "KV store not initialized")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var parsed struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Body    string            `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&parsed); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	req := &runtime.Request{
		Method:  parsed.Method,
		URL:     parsed.URL,
		Path:    parsed.URL,
		Headers: parsed.Headers,
		Body:    parsed.Body,
	}
	if idx := strings.Index(parsed.URL, "://"); idx >= 0 {
		req.Path = parsed.URL[idx+3:]
		if slashIdx := strings.Index(req.Path, "/"); slashIdx >= 0 {
			req.Path = req.Path[slashIdx:]
		} else {
			req.Path = "/"
		}
	}

	pipelines, err := runtime.GetPipelines(s.kvStore)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
		return
	}

	// Ordena por prioridade
	sort.Slice(pipelines, func(i, j int) bool {
		return pipelines[i].Priority > pipelines[j].Priority
	})

	var matched *runtime.Pipeline
	for _, p := range pipelines {
		if p.Match(req) {
			matched = &p
			break
		}
	}

	res := map[string]interface{}{
		"matched": matched != nil,
	}
	if matched != nil {
		res["pipeline"] = matched
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *AdminServer) handleGenerateTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 1. Garantir o worker demonstrativo 'demo_logger.js' na pasta functions
	workerPath := filepath.Join(s.functions, "demo_logger.js")
	if _, err := os.Stat(workerPath); os.IsNotExist(err) {
		code := `// demo_logger.js
function onTraffic(request) {
    console.log("Recebendo conexao simulada no Sonic Proxy para: " + request.url);
    if (request.url.includes("danger") || request.url.includes("exec")) {
        console.warn("ALERTA: Tentativa de exploit suspeita detectada no caminho!");
    } else {
        console.log("Tráfego de requisição padrão processado pelo worker de demonstração.");
    }
    request.headers.set("X-Sonic-Demo", "Active-Simulated-Traffic");
    return request;
}

function onResponse(response) {
    console.log("Processando resposta do upstream no worker de demonstração (Status " + response.status + ")");
    return response;
}
`
		_ = os.WriteFile(workerPath, []byte(code), 0644)
	}

	// 2. Carregar o worker se ele ainda não estiver carregado no manager
	if s.workerManager != nil {
		_ = s.workerManager.LoadWorker(runtime.WorkerConfig{
			FilePath:  workerPath,
			TimeoutMS: s.cfg.Runtime.TimeoutMS,
			PoolSize:  s.cfg.Runtime.PoolSize,
			KVStore:   s.kvStore,
		})
	}

	// 3. Verificar se há algum pipeline. Se não houver nenhum, cria um pipeline temporário de teste
	if s.kvStore != nil {
		pipelines, err := runtime.GetPipelines(s.kvStore)
		if err == nil && len(pipelines) == 0 {
			demoPipe := &runtime.Pipeline{
				ID:       "demo-pipeline",
				Name:     "Demo Traffic Pipeline",
				Pattern:  "*",
				Enabled:  true,
				Priority: 10,
				Steps: []runtime.PipelineStep{
					{
						Type:    runtime.StepTypeCVEScanner,
						Enabled: true,
					},
					{
						Type:    runtime.StepTypeRateLimiter,
						Enabled: true,
						Limit:   100,
						Window:  60000,
					},
					{
						Type:    runtime.StepTypeWorker,
						Enabled: true,
						Name:    "demo_logger.js",
					},
				},
			}
			_ = runtime.SavePipeline(s.kvStore, demoPipe)
		}
	}

	// 4. Inserir dados pré-seeded no cache de IA (ai_cache) caso a tabela esteja vazia
	db := runtime.GetDB()
	if db != nil {
		var count int
		_ = db.DB().QueryRow("SELECT COUNT(*) FROM ai_cache").Scan(&count)
		if count == 0 {
			// Inserir perguntas de demonstração
			demoQA := []struct {
				q, a string
			}{
				{"O que e o Sonic Engine?", "Sonic Engine e um proxy reverso e servidor de Edge Computing de alto desempenho escrito em Go e Goja JS VM."},
				{"Como configurar um worker?", "Crie um arquivo JavaScript na pasta functions contendo a funcao onTraffic(request) e registre o mesmo na aba Edge Workers da WebUI."},
				{"Qual a versao atual?", "A versao atual do Sonic e 1.3.0 executando em arquitetura distribuida."},
			}
			for _, qa := range demoQA {
				uuid := fmt.Sprintf("demo-qa-%d", time.Now().UnixNano())
				embBytes, _ := json.Marshal([]float64{0.1, 0.2, 0.3})
				_, errInsert := db.DB().Exec(`
					INSERT INTO ai_cache (id, prompt, response, embedding, created, tokens, expires_at)
					VALUES (?, ?, ?, ?, ?, ?, ?);
				`, uuid, qa.q, qa.a, embBytes, time.Now().Format(time.RFC3339), 50, nil)
				if errInsert == nil {
					var rowid int64
					_ = db.DB().QueryRow("SELECT last_insert_rowid();").Scan(&rowid)
					_, _ = db.DB().Exec(`
						INSERT INTO ai_cache_fts (rowid, prompt, response)
						VALUES (?, ?, ?);
					`, rowid, qa.q, qa.a)
				}
			}
		}
	}

	// 5. Iniciar goroutine para simular tráfego concorrente realista
	go func() {
		// Simular ~40 requisições variadas em lotes rápidos
		methods := []string{"GET", "POST", "PUT"}
		urls := []string{
			"http://portal.corporativo.com/api/v1/users",
			"http://portal.corporativo.com/dashboard/analytics",
			"http://portal.corporativo.com/static/js/main.js",
			"http://portal.corporativo.com/api/v1/data",
		}
		cves := []string{
			"cmd=cat+/etc/passwd",
			"query=select+*+from+users",
			"exec=/bin/sh",
		}

		for i := 0; i < 40; i++ {
			// Requisição Normal
			method := methods[i%len(methods)]
			url := urls[i%len(urls)]
			packet := &runtime.Packet{
				ID:        fmt.Sprintf("sim-%d-%d", time.Now().UnixNano(), i),
				Protocol:  runtime.ProtocolHTTP,
				Source:    fmt.Sprintf("192.168.1.%d:50%d", 10+i, i),
				Dest:      "portal.corporativo.com:80",
				IsInbound: true,
				Request: &runtime.Request{
					Method:     method,
					URL:        url,
					Path:       url[26:], // remove http://portal.corporativo.com
					Headers:    map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) simulated", "Accept": "application/json"},
					Body:       `{"status":"active"}`,
					ClientAddr: fmt.Sprintf("192.168.1.%d", 10+i),
				},
			}
			if s.workerManager != nil {
				_, _ = s.workerManager.ProcessPacket(packet)
			}

			// Simular latência realista entre as chamadas
			time.Sleep(10 * time.Millisecond)
		}

		// Requisições com CVE/Exploits
		for i := 0; i < 5; i++ {
			cvePayload := cves[i%len(cves)]
			packetDanger := &runtime.Packet{
				ID:        fmt.Sprintf("sim-danger-%d-%d", time.Now().UnixNano(), i),
				Protocol:  runtime.ProtocolHTTP,
				Source:    fmt.Sprintf("85.204.11.%d:6123%d", 20+i, i),
				Dest:      "portal.corporativo.com:80",
				IsInbound: true,
				Request: &runtime.Request{
					Method:     "POST",
					URL:        "http://portal.corporativo.com/admin/settings?danger=true",
					Path:       "/admin/settings",
					Headers:    map[string]string{"User-Agent": "curl/7.79.1 simulated", "Content-Type": "application/x-www-form-urlencoded"},
					Body:       cvePayload,
					ClientAddr: fmt.Sprintf("85.204.11.%d", 20+i),
				},
			}
			if s.workerManager != nil {
				_, _ = s.workerManager.ProcessPacket(packetDanger)
			}
			time.Sleep(15 * time.Millisecond)
		}

		// Requisições AI Gateway (forçando cache hits e misses)
		if s.aiGateway != nil {
			// Ativar o AI Gateway de forma temporária se estiver inativo
			s.aiGateway.mu.Lock()
			wasActive := s.aiGateway.config.Active
			s.aiGateway.config.Active = true
			wasMode := s.aiGateway.config.Mode
			s.aiGateway.config.Mode = "offline" // Grátis, sem precisar de chaves
			s.aiGateway.mu.Unlock()

			prompts := []string{
				"O que e o Sonic Engine?",
				"Como configurar um worker?",
				"Qual a versao atual?",
				"Pergunta que vai dar MISS",
			}

			for _, pText := range prompts {
				reqBody, _ := json.Marshal(map[string]interface{}{
					"model": "gpt-4o-mini",
					"messages": []map[string]interface{}{
						{"role": "user", "content": pText},
					},
				})
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, _, _ = s.aiGateway.ProcessChatCompletion(ctx, reqBody)
				cancel()
				time.Sleep(150 * time.Millisecond)
			}

			// Restaurar estado do AI Gateway
			s.aiGateway.mu.Lock()
			s.aiGateway.config.Active = wasActive
			s.aiGateway.config.Mode = wasMode
			s.aiGateway.mu.Unlock()
		}
	}()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"started","message":"Traffic simulation initialized in background."}`))
}

func isWSLAvailable() bool {
	if goRuntime.GOOS != "windows" {
		return false
	}
	windir := os.Getenv("SystemRoot")
	if windir == "" {
		windir = "C:\\Windows"
	}
	wslPath := filepath.Join(windir, "System32", "wsl.exe")
	if _, err := os.Stat(wslPath); err != nil {
		return false
	}

	// Tenta listar silenciosamente as distribuições WSL ativas.
	// Se o WSL não estiver ativado, se for um stub da Microsoft Store não instalado,
	// ou se não houver nenhuma distro Linux instalada, o comando retornará erro.
	// Usamos context com timeout para garantir que o web server nunca trave.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, wslPath, "--list", "--quiet")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return len(output) > 0
}
