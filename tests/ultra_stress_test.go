package sonic_test

import (
	"fmt"
	go_runtime "runtime"
	"runtime/debug"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manassesbinga/sonic/mitm"
	"github.com/manassesbinga/sonic/runtime"
)

// ============================================================================
// SONIC ULTRA-STRESS: 100.000+ operações concorrentes simultâneas
// ============================================================================

type UltraStats struct {
	Nome          string
	N             int
	Sucesso       int64
	Erros         int64
	TempoTotal    time.Duration
	Throughput    float64
	Media         time.Duration
	DesvioPadrao  time.Duration
	P50           time.Duration
	P90           time.Duration
	P95           time.Duration
	P99           time.Duration
	P999          time.Duration
	Min           time.Duration
	Max           time.Duration
	MemAllocMB    float64
	NumGC         uint32
}

func computeUltraStats(nome string, tempos []time.Duration, total time.Duration, mA, mD *go_runtime.MemStats) UltraStats {
	n := len(tempos)
	if n == 0 {
		return UltraStats{Nome: nome}
	}
	sort.Slice(tempos, func(i, j int) bool { return tempos[i] < tempos[j] })

	var soma time.Duration
	for _, t := range tempos {
		soma += t
	}
	media := soma / time.Duration(n)

	var sv float64
	mn := float64(media.Nanoseconds())
	for _, t := range tempos {
		d := float64(t.Nanoseconds()) - mn
		sv += d * d
	}
	dp := time.Duration(math.Sqrt(sv / float64(n)))

	idx := func(p float64) int {
		i := int(float64(n) * p)
		if i >= n {
			i = n - 1
		}
		return i
	}

	memMB := float64(0)
	gcs := uint32(0)
	if mA != nil && mD != nil {
		memMB = float64(mD.TotalAlloc-mA.TotalAlloc) / 1024 / 1024
		gcs = mD.NumGC - mA.NumGC
	}

	return UltraStats{
		Nome: nome, N: n, TempoTotal: total,
		Throughput:   float64(n) / total.Seconds(),
		Media:        media,
		DesvioPadrao: dp,
		P50: tempos[idx(0.50)], P90: tempos[idx(0.90)],
		P95: tempos[idx(0.95)], P99: tempos[idx(0.99)],
		P999: tempos[idx(0.999)],
		Min: tempos[0], Max: tempos[n-1],
		MemAllocMB: memMB, NumGC: gcs,
	}
}

// ============================================================================
// TESTE 1: 100K Feature Flags Concorrentes
// ============================================================================

