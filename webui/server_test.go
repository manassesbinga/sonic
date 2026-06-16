package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/runtime"
	"github.com/manassesbinga/sonic/security"
)

func TestTokenGeneration(t *testing.T) {
	// Verifica se a geração do token gera strings válidas de 64 caracteres hexa
	token1, err := generateSecureToken()
	if err != nil {
		t.Fatalf("erro ao gerar token: %v", err)
	}

	token2, err := generateSecureToken()
	if err != nil {
		t.Fatalf("erro ao gerar token: %v", err)
	}

	if token1 == "" || token2 == "" {
		t.Error("esperava token não vazio")
	}

	if token1 == token2 {
		t.Error("esperava tokens diferentes (aleatoriedade)")
	}

	if len(token1) != 64 {
		t.Errorf("esperava comprimento de 64 caracteres hexa para 32 bytes, obteve %d", len(token1))
	}
}

func TestAuthMiddleware(t *testing.T) {
	cfg := &config.Config{}
	cfg.WebUI.Enabled = true
	cfg.WebUI.Port = 9091
	cfg.WebUI.Token = "meu-token-secreto-super-protegido-123"

	server, err := NewAdminServer(cfg)
	if err != nil {
		t.Fatalf("erro ao instanciar AdminServer: %v", err)
	}

	// Handler de teste protegido
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	protected := server.authMiddleware(testHandler)

	t.Run("Bloqueio Sem Token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		rec := httptest.NewRecorder()

		protected.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("esperava status 401, obteve %d", rec.Code)
		}
	})

	t.Run("Bloqueio Token Invalido", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status?token=token-errado", nil)
		rec := httptest.NewRecorder()

		protected.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("esperava status 401, obteve %d", rec.Code)
		}
	})

	t.Run("Acesso Valido Query Token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/sandbox/events?token=meu-token-secreto-super-protegido-123", nil)
		rec := httptest.NewRecorder()

		protected.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("esperava status 200, obteve %d", rec.Code)
		}

		if rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("esperava header Content-Type application/json, obteve %s", rec.Header().Get("Content-Type"))
		}

		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("esperava header X-Frame-Options DENY, obteve %s", rec.Header().Get("X-Frame-Options"))
		}
	})

	t.Run("Rejeicao Query Token em Rotas Normais", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status?token=meu-token-secreto-super-protegido-123", nil)
		rec := httptest.NewRecorder()

		protected.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("esperava status 401 para token na query parameter em rota normal, obteve %d", rec.Code)
		}
	})

	t.Run("Acesso Valido Header Token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer meu-token-secreto-super-protegido-123")
		rec := httptest.NewRecorder()

		protected.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("esperava status 200, obteve %d", rec.Code)
		}
	})
}

func TestServerEndpoints(t *testing.T) {
	cfg := &config.Config{}
	cfg.WebUI.Enabled = true
	cfg.WebUI.Port = 9091
	cfg.WebUI.Token = "test-token"

	server, err := NewAdminServer(cfg)
	if err != nil {
		t.Fatalf("erro ao instanciar AdminServer: %v", err)
	}

	// Setup do diretório de funções temporário para teste
	tempDir, err := os.MkdirTemp("", "sonic-functions-test-*")
	if err != nil {
		t.Fatalf("erro ao criar pasta temporaria: %v", err)
	}
	defer os.RemoveAll(tempDir)

	server.functions = tempDir

	// Escreve um worker de teste
	workerCode := `// functions/auth.js
function onTraffic(request) {
	return request;
}`
	err = os.WriteFile(filepath.Join(tempDir, "auth.js"), []byte(workerCode), 0644)
	if err != nil {
		t.Fatalf("erro ao criar arquivo de worker: %v", err)
	}

	t.Run("Workers API", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/workers", nil)
		rec := httptest.NewRecorder()

		server.handleWorkers(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("esperava status 200, obteve %d", rec.Code)
		}

		var list []WorkerInfo
		err := json.Unmarshal(rec.Body.Bytes(), &list)
		if err != nil {
			t.Fatalf("erro ao fazer parse do JSON: %v", err)
		}

		if len(list) != 1 {
			t.Fatalf("esperava 1 worker listado, obteve %d", len(list))
		}

		if list[0].Name != "auth.js" {
			t.Errorf("esperava nome auth.js, obteve %s", list[0].Name)
		}

		if !list[0].OnTraffic {
			t.Error("esperava que onTraffic fosse detectado no worker")
		}

		if list[0].OnResponse {
			t.Error("não esperava que onResponse fosse detectado no worker")
		}
	})

	t.Run("CVE API", func(t *testing.T) {
		// Adicionar um incidente artificial para testar
		security.AddSecurityIncident("(?i)(eval\\s*\\()", "eval(req.body)")

		req := httptest.NewRequest("GET", "/api/cves", nil)
		rec := httptest.NewRecorder()

		server.handleCVEs(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("esperava status 200, obteve %d", rec.Code)
		}

		var incidents []security.SecurityIncident
		err := json.Unmarshal(rec.Body.Bytes(), &incidents)
		if err != nil {
			t.Fatalf("erro ao fazer parse do JSON: %v", err)
		}

		if len(incidents) < 1 {
			t.Fatal("esperava pelo menos 1 incidente listado")
		}

		// O primeiro retornado (ordem reversa) deve ser o que adicionamos
		found := false
		for _, inc := range incidents {
			if inc.Signature == "(?i)(eval\\s*\\()" {
				found = true
				if !strings.Contains(inc.Preview, "eval(req.body)") {
					t.Errorf("preview incorreto, obteve: %s", inc.Preview)
				}
				break
			}
		}

		if !found {
			t.Error("incidente adicionado não encontrado no log de CVEs")
		}
	})
}

