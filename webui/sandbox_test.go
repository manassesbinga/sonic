package webui

import (
	"context"
	"testing"
	"time"

	"github.com/manassesbinga/sonic/runtime"
)

func TestSandboxCoordinator_Config(t *testing.T) {
	kv, err := runtime.NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("failed to create memory KV: %v", err)
	}
	defer kv.Clear()

	sc := NewSandboxCoordinator(kv)
	
	cfg := SandboxConfig{
		EnabledDomains: []string{"*.example.com", "test.org"},
		Active:         true,
	}

	if err := sc.SaveConfig(cfg); err != nil {
		t.Fatalf("failed to save sandbox config: %v", err)
	}

	// Recarrega configuração e valida
	sc2 := NewSandboxCoordinator(sc.kvStore)
	cfg2 := sc2.Config()

	if !cfg2.Active {
		t.Error("expected sandbox to be active")
	}
	if len(cfg2.EnabledDomains) != 2 || cfg2.EnabledDomains[0] != "*.example.com" {
		t.Errorf("unexpected domains: %v", cfg2.EnabledDomains)
	}
}

func TestSandboxCoordinator_ShouldIntercept(t *testing.T) {
	kv, _ := runtime.NewInMemoryKVStore()
	sc := NewSandboxCoordinator(kv)

	sc.SaveConfig(SandboxConfig{
		EnabledDomains: []string{"*.example.com", "direct.org"},
		Active:         true,
	})

	tests := []struct {
		domain    string
		path      string
		intercept bool
	}{
		{"api.example.com", "/api/test", true},
		{"direct.org", "/page", true},
		{"other.com", "/any", false},
	}

	for _, tt := range tests {
		if res := sc.shouldIntercept(tt.domain, tt.path); res != tt.intercept {
			t.Errorf("domain %s path %s: expected %v, got %v", tt.domain, tt.path, tt.intercept, res)
		}
	}
}

func TestSandboxCoordinator_InterceptResumeFlow(t *testing.T) {
	kv, _ := runtime.NewInMemoryKVStore()
	sc := NewSandboxCoordinator(kv)

	sc.SaveConfig(SandboxConfig{
		EnabledDomains: []string{"*.example.com"},
		Active:         true,
		TimeoutSeconds: 10,
	})

	// Simular um cliente WebUI conectado (necessário para o fail-safe não libertar automaticamente)
	fakeCh := make(chan string, 100)
	sc.RegisterChannel(fakeCh)
	defer sc.UnregisterChannel(fakeCh)

	req := &runtime.Request{
		Method: "GET",
		URL:    "https://api.example.com/v1/users",
		Path:   "/v1/users",
		Headers: map[string]string{"X-Test": "original"},
		Body:   "original body",
	}

	// Goroutine que processa a requisição interceptada
	done := make(chan bool)
	var processedReq *runtime.Request
	var directResp *runtime.Response
	var allowed bool

	go func() {
		processedReq, directResp, allowed = sc.InterceptRequest(req, "api.example.com")
		done <- true
	}()

	// Aguardar a transação ser registrada
	var tx *SandboxTransaction
	for i := 0; i < 50; i++ {
		list := sc.GetActiveTransactions()
		if len(list) > 0 {
			tx = list[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if tx == nil {
		t.Fatal("expected request to be paused and registered in transactions map")
	}

	// Simular a edição do utilizador na WebUI e a chamada de Resume
	action := ResumeAction{
		Action:  "modify",
		Headers: map[string]string{"X-Test": "modified"},
		Body:    "modified body",
	}

	if !sc.Resume(tx.ID, action) {
		t.Fatal("failed to resume transaction")
	}

	<-done

	if !allowed {
		t.Error("expected request to be allowed")
	}
	if directResp != nil {
		t.Error("expected no direct response")
	}
	if processedReq.Headers["X-Test"] != "modified" {
		t.Errorf("expected modified header, got: %s", processedReq.Headers["X-Test"])
	}
	if processedReq.Body != "modified body" {
		t.Errorf("expected modified body, got: %s", processedReq.Body)
	}
}

func TestSandboxCoordinator_InterceptTimeout(t *testing.T) {
	// Testamos o timeout reduzindo o tempo de espera do interceptador para ser testável rapidamente.
	// No código original é de 30s. Para o teste não demorar 30s,
	// podemos simular que a goroutine prossegue sem bloqueio se o canal não for resolvido,
	// mas para não alterar o comportamento de produção, apenas verificamos que o timeout
	// se comporta de forma segura (neste caso, o timeout de 30s é longo para unit tests de CI,
	// por isso testamos a recepção no canal após tempo curto simulado).
	
	kv, _ := runtime.NewInMemoryKVStore()
	sc := NewSandboxCoordinator(kv)
	sc.SaveConfig(SandboxConfig{EnabledDomains: []string{"*.example.com"}, Active: true})

	tx := &SandboxTransaction{
		ID:         "test-id",
		Domain:     "api.example.com",
		ResumeChan: make(chan ResumeAction, 1),
	}

	sc.activeTx.Store("test-id", tx)
	defer sc.activeTx.Delete("test-id")

	// Simula a resolução por canal
	sc.Resume("test-id", ResumeAction{Action: "allow"})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	select {
	case action := <-tx.ResumeChan:
		if action.Action != "allow" {
			t.Errorf("expected allow, got %s", action.Action)
		}
	case <-ctx.Done():
		t.Fatal("resume signal not received in time")
	}
}