func TestUltra_100K_FeatureFlags(t *testing.T) {
	totalOps := 100_000
	poolSize := 128
	t.Logf("🔥 ULTRA STRESS: %d Feature Flag evaluations concorrentes (pool=%d)", totalOps, poolSize)

	jsCode := `
	function onTraffic(req) {
		var u = req.headers["x-user-id"] || "anon";
		var c = req.headers["x-country"] || "US";
		var d = req.headers["x-device"] || "desktop";
		var v = "control";
		if (c === "BR" && d === "mobile") {
			v = u.indexOf("alpha") === 0 ? "br-mobile-a" : "br-mobile-b";
		} else if (c === "US" && d === "desktop") {
			v = u.indexOf("beta") === 0 ? "us-desk-a" : "us-desk-b";
		} else if (c === "PT") {
			v = "pt-special";
		} else if (u.indexOf("internal") === 0) {
			v = "internal";
		}
		req.headers["x-variant"] = v;
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, poolSize)
	if err != nil {
		t.Fatalf("Engine: %v", err)
	}

	tempos := make([]time.Duration, totalOps)
	var sucesso, erros int64

	debug.FreeOSMemory()
	var mA, mD go_runtime.MemStats
	go_runtime.ReadMemStats(&mA)

	var wg sync.WaitGroup
	start := time.Now()

	sem := make(chan struct{}, 512) // Limitar concorrência ativa nas VMs Goja para evitar OOM
	for i := 0; i < totalOps; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}
			countries := []string{"BR", "US", "PT", "DE", "JP"}
			devices := []string{"mobile", "desktop", "tablet"}
			req := &runtime.Request{
				Method: "GET",
				Headers: map[string]string{
					"x-user-id": fmt.Sprintf("alpha-user-%d", id),
					"x-country": countries[id%5],
					"x-device":  devices[id%3],
				},
			}
			t0 := time.Now()
			_, err := engine.RunOnTraffic(req)
			elapsed := time.Since(t0)
			<-sem
			if err != nil {
				atomic.AddInt64(&erros, 1)
			} else {
				atomic.AddInt64(&sucesso, 1)
			}
			tempos[id] = elapsed
		}(i)
	}

	wg.Wait()
	dur := time.Since(start)
	go_runtime.ReadMemStats(&mD)

	valid := make([]time.Duration, 0, totalOps)
	for _, d := range tempos {
		if d > 0 {
			valid = append(valid, d)
		}
	}
	st := computeUltraStats("100K Feature Flags", valid, dur, &mA, &mD)
	st.Sucesso = sucesso
	st.Erros = erros

	t.Logf("✅ Sucesso: %d | ❌ Erros: %d", sucesso, erros)
	t.Logf("⚡ Throughput: %.2f ops/s", st.Throughput)
	t.Logf("📊 p50=%v | p95=%v | p99=%v | p999=%v", st.P50, st.P95, st.P99, st.P999)
	t.Logf("🧠 RAM: %.2f MB | GC: %d colectas", st.MemAllocMB, st.NumGC)
	t.Logf("⏱️ Total: %v", dur)
}

// ============================================================================
// TESTE 2: 100K WAF Blocks Concorrentes
// ============================================================================

func TestUltra_100K_WAF(t *testing.T) {
	totalOps := 100_000
	poolSize := 128
	t.Logf("🔥 ULTRA STRESS: %d WAF block evaluations concorrentes (pool=%d)", totalOps, poolSize)

	jsCode := `
	function onTraffic(req) {
		var p = req.path || "";
		var ua = req.headers["user-agent"] || "";
		if (p.indexOf("../") !== -1 || p.indexOf("etc/passwd") !== -1) {
			return { _isResponse: true, status: 403, headers: {"X-WAF": "path-traversal"}, body: "blocked" };
		}
		if (ua.indexOf("<script") !== -1 || ua.indexOf("javascript:") !== -1) {
			return { _isResponse: true, status: 403, headers: {"X-WAF": "xss"}, body: "blocked" };
		}
		if (p.indexOf("' OR ") !== -1 || p.indexOf("--") !== -1 || p.indexOf("DROP TABLE") !== -1) {
			return { _isResponse: true, status: 403, headers: {"X-WAF": "sqli"}, body: "blocked" };
		}
		req.headers["x-waf-check"] = "passed";
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, poolSize)
	if err != nil {
		t.Fatalf("Engine: %v", err)
	}

	attacks := []struct {
		path string
		ua   string
		block bool
	}{
		{"/api/data", "Mozilla/5.0", false},
		{"/../../../etc/passwd", "curl/7.0", true},
		{"/search?q=' OR 1=1--", "Mozilla/5.0", true},
		{"/page", "<script>alert(1)</script>", true},
		{"/normal", "Chrome/120", false},
	}

	tempos := make([]time.Duration, totalOps)
	var sucesso, erros, bloqueados int64

	debug.FreeOSMemory()
	var mA, mD go_runtime.MemStats
	go_runtime.ReadMemStats(&mA)

	var wg sync.WaitGroup
	start := time.Now()

	sem := make(chan struct{}, 512)
	for i := 0; i < totalOps; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}
			atk := attacks[id%len(attacks)]
			req := &runtime.Request{
				Method: "GET",
				Path:   atk.path,
				Headers: map[string]string{
					"user-agent": atk.ua,
				},
			}
			t0 := time.Now()
			res, err := engine.RunOnTraffic(req)
			elapsed := time.Since(t0)
			<-sem
			if err != nil {
				atomic.AddInt64(&erros, 1)
			} else {
				atomic.AddInt64(&sucesso, 1)
				if res.IsResponse && res.Status == 403 {
					atomic.AddInt64(&bloqueados, 1)
				}
			}
			tempos[id] = elapsed
		}(i)
	}

	wg.Wait()
	dur := time.Since(start)
	go_runtime.ReadMemStats(&mD)

	valid := make([]time.Duration, 0, totalOps)
	for _, d := range tempos {
		if d > 0 {
			valid = append(valid, d)
		}
	}
	st := computeUltraStats("100K WAF Checks", valid, dur, &mA, &mD)

	t.Logf("✅ Sucesso: %d | ❌ Erros: %d | 🛡️ Bloqueados: %d", sucesso, erros, bloqueados)
	t.Logf("⚡ Throughput: %.2f ops/s", st.Throughput)
	t.Logf("📊 p50=%v | p95=%v | p99=%v | p999=%v", st.P50, st.P95, st.P99, st.P999)
	t.Logf("🧠 RAM: %.2f MB | GC: %d", st.MemAllocMB, st.NumGC)
	t.Logf("⏱️ Total: %v", dur)
}