func TestAuthRateLimitingAndCleanup(t *testing.T) {
	authLimiterMu.Lock()
	// Limpar estados existentes
	authStates = make(map[string]*authState)
	authLimiterMu.Unlock()

	ip := "1.2.3.4"

	// 1. Verificacao inicial deve passar
	if err := checkAuthRateLimit(ip); err != nil {
		t.Errorf("esperava nenhum rate limit inicialmente, obteve %v", err)
	}

	// 2. 4 tentativas falhas nao devem bloquear
	for i := 0; i < 4; i++ {
		recordFailedAuth(ip)
	}
	if err := checkAuthRateLimit(ip); err != nil {
		t.Errorf("esperava nenhum rate limit apos 4 falhas, obteve %v", err)
	}

	// 3. A 5a tentativa falha deve bloquear
	recordFailedAuth(ip)
	if err := checkAuthRateLimit(ip); err == nil {
		t.Error("esperava rate limit lockout apos 5 falhas, mas obteve nil")
	}

	// 4. Autenticacao com sucesso deve limpar o bloqueio
	recordSuccessfulAuth(ip)
	if err := checkAuthRateLimit(ip); err != nil {
		t.Errorf("esperava que login correto limpasse o lockout, obteve %v", err)
	}

	// 5. Testar a rotina de limpeza de inativos
	authLimiterMu.Lock()
	authStates["expired-ip"] = &authState{
		failedAttempts: 3,
		lastAttempt:    time.Now().Add(-2 * time.Hour),
	}
	authStates["active-ip"] = &authState{
		failedAttempts: 3,
		lastAttempt:    time.Now(),
	}
	authLimiterMu.Unlock()

	// Simula a execucao síncrona do loop de cleanup
	authLimiterMu.Lock()
	now := time.Now()
	for k, state := range authStates {
		if now.Sub(state.lastAttempt) > 1*time.Hour {
			delete(authStates, k)
		}
	}
	authLimiterMu.Unlock()

	authLimiterMu.Lock()
	_, activeExists := authStates["active-ip"]
	_, expiredExists := authStates["expired-ip"]
	authLimiterMu.Unlock()

	if !activeExists {
		t.Error("esperava que o IP ativo permanecesse apos o cleanup")
	}
	if expiredExists {
		t.Error("esperava que o IP expirado fosse removido apos o cleanup")
	}
}

