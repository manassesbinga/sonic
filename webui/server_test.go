package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manassesbinga/sonic/config"
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
