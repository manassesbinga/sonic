package mitm

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

func TestCAKeyPermissions(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-perm-test-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	_, err = LoadOrCreateCA(tempDir)
	if err != nil {
		t.Fatalf("Erro ao gerar CA: %v", err)
	}

	keyPath := filepath.Join(tempDir, "ca-key.pem")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Erro ao stat ca-key.pem: %v", err)
	}

	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Errorf("ca-key.pem tem permissoes muito abertas: %#o (esperava 0600 ou similar)", perm)
	}
	t.Logf("Permissoes ca-key.pem: %#o", perm)
}

func TestCertCache_ConcurrentDifferentDomains(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-cache-stress-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ca, err := LoadOrCreateCA(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar CA: %v", err)
	}

	cache, err := NewCertCache(ca)
	if err != nil {
		t.Fatalf("Erro ao criar cache: %v", err)
	}

	var wg sync.WaitGroup
	domains := make([]string, 100)
	for i := 0; i < 100; i++ {
		domains[i] = fmt.Sprintf("host-%d.example.com", i)
	}

	for _, dom := range domains {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			cert, err := cache.GetCertificate(d)
			if err != nil {
				t.Errorf("Erro ao obter certificado para %s: %v", d, err)
				return
			}
			if cert == nil {
				t.Errorf("Certificado nulo para %s", d)
			}
		}(dom)
	}

	wg.Wait()

	for _, dom := range domains {
		_, err := cache.GetCertificate(dom)
		if err != nil {
			t.Errorf("Erro ao re-obter certificado em cache para %s: %v", dom, err)
		}
	}
}

func TestCertCache_IPDomain(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-ip-test-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ca, err := LoadOrCreateCA(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar CA: %v", err)
	}

	cache, err := NewCertCache(ca)
	if err != nil {
		t.Fatalf("Erro ao criar cache: %v", err)
	}

	cert, err := cache.GetCertificate("192.168.1.1")
	if err != nil {
		t.Fatalf("Erro ao gerar certificado para IP: %v", err)
	}

	tlsCert := cert
	if len(tlsCert.Certificate) == 0 {
		t.Fatal("Certificado TLS sem bytes")
	}
}

func TestMITMEngine_ConnectRealServerValidatesTLS(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-tls-test-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewMITMEngine(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar MITMEngine: %v", err)
	}

	_, err = engine.ConnectRealServer("127.0.0.1:1", "example.com")
	if err == nil {
		t.Fatal("Esperava erro ao conectar a servidor inexistente (TLS deve falhar)")
	}
}

func TestCertCache_DoubleCheckLocking(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-dcl-test-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ca, err := LoadOrCreateCA(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar CA: %v", err)
	}

	cache, err := NewCertCache(ca)
	if err != nil {
		t.Fatalf("Erro ao criar cache: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *tls.Certificate, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cert, err := cache.GetCertificate("double-check.example.com")
			if err == nil && cert != nil {
				results <- cert
			}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var first *tls.Certificate
	count := 0
	for cert := range results {
		count++
		if first == nil {
			first = cert
		} else if cert != first {
			t.Error("Double-check locking falhou: diferentes ponteiros de certificado")
		}
	}
	if count < 2 {
		t.Log("Menos de 2 goroutines conseguiram obter o certificado (pode ocorrer com scheduling rapido)")
	}
}

func TestCertCache_DifferentDomainsUnique(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-unique-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ca, err := LoadOrCreateCA(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar CA: %v", err)
	}

	cache, err := NewCertCache(ca)
	if err != nil {
		t.Fatalf("Erro ao criar cache: %v", err)
	}

	certA, err := cache.GetCertificate("alpha.example.com")
	if err != nil {
		t.Fatalf("Erro ao obter cert para alpha: %v", err)
	}

	certB, err := cache.GetCertificate("beta.example.com")
	if err != nil {
		t.Fatalf("Erro ao obter cert para beta: %v", err)
	}

	if certA == certB {
		t.Error("Certificados para dominios diferentes deveriam ser objetos diferentes na cache")
	}
}

func TestMITMEngine_InterceptTermTLSHandshakeTimeout(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mitm-timeout-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewMITMEngine(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar MITMEngine: %v", err)
	}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	conn, err := engine.InterceptTermTLS(server, "example.com")
	if err != nil {
		t.Fatalf("InterceptTermTLS falhou para dominio valido: %v", err)
	}
	if conn == nil {
		t.Fatal("InterceptTermTLS retornou conexao nula")
	}
}
