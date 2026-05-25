package mitm

import (
	"os"
	"sync"
	"testing"
)

func TestMITMCAAndCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-test-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Testa geracao de CA Raiz
	ca, err := LoadOrCreateCA(tempDir)
	if err != nil {
		t.Fatalf("Erro ao carregar ou criar CA: %v", err)
	}

	if ca.Cert == nil || ca.PrivKey == nil {
		t.Fatal("CA inicializada incorretamente com dados nulos")
	}

	// 2. Testa cache concorrente de certificados
	cache, err := NewCertCache(ca)
	if err != nil {
		t.Fatalf("Erro ao criar cache de certificados: %v", err)
	}

	// Gera certificado para um dominio
	cert1, err := cache.GetCertificate("example.com")
	if err != nil {
		t.Fatalf("Erro ao obter certificado para example.com: %v", err)
	}

	// Pega novamente da cache
	cert2, err := cache.GetCertificate("example.com")
	if err != nil {
		t.Fatalf("Erro ao obter certificado na segunda chamada: %v", err)
	}

	// Valida se sao o mesmo ponteiro na cache
	if cert1 != cert2 {
		t.Error("Ponteiros diferentes! Cache nao funcionou corretamente.")
	}

	// 3. Stress Test de Concorrencia (Gera certificados em paralelo)
	var wg sync.WaitGroup
	numRoutines := 50
	domains := []string{"stripe.com", "google.com", "github.com", "apple.com", "amazon.com"}

	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			dom := domains[id%len(domains)]
			_, err := cache.GetCertificate(dom)
			if err != nil {
				t.Errorf("Erro ao obter certificado na thread %d para o dominio %s: %v", id, dom, err)
			}
		}(i)
	}

	wg.Wait()
}