// ============================================================================
// TESTE 3: 100K Routing Decisions Concorrentes
// ============================================================================

func TestUltra_100K_Routing(t *testing.T) {
	totalOps := 100_000
	poolSize := 128
	t.Logf("🔥 ULTRA STRESS: %d routing decisions concorrentes (pool=%d)", totalOps, poolSize)

	jsCode := `
	function onTraffic(req) {
		var p = req.path || "/";
		if (p.indexOf("/api/v2/") === 0) {
			req.headers["x-upstream"] = "api-v2-cluster";
			req.headers["x-weight"] = "80";
		} else if (p.indexOf("/api/v1/") === 0) {
			req.headers["x-upstream"] = "api-v1-legacy";
			req.headers["x-weight"] = "20";
		} else if (p.indexOf("/static/") === 0) {
			req.headers["x-upstream"] = "cdn-origin";
			req.headers["x-cache"] = "HIT";
		} else if (p.indexOf("/ws/") === 0) {
			req.headers["x-upstream"] = "websocket-pool";
		} else {
			req.headers["x-upstream"] = "default-backend";
		}
		req.headers["x-request-id"] = Date.now().toString();
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, poolSize)
	if err != nil {
		t.Fatalf("Engine: %v", err)
	}

	paths := []string{"/api/v2/users", "/api/v1/legacy", "/static/img.png", "/ws/chat", "/home", "/api/v2/orders", "/dashboard"}

	tempos := make([]time.Duration, totalOps)
	var sucesso, erros int64

	debug.FreeOSMemory()
	var mA, mD go_runtime.MemStats
	go_runtime.ReadMemStats(&mA)

	var wg sync.WaitGroup
	start := time.Now()

	sem := make(chan struct{}, 512)
	for i := 0; i < totalOps; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}
			req := &runtime.Request{
				Method:  "GET",
				Path:    paths[id%len(paths)],
				Headers: map[string]string{},
			}
			t0 := time.Now()
			_, err := engine.RunOnTraffic(req)
			elapsed := time.Since(t0)
			<-sem
			if err != nil {
				atomic.AddInt64(&erros, 1)
			} else {
				atomic.AddInt64(&sucesso, 1)
			}
			tempos[id] = elapsed
		}(i)
	}

	wg.Wait()
	dur := time.Since(start)
	go_runtime.ReadMemStats(&mD)

	valid := make([]time.Duration, 0, totalOps)
	for _, d := range tempos {
		if d > 0 {
			valid = append(valid, d)
		}
	}
	st := computeUltraStats("100K Routing", valid, dur, &mA, &mD)

	t.Logf("✅ Sucesso: %d | ❌ Erros: %d", sucesso, erros)
	t.Logf("⚡ Throughput: %.2f ops/s", st.Throughput)
	t.Logf("📊 p50=%v | p95=%v | p99=%v | p999=%v", st.P50, st.P95, st.P99, st.P999)
	t.Logf("🧠 RAM: %.2f MB | GC: %d", st.MemAllocMB, st.NumGC)
	t.Logf("⏱️ Total: %v", dur)
}

// ============================================================================
// TESTE 4: 100K Header Injections Concorrentes
// ============================================================================

func TestUltra_100K_HeaderInjection(t *testing.T) {
	totalOps := 100_000
	poolSize := 128
	t.Logf("🔥 ULTRA STRESS: %d header injection ops concorrentes (pool=%d)", totalOps, poolSize)

	jsCode := `
	function onTraffic(req) {
		req.headers["x-trace-id"] = Date.now().toString(36) + Math.random().toString(36).substr(2);
		req.headers["x-region"] = "eu-west-1";
		req.headers["x-version"] = "v2.1.0";
		req.headers["x-edge"] = "sonic";
		req.headers["x-timestamp"] = new Date().toISOString();
		return req;
	}
	function onResponse(res) {
		res.headers["x-powered-by"] = "Sonic Edge Engine";
		res.headers["x-cache-status"] = "MISS";
		return res;
	}`

	engine, err := runtime.NewJSEngine(jsCode, 500, poolSize)
	if err != nil {
		t.Fatalf("Engine: %v", err)
	}

	tempos := make([]time.Duration, totalOps)
	var sucesso, erros int64

	debug.FreeOSMemory()
	var mA, mD go_runtime.MemStats
	go_runtime.ReadMemStats(&mA)

	var wg sync.WaitGroup
	start := time.Now()

	sem := make(chan struct{}, 512)
	for i := 0; i < totalOps; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}
			req := &runtime.Request{
				Method: "GET",
				Path:   fmt.Sprintf("/resource/%d", id),
				Headers: map[string]string{
					"host":       fmt.Sprintf("shard-%d.example.com", id%10),
					"user-agent": "SonicBench/1.0",
				},
			}
			t0 := time.Now()
			_, err := engine.RunOnTraffic(req)
			elapsed := time.Since(t0)
			<-sem
			if err != nil {
				atomic.AddInt64(&erros, 1)
			} else {
				atomic.AddInt64(&sucesso, 1)
			}
			tempos[id] = elapsed
		}(i)
	}

	wg.Wait()
	dur := time.Since(start)
	go_runtime.ReadMemStats(&mD)

	valid := make([]time.Duration, 0, totalOps)
	for _, d := range tempos {
		if d > 0 {
			valid = append(valid, d)
		}
	}
	st := computeUltraStats("100K Headers", valid, dur, &mA, &mD)

	t.Logf("✅ Sucesso: %d | ❌ Erros: %d", sucesso, erros)
	t.Logf("⚡ Throughput: %.2f ops/s", st.Throughput)
	t.Logf("📊 p50=%v | p95=%v | p99=%v | p999=%v", st.P50, st.P95, st.P99, st.P999)
	t.Logf("🧠 RAM: %.2f MB | GC: %d", st.MemAllocMB, st.NumGC)
	t.Logf("⏱️ Total: %v", dur)
}

// ============================================================================
// TESTE 5: 10K Cert Generations Concorrentes (Estresse MITM Extremo)
// ============================================================================

func TestUltra_10K_CertGen(t *testing.T) {
	totalOps := 10_000
	t.Logf("🔥 ULTRA STRESS: %d TLS certificate generations concorrentes", totalOps)

	ca, err := mitm.LoadOrCreateCA("./certs")
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	cache, err := mitm.NewCertCache(ca)
	if err != nil {
		t.Fatalf("Cache: %v", err)
	}

	tempos := make([]time.Duration, totalOps)
	var sucesso, erros int64

	debug.FreeOSMemory()
	var mA, mD go_runtime.MemStats
	go_runtime.ReadMemStats(&mA)

	var wg sync.WaitGroup
	start := time.Now()

	sem := make(chan struct{}, 256) // Limitar concorrência de geração de certificados TLS
	for i := 0; i < totalOps; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}
			domain := fmt.Sprintf("ultra-%05d.thesis.example.com", id)
			t0 := time.Now()
			_, err := cache.GetCertificate(domain)
			elapsed := time.Since(t0)
			<-sem
			if err != nil {
				atomic.AddInt64(&erros, 1)
			} else {
				atomic.AddInt64(&sucesso, 1)
			}
			tempos[id] = elapsed
		}(i)
	}

	wg.Wait()
	dur := time.Since(start)
	go_runtime.ReadMemStats(&mD)

	valid := make([]time.Duration, 0, totalOps)
	for _, d := range tempos {
		if d > 0 {
			valid = append(valid, d)
		}
	}
	st := computeUltraStats("10K Certs", valid, dur, &mA, &mD)

	t.Logf("✅ Sucesso: %d | ❌ Erros: %d", sucesso, erros)
	t.Logf("⚡ Throughput: %.2f certs/s", st.Throughput)
	t.Logf("📊 p50=%v | p95=%v | p99=%v | p999=%v", st.P50, st.P95, st.P99, st.P999)
	t.Logf("🧠 RAM: %.2f MB | GC: %d", st.MemAllocMB, st.NumGC)
	t.Logf("⏱️ Total: %v", dur)
}

// ============================================================================
// TESTE 6: Gravação do Relatório Ultra-Stress
// ============================================================================

func TestUltra_WriteReport(t *testing.T) {
	resultsDir := filepath.Join(".", "results")
	_ = os.MkdirAll(resultsDir, 0755)
	reportPath := filepath.Join(resultsDir, "ultra_stress_report.md")

	hostname, _ := os.Hostname()

	report := fmt.Sprintf(`# Relatório Ultra-Stress: 100K+ Operações Concorrentes Simultâneas

**Sonic Edge Engine — Teste de Limites Absolutos da Máquina**

---

## Infraestrutura

- **Host**: %s
- **Data**: %s
- **Pool Size**: 128 VMs Goja
- **Operações por Teste**: 100.000 (goroutines simultâneas)

---

## Resumo de Execução

Os 5 testes de ultra-stress foram projectados para lançar **100.000 goroutines em simultâneo** contra o pool de VMs JavaScript do Sonic, forçando o scheduler do Go e o garbage collector a operar sob pressão extrema.

### Cenários Testados

| # | Cenário | Goroutines | Objectivo |
| :--- | :--- | :--- | :--- |
| 1 | Feature Flags (Targeting Complexo) | 100.000 | Avaliar regras de segmentação multi-condição |
| 2 | WAF (Firewall de Aplicação) | 100.000 | Detectar SQL injection, XSS, path traversal |
| 3 | Routing (Decisões de Encaminhamento) | 100.000 | Rotear para clusters com base em paths |
| 4 | Header Injection (Injecção de Cabeçalhos) | 100.000 | Adicionar trace IDs, timestamps, metadados |
| 5 | Cert Generation (Certificados TLS) | 10.000 | Gerar X.509 certs dinâmicos concorrentes |

> Os resultados detalhados de cada teste estão nos logs de execução do Go test.

---
*Gerado automaticamente pelo Sonic Ultra-Stress Framework — %s*
`, hostname, time.Now().Format("2006-01-02 15:04:05"), time.Now().Format("Jan 2006"))

	err := os.WriteFile(reportPath, []byte(report), 0644)
	if err != nil {
		t.Fatalf("Erro: %v", err)
	}
	t.Logf("Relatório ultra-stress gerado em: %s", reportPath)
}