func TestProtocolsConfigAPI(t *testing.T) {
	cfg := &config.Config{}
	cfg.WebUI.Enabled = true
	cfg.WebUI.Port = 9091
	cfg.WebUI.Token = "test-token"

	server, err := NewAdminServer(cfg)
	if err != nil {
		t.Fatalf("erro ao instanciar AdminServer: %v", err)
	}

	// Criar KVStore em memória para os testes
	kv, err := runtime.NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("erro ao criar KVStore em memoria: %v", err)
	}
	defer kv.Close()

	server.SetKVStore(kv)

	// 1. Validar GET Inicial (Defaults)
	reqGet := httptest.NewRequest("GET", "/api/protocols/config", nil)
	recGet := httptest.NewRecorder()
	server.handleProtocolsConfig(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Errorf("esperava status 200 no GET inicial, obteve %d", recGet.Code)
	}

	var initialCfg ProtocolConfig
	if err := json.Unmarshal(recGet.Body.Bytes(), &initialCfg); err != nil {
		t.Fatalf("erro ao decodificar JSON do GET inicial: %v", err)
	}

	if initialCfg.DNS.Enabled || initialCfg.UDP.Enabled || initialCfg.QUIC.Enabled {
		t.Error("esperava protocolos desabilitados por padrao")
	}

	if initialCfg.DNS.Port != 53 || initialCfg.UDP.Port != 8443 {
		t.Errorf("portas padrao invalidas, DNS: %d, UDP: %d", initialCfg.DNS.Port, initialCfg.UDP.Port)
	}

	// 2. Validar POST (Gravação de novas configurações com registros)
	newCfg := ProtocolConfig{
		DNS: DNSConfig{
			Enabled:  true,
			Port:     8053,
			Upstream: "8.8.8.8:53",
			Records: []DNSRecord{
				{ID: "rec1", Domain: "test.local", Type: "A", Address: "10.0.0.1", TTL: 60},
			},
		},
		UDP: UDPConfig{
			Enabled:        true,
			Port:           9000,
			SessionTimeout: 15,
			Routes: []UDPRoute{
				{ID: "route1", Source: "192.168.1.100", Dest: "10.0.0.2:9000", Action: "reroute"},
			},
		},
		QUIC: QUICConfig{
			Enabled:         true,
			Port:            9443,
			BypassDomains:   []string{"*.bypass.com"},
			GroGso:          true,
			EbpfRedirection: true,
		},
	}

	bodyBytes, _ := json.Marshal(newCfg)
	reqPost := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(bodyBytes))
	recPost := httptest.NewRecorder()
	server.handleProtocolsConfig(recPost, reqPost)

	if recPost.Code != http.StatusOK {
		t.Errorf("esperava status 200 no POST, obteve %d", recPost.Code)
	}

	// 3. Validar GET Novamente (Verificar persistência de dados no KVStore)
	reqGet2 := httptest.NewRequest("GET", "/api/protocols/config", nil)
	recGet2 := httptest.NewRecorder()
	server.handleProtocolsConfig(recGet2, reqGet2)

	if recGet2.Code != http.StatusOK {
		t.Errorf("esperava status 200 no segundo GET, obteve %d", recGet2.Code)
	}

	var savedCfg ProtocolConfig
	if err := json.Unmarshal(recGet2.Body.Bytes(), &savedCfg); err != nil {
		t.Fatalf("erro ao decodificar JSON do segundo GET: %v", err)
	}

	if !savedCfg.DNS.Enabled || !savedCfg.UDP.Enabled || !savedCfg.QUIC.Enabled {
		t.Error("esperava protocolos habilitados apos POST")
	}

	if len(savedCfg.DNS.Records) != 1 || savedCfg.DNS.Records[0].Domain != "test.local" {
		t.Errorf("registro DNS nao persistido corretamente: %+v", savedCfg.DNS.Records)
	}

	if len(savedCfg.UDP.Routes) != 1 || savedCfg.UDP.Routes[0].Source != "192.168.1.100" {
		t.Errorf("rota UDP nao persistida corretamente: %+v", savedCfg.UDP.Routes)
	}

	if !savedCfg.QUIC.GroGso || !savedCfg.QUIC.EbpfRedirection || savedCfg.QUIC.BypassDomains[0] != "*.bypass.com" {
		t.Errorf("configuracoes QUIC nao persistidas corretamente: %+v", savedCfg.QUIC)
	}

	// 4. Validar Rejeição de Portas Inválidas
	badPortCfg := savedCfg
	badPortCfg.DNS.Port = 999999 // Porta acima do limite
	bodyBytesBadPort, _ := json.Marshal(badPortCfg)
	reqPostBadPort := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(bodyBytesBadPort))
	recPostBadPort := httptest.NewRecorder()
	server.handleProtocolsConfig(recPostBadPort, reqPostBadPort)
	if recPostBadPort.Code != http.StatusBadRequest {
		t.Errorf("esperava status 400 para porta invalida, obteve %d", recPostBadPort.Code)
	}

	// 5. Validar Rejeição de IDs Maliciosos (Sanitização XSS)
	badIDCfg := savedCfg
	badIDCfg.DNS.Records = []DNSRecord{
		{ID: "dns_' onclick='alert(1)", Domain: "xss.local", Type: "A", Address: "1.1.1.1", TTL: 30},
	}
	bodyBytesBadID, _ := json.Marshal(badIDCfg)
	reqPostBadID := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(bodyBytesBadID))
	recPostBadID := httptest.NewRecorder()
	server.handleProtocolsConfig(recPostBadID, reqPostBadID)
	if recPostBadID.Code != http.StatusBadRequest {
		t.Errorf("esperava status 400 para ID com caracteres especiais (XSS), obteve %d", recPostBadID.Code)
	}

	// 6. Validar Rejeição de TTL Negativo
	badTTLCfg := savedCfg
	badTTLCfg.DNS.Records = []DNSRecord{
		{ID: "rec1", Domain: "ttl.local", Type: "A", Address: "1.1.1.1", TTL: -60},
	}
	bodyBytesBadTTL, _ := json.Marshal(badTTLCfg)
	reqPostBadTTL := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(bodyBytesBadTTL))
	recPostBadTTL := httptest.NewRecorder()
	server.handleProtocolsConfig(recPostBadTTL, reqPostBadTTL)
	if recPostBadTTL.Code != http.StatusBadRequest {
		t.Errorf("esperava status 400 para TTL negativo, obteve %d", recPostBadTTL.Code)
	}

	// 7. Validar Rejeição de Injeção de Comando em Domínios de Bypass do QUIC
	badDomainCfg := savedCfg
	badDomainCfg.QUIC.BypassDomains = []string{"*.stripe.com; rm -rf /"}
	bodyBytesBadDomain, _ := json.Marshal(badDomainCfg)
	reqPostBadDomain := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(bodyBytesBadDomain))
	recPostBadDomain := httptest.NewRecorder()
	server.handleProtocolsConfig(recPostBadDomain, reqPostBadDomain)
	if recPostBadDomain.Code != http.StatusBadRequest {
		t.Errorf("esperava status 400 para dominio malicioso, obteve %d", recPostBadDomain.Code)
	}

	// 8. Validar Rejeição de Limite Quantitativo (Mais de 1000 registros)
	tooManyRecordsCfg := savedCfg
	tooManyRecordsCfg.DNS.Records = make([]DNSRecord, 1001)
	for i := 0; i < 1001; i++ {
		tooManyRecordsCfg.DNS.Records[i] = DNSRecord{ID: fmt.Sprintf("rec%d", i), Domain: "test.local", Type: "A", Address: "1.1.1.1", TTL: 60}
	}
	bodyBytesTooMany, _ := json.Marshal(tooManyRecordsCfg)
	reqPostTooMany := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(bodyBytesTooMany))
	recPostTooMany := httptest.NewRecorder()
	server.handleProtocolsConfig(recPostTooMany, reqPostTooMany)
	if recPostTooMany.Code != http.StatusBadRequest {
		t.Errorf("esperava status 400 para excesso de registros, obteve %d", recPostTooMany.Code)
	}

	// 9. Validar Proteção contra Payload Gigante (DoS/OOM - Max 1MB)
	giantBytes := make([]byte, 1048576 * 2) // 2MB de zeros
	reqPostGiant := httptest.NewRequest("POST", "/api/protocols/config", bytes.NewReader(giantBytes))
	recPostGiant := httptest.NewRecorder()
	server.handleProtocolsConfig(recPostGiant, reqPostGiant)
	if recPostGiant.Code != http.StatusBadRequest {
		t.Errorf("esperava status 400 para payload gigante, obteve %d", recPostGiant.Code)
	}

	// 10. Validar Endpoint de Limpeza do Cache DNS
	reqClear := httptest.NewRequest("POST", "/api/protocols/dns/clear-cache", nil)
	recClear := httptest.NewRecorder()
	server.handleDNSClearCache(recClear, reqClear)
	if recClear.Code != http.StatusOK {
		t.Errorf("esperava status 200 na limpeza de cache DNS, obteve %d", recClear.Code)
	}
}
