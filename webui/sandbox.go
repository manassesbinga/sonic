package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/manassesbinga/sonic/runtime"
)

// SandboxTransaction representa uma requisição/resposta HTTP suspensa no breakpoint.
type SandboxTransaction struct {
	ID         string            `json:"id"`
	Domain     string            `json:"domain"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	IsInbound  bool              `json:"isInbound"` // true = request, false = response
	Status     int               `json:"status,omitempty"` // Apenas para resposta
	ResumeChan chan ResumeAction `json:"-"`
}

// ResumeAction define a ação a tomar quando a requisição/resposta é retomada.
type ResumeAction struct {
	Action   string            // "allow", "block", "modify"
	Headers  map[string]string // Modificações nos headers
	Body     string            // Modificações no body
	Status   int               // Novo status se for resposta ou se for editado
	Response *runtime.Response // Resposta direta caso queira responder direto no request
}

// SandboxConfig armazena os domínios configurados para entrar em Sandbox.
type SandboxConfig struct {
	EnabledDomains []string `json:"enabledDomains"`
	Active         bool     `json:"active"`
	TimeoutSeconds int      `json:"timeoutSeconds"` // Tempo máximo de pausa
	BypassPaths    []string `json:"bypassPaths"`    // Caminhos excluídos (ex: /health)
}

// SandboxCoordinator gerencia os breakpoints de conexões pausadas.
type SandboxCoordinator struct {
	mu             sync.RWMutex
	activeTx       sync.Map // ID -> *SandboxTransaction
	config         SandboxConfig
	eventChans     map[chan string]bool
	chanMu         sync.Mutex
	kvStore        *runtime.KVStore
}

// NewSandboxCoordinator cria uma nova instância do SandboxCoordinator.
func NewSandboxCoordinator(kv *runtime.KVStore) *SandboxCoordinator {
	sc := &SandboxCoordinator{
		eventChans: make(map[chan string]bool),
		kvStore:    kv,
	}
	sc.loadConfig()
	return sc
}

// loadConfig carrega a configuração a partir do KVStore.
func (sc *SandboxCoordinator) loadConfig() {
	if sc.kvStore == nil {
		return
	}
	data := sc.kvStore.Get("_sys:sandbox:config")
	if data != nil {
		var cfg SandboxConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			sc.config = cfg
		}
	}
}

// SaveConfig guarda a configuração no KVStore.
func (sc *SandboxCoordinator) SaveConfig(cfg SandboxConfig) error {
	sc.mu.Lock()
	sc.config = cfg
	sc.mu.Unlock()

	if sc.kvStore == nil {
		return nil
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return sc.kvStore.Set("_sys:sandbox:config", data)
}

// Config retorna a configuração ativa do Sandbox.
func (sc *SandboxCoordinator) Config() SandboxConfig {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.config
}

// RegisterChannel registra um canal de SSE.
func (sc *SandboxCoordinator) RegisterChannel(ch chan string) {
	sc.chanMu.Lock()
	sc.eventChans[ch] = true
	sc.chanMu.Unlock()
}

// UnregisterChannel remove um canal de SSE.
func (sc *SandboxCoordinator) UnregisterChannel(ch chan string) {
	sc.chanMu.Lock()
	delete(sc.eventChans, ch)
	clientCount := len(sc.eventChans)
	sc.chanMu.Unlock()

	// Se todos os clientes WebUI se desconectarem, libera automaticamente todas as requisições suspensas (Fail-Safe)
	if clientCount == 0 {
		sc.ReleaseAll()
	}
}

// ReleaseAll libera todas as requisições ativas travadas em breakpoints.
func (sc *SandboxCoordinator) ReleaseAll() {
	sc.activeTx.Range(func(key, value interface{}) bool {
		tx := value.(*SandboxTransaction)
		select {
		case tx.ResumeChan <- ResumeAction{Action: "allow"}:
		default:
		}
		return true
	})
}

// notifyClients envia um evento SSE para todos os clientes da WebUI escutando.
func (sc *SandboxCoordinator) notifyClients(eventType string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(payload))

	sc.chanMu.Lock()
	defer sc.chanMu.Unlock()
	for ch := range sc.eventChans {
		select {
		case ch <- msg:
		default:
			// Canal bloqueado ou fechado, ignora
		}
	}
}

// shouldIntercept verifica se o domínio e o caminho devem ser interceptados pelo sandbox.
func (sc *SandboxCoordinator) shouldIntercept(domain string, path string) bool {
	sc.mu.RLock()
	config := sc.config
	sc.mu.RUnlock()

	if !config.Active {
		return false
	}

	// 1. Verificar caminhos de Bypass (health check, etc)
	for _, bp := range config.BypassPaths {
		bp = strings.TrimSpace(bp)
		if bp != "" && (bp == path || strings.HasPrefix(path, bp)) {
			return false
		}
	}

	// 2. Verificar domínios
	for _, d := range config.EnabledDomains {
		if d == "*" {
			return true
		}
		if strings.HasPrefix(d, "*.") {
			suffix := d[1:]
			if strings.HasSuffix(domain, suffix) {
				return true
			}
		}
		if domain == d {
			return true
		}
	}
	return false
}

// InterceptRequest é o gancho principal que suspende a requisição HTTP.
func (sc *SandboxCoordinator) InterceptRequest(req *runtime.Request, domain string) (*runtime.Request, *runtime.Response, bool) {
	if req == nil || !sc.shouldIntercept(domain, req.Path) {
		return req, nil, true
	}

	// Se não houver nenhum cliente WebUI ouvindo, não intercepta para evitar travar a rede (Fail-Safe)
	sc.chanMu.Lock()
	clients := len(sc.eventChans)
	sc.chanMu.Unlock()
	if clients == 0 {
		return req, nil, true
	}

	// Gerar ID seguro para transação
	txID := generateTxID()
	tx := &SandboxTransaction{
		ID:         txID,
		Domain:     domain,
		Method:     req.Method,
		Path:       req.Path,
		Headers:    req.Headers,
		Body:       req.Body,
		IsInbound:  true,
		ResumeChan: make(chan ResumeAction, 1),
	}

	sc.activeTx.Store(txID, tx)
	defer sc.activeTx.Delete(txID)

	// Notificar WebUI via SSE
	sc.notifyClients("request_paused", tx)

	timeoutSecs := sc.Config().TimeoutSeconds
	if timeoutSecs <= 0 {
		timeoutSecs = 10 // Padrão seguro de 10 segundos
	}

	timer := time.NewTimer(time.Duration(timeoutSecs) * time.Second)
	defer timer.Stop()

	// Aguardar interação do utilizador ou timeout configurado
	select {
	case action := <-tx.ResumeChan:
		switch action.Action {
		case "block":
			return nil, nil, false
		case "allow":
			return req, nil, true
		case "modify":
			// Se o utilizador reescreveu para responder direto no request
			if action.Response != nil {
				return req, action.Response, true
			}
			// Caso contrário, modifica o request
			req.Headers = action.Headers
			req.Body = action.Body
			return req, nil, true
		}
	case <-timer.C:
		// Timeout: Prossegue automaticamente para não quebrar a conexão permanentemente
		sc.notifyClients("request_timeout", map[string]string{"id": txID})
	}

	return req, nil, true
}

// InterceptResponse é o gancho principal que suspende a resposta HTTP.
func (sc *SandboxCoordinator) InterceptResponse(resp *runtime.Response, domain string, path string) (*runtime.Response, bool) {
	if resp == nil || !sc.shouldIntercept(domain, path) {
		return resp, true
	}

	sc.chanMu.Lock()
	clients := len(sc.eventChans)
	sc.chanMu.Unlock()
	if clients == 0 {
		return resp, true
	}

	txID := generateTxID()
	tx := &SandboxTransaction{
		ID:         txID,
		Domain:     domain,
		Method:     "RESPONSE",
		Path:       path,
		Headers:    resp.Headers,
		Body:       resp.Body,
		IsInbound:  false,
		Status:     resp.Status,
		ResumeChan: make(chan ResumeAction, 1),
	}

	sc.activeTx.Store(txID, tx)
	defer sc.activeTx.Delete(txID)

	// Notificar WebUI via SSE
	sc.notifyClients("response_paused", tx)

	timeoutSecs := sc.Config().TimeoutSeconds
	if timeoutSecs <= 0 {
		timeoutSecs = 10 // Padrão seguro
	}

	timer := time.NewTimer(time.Duration(timeoutSecs) * time.Second)
	defer timer.Stop()

	// Aguardar interação do utilizador ou timeout configurado
	select {
	case action := <-tx.ResumeChan:
		switch action.Action {
		case "block":
			return nil, false
		case "allow":
			return resp, true
		case "modify":
			resp.Headers = action.Headers
			resp.Body = action.Body
			if action.Status > 0 {
				resp.Status = action.Status
			}
			return resp, true
		}
	case <-timer.C:
		// Timeout: Prossegue automaticamente
		sc.notifyClients("response_timeout", map[string]string{"id": txID})
	}

	return resp, true
}

// Resume retoma a transação pausada.
func (sc *SandboxCoordinator) Resume(txID string, action ResumeAction) bool {
	val, exists := sc.activeTx.Load(txID)
	if !exists {
		return false
	}
	tx := val.(*SandboxTransaction)
	select {
	case tx.ResumeChan <- action:
		return true
	default:
		return false
	}
}

// GetActiveTransactions retorna a lista de transações suspensas no momento.
func (sc *SandboxCoordinator) GetActiveTransactions() []*SandboxTransaction {
	var list []*SandboxTransaction
	sc.activeTx.Range(func(key, value interface{}) bool {
		list = append(list, value.(*SandboxTransaction))
		return true
	})
	return list
}

func generateTxID() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
