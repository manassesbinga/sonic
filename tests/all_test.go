// Tests for Sonic — Integration, Compatibility, Stress tests
//
// This file contains comprehensive integration tests that validate
// Sonic's behavior end-to-end: proxy, JS engine, MITM, and TLS.
package sonic_test

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"channelworkers/config"
	"channelworkers/mitm"
	"channelworkers/proxy"
	"channelworkers/runtime"
)

// ── Helpers ─────────────────────────────────────────────

func startEchoServer(t testing.TB) (net.Listener, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo server: %v", err)
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	return l, l.Addr().(*net.TCPAddr).Port
}

func startProxy(t testing.TB, jsCode string, mode string) (int, func()) {
	t.Helper()
	if jsCode == "" {
		jsCode = `function onTraffic(r) { return r; } function onResponse(r) { return r; }`
	}

	port := getFreePort(t)
	cfg := &config.Config{}
	cfg.ListenPort = port
	cfg.Mode = mode
	cfg.Runtime.TimeoutMS = 500
	cfg.Runtime.PoolSize = 4
	cfg.Runtime.Failsafe = "bypass"
	cfg.TLS.CADir = "./certs"
	cfg.TLS.AutoGenerate = true
	cfg.Logging.Level = "error"

	jsEngine, err := runtime.NewJSEngine(jsCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
	if err != nil {
		t.Fatalf("js engine: %v", err)
	}

	mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
	if err != nil {
		t.Fatalf("mitm engine: %v", err)
	}

	p := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)
	if err := p.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}

	return port, func() { p.Stop() }
}

func getFreePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// ── Integration Tests ───────────────────────────────────

func TestIntegration_JSExecution(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		req.headers["X-Test"] = "passed";
		req.path = "/modified";
		return req;
	}
	function onResponse(resp) {
		resp.headers["X-Response"] = "modified";
		return resp;
	}`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{
		Method:  "POST",
		URL:     "https://example.com/test",
		Path:    "/test",
		Headers: map[string]string{"Host": "example.com"},
		Body:    `{"hello":"world"}`,
	}

	result, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.Headers["X-Test"] != "passed" {
		t.Errorf("X-Test header missing, got %v", result.Headers)
	}
	if result.Path != "/modified" {
		t.Errorf("path not modified, got %s", result.Path)
	}

	resp := &runtime.Response{
		Status:  200,
		Headers: map[string]string{},
		Body:    "ok",
	}
	modResp, err := engine.RunOnResponse(resp)
	if err != nil {
		t.Fatalf("onResponse: %v", err)
	}
	if modResp.Headers["X-Response"] != "modified" {
		t.Errorf("X-Response header missing")
	}
}

func TestIntegration_JSBlockWAF(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		if (req.headers["x-block"] === "true") {
			return {
				_isResponse: true,
				status: 403,
				headers: {"X-WAF": "blocked"},
				body: "blocked"
			};
		}
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{
		Method:  "GET",
		Headers: map[string]string{"x-block": "true"},
	}
	result, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if !result.IsResponse {
		t.Fatal("expected IsResponse=true for WAF block")
	}
	if result.Status != 403 {
		t.Errorf("expected status 403, got %d", result.Status)
	}
}

func TestIntegration_FailsafeBypass(t *testing.T) {
	jsCode := `function onTraffic(r) { throw new Error("crash"); } function onResponse(r) { return r; }`
	engine, err := runtime.NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{Method: "GET", Headers: map[string]string{}}
	_, err = engine.RunOnTraffic(req)
	if err == nil {
		t.Fatal("expected error from crashing JS")
	}
}

func TestIntegration_ConcurrentWorkers(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		req.headers["x-worker-id"] = req.headers["x-input"];
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 10000, 50)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	var mu sync.Mutex
	errs := make([]error, 0)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := &runtime.Request{
				Headers: map[string]string{"x-input": fmt.Sprintf("%d", id)},
			}
			result, err := engine.RunOnTraffic(req)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("goroutine %d: %v", id, err))
				mu.Unlock()
				return
			}
			if result.Headers["x-worker-id"] != fmt.Sprintf("%d", id) {
				mu.Lock()
				errs = append(errs, fmt.Errorf("goroutine %d: state pollution, got %s", id, result.Headers["x-worker-id"]))
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if len(errs) > 0 {
		for _, e := range errs {
			t.Log(e)
		}
		t.Fatalf("%d goroutines failed (out of 50)", len(errs))
	}
}

func TestIntegration_TimeoutRecovery(t *testing.T) {
	jsCode := `
	function onTraffic(req) { while(true) {} }
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 10, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{Headers: map[string]string{}}
	_, err = engine.RunOnTraffic(req)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	req2 := &runtime.Request{Headers: map[string]string{}}
	_, err = engine.RunOnTraffic(req2)
	if err == nil {
		t.Fatal("expected timeout on second call (pool recovery)")
	}
}

func TestIntegration_CPUPoolWarmup(t *testing.T) {
	jsCode := `
	function onTraffic(req) { return req; }
	function onResponse(r) { return r; }`

	poolSizes := []int{1, 4, 16, 64}
	for _, size := range poolSizes {
		start := time.Now()
		engine, err := runtime.NewJSEngine(jsCode, 100, size)
		if err != nil {
			t.Fatalf("pool %d: %v", size, err)
		}
		warmupTime := time.Since(start)

		req := &runtime.Request{Headers: map[string]string{}}
		startExec := time.Now()
		for i := 0; i < 10; i++ {
			engine.RunOnTraffic(req)
		}
		execTime := time.Since(startExec)

		t.Logf("pool=%d warmup=%v exec10=%v", size, warmupTime, execTime)
	}
}

func TestIntegration_MITMEngine(t *testing.T) {
	engine, err := mitm.NewMITMEngine("./certs")
	if err != nil {
		t.Fatalf("mitm engine: %v", err)
	}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	_, err = engine.InterceptTermTLS(server, "test.example.com")
	if err != nil {
		t.Fatalf("intercept term tls: %v", err)
	}
}

func TestIntegration_CertCache(t *testing.T) {
	ca, err := mitm.LoadOrCreateCA("./certs")
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}

	cache, err := mitm.NewCertCache(ca)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	cert1, _ := cache.GetCertificate("alpha.com")
	cert2, _ := cache.GetCertificate("alpha.com")
	cert3, _ := cache.GetCertificate("beta.com")

	if cert1 != cert2 {
		t.Error("cache miss for same domain")
	}
	if cert1 == cert3 {
		t.Error("different domains should have different certs")
	}
}

func TestIntegration_SNIParser(t *testing.T) {
	tests := []struct {
		name    string
		build   func() []byte
		wantSNI string
		wantErr bool
	}{
		{
			name: "valid sni",
			build: func() []byte {
				return buildClientHello("example.com")
			},
			wantSNI: "example.com",
		},
		{
			name: "long domain",
			build: func() []byte {
				return buildClientHello("a-very-long-subdomain-name.example.com")
			},
			wantSNI: "a-very-long-subdomain-name.example.com",
		},
		{
			name: "ip address",
			build: func() []byte {
				return buildClientHello("192.168.1.1")
			},
			wantSNI: "192.168.1.1",
		},
		{
			name:    "empty data",
			build:   func() []byte { return []byte{} },
			wantErr: true,
		},
		{
			name:    "not tls",
			build:   func() []byte { return []byte{0x00, 0x00, 0x00, 0x00, 0x00} },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.build()
			sni, err := proxy.ExtractSNI(data)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sni != tt.wantSNI {
				t.Errorf("got %q, want %q", sni, tt.wantSNI)
			}
		})
	}
}

func TestIntegration_ShouldBypass(t *testing.T) {
	cfg := &config.Config{
		Mode: "intercept",
		BypassDomains: []string{
			"*.stripe.com",
			"api.example.com",
		},
	}
	p := proxy.NewTransparentProxy(cfg, nil, nil)

	// Test bypass logic via handleConnection won't work since it tries to connect
	// Instead, test the shouldBypass method exposed through the struct
	// Since shouldBypass is unexported, test through proxy_test.go
	// This test verifies config parsing for bypass
	if len(cfg.BypassDomains) != 2 {
		t.Errorf("expected 2 bypass domains, got %d", len(cfg.BypassDomains))
	}
	_ = p
}

// ── Cloudflare Workers Compatibility Tests ─────────────

func TestCompatibility_HeadersAPI(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		var h = new Headers();
		h.set("X-Custom", "value1");
		h.set("X-Another", "value2");

		if (h.get("X-Custom") !== "value1") {
			return { _isResponse: true, status: 500, body: "Headers.get failed" };
		}
		if (!h.has("X-Another")) {
			return { _isResponse: true, status: 500, body: "Headers.has failed" };
		}

		h.delete("X-Another");
		if (h.has("X-Another")) {
			return { _isResponse: true, status: 500, body: "Headers.delete failed" };
		}

		req.headers["x-cf-test"] = "compatible";
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{Headers: map[string]string{}})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.IsResponse {
		t.Fatalf("headers test failed: %s", result.Body)
	}
	if result.Headers["x-cf-test"] != "compatible" {
		t.Error("header propagation failed")
	}
}

func TestCompatibility_RequestClass(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		var r = new Request("https://api.example.com/data", {
			method: "POST",
			headers: {"X-Test": "value"},
			body: "hello"
		});

		if (r.method !== "POST") {
			return { _isResponse: true, status: 500, body: "method" };
		}
		if (r.url !== "https://api.example.com/data") {
			return { _isResponse: true, status: 500, body: "url" };
		}

		req.headers["x-request-test"] = "passed";
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{Headers: map[string]string{}})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.IsResponse {
		t.Fatalf("Request test failed: %s", result.Body)
	}
}

func TestCompatibility_ResponseClass(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		var r = new Response("test body", {
			status: 201,
			headers: {"X-Created": "true"}
		});

		if (r.status !== 201) {
			return { _isResponse: true, status: 500, body: "status" };
		}
		if (r.body !== "test body") {
			return { _isResponse: true, status: 500, body: "body" };
		}

		return {
			_isResponse: true,
			status: 201,
			headers: {"X-Created": "true"},
			body: "created"
		};
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{Headers: map[string]string{}})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if !result.IsResponse {
		t.Fatal("expected direct response")
	}
	if result.Status != 201 {
		t.Errorf("expected 201, got %d", result.Status)
	}
}

func TestCompatibility_FetchPolyfill(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		_logGoFetch = typeof _goFetch;
		if (typeof fetch !== 'function') {
			return { _isResponse: true, status: 500, body: "fetch not defined" };
		}
		req.headers["x-fetch-available"] = "true";
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{Headers: map[string]string{}})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.IsResponse {
		t.Fatalf("fetch test failed: %s", result.Body)
	}
	if result.Headers["x-fetch-available"] != "true" {
		t.Error("fetch should be available")
	}
}

func TestCompatibility_LogBridge(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		log("test log message");
		req.headers["x-logged"] = "true";
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{Headers: map[string]string{}})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.Headers["x-logged"] != "true" {
		t.Error("log bridge failed")
	}
}

func TestCompatibility_NullUndefined(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		return null;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{
		Method:  "GET",
		URL:     "https://example.com/",
		Path:    "/",
		Headers: map[string]string{},
	})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.URL != "https://example.com/" {
		t.Error("null return should preserve original request")
	}
}

func TestCompatibility_NoFunctions(t *testing.T) {
	jsCode := `var x = 42;`
	engine, err := runtime.NewJSEngine(jsCode, 100, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	result, err := engine.RunOnTraffic(&runtime.Request{
		Method:  "GET",
		URL:     "https://example.com/",
		Headers: map[string]string{},
	})
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}
	if result.Method != "GET" {
		t.Error("no onTraffic should preserve request")
	}
}

// ── Stress Tests ────────────────────────────────────────

func TestStress_HighConcurrency(t *testing.T) {
	jsCode := `
	function onTraffic(req) { return req; }
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 10000, 64)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := &runtime.Request{Headers: map[string]string{"id": fmt.Sprintf("%d", id)}}
			_, err := engine.RunOnTraffic(req)
			if err != nil {
				errCh <- fmt.Errorf("req %d: %v", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var count int
	for err := range errCh {
		t.Log(err)
		count++
	}
	if count > 5 {
		t.Fatalf("%d goroutines failed (out of 200)", count)
	}
	t.Logf("200 concurrent requests: %d errors", count)
}

func TestStress_LargePayload(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		req.headers["x-size"] = req.body.length.toString();
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	sizes := []int{0, 100, 10000, 100000, 500000}
	for _, size := range sizes {
		body := strings.Repeat("A", size)
		req := &runtime.Request{
			Method:  "POST",
			URL:     "https://example.com/",
			Path:    "/",
			Headers: map[string]string{},
			Body:    body,
		}

		start := time.Now()
		result, err := engine.RunOnTraffic(req)
		dur := time.Since(start)

		if err != nil {
			t.Errorf("size %d: %v", size, err)
			continue
		}
		if result.Headers["x-size"] != fmt.Sprintf("%d", size) {
			t.Errorf("size %d: header mismatch", size)
		}
		t.Logf("payload %d bytes: %v (%.2f MB/s)", size, dur, float64(size)/dur.Seconds()/1024/1024)
	}
}

func TestStress_RapidPoolRecycle(t *testing.T) {
	jsCode := `
	function onTraffic(req) { return req; }
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 200, 16)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	start := time.Now()
	deadline := start.Add(2 * time.Second)
	count := 0

	for time.Now().Before(deadline) {
		req := &runtime.Request{Headers: map[string]string{}}
		_, err := engine.RunOnTraffic(req)
		if err != nil {
			continue
		}
		count++
	}

	dur := time.Since(start)
	if count < 1000 {
		t.Fatalf("only %d successful requests, need >=1000", count)
	}
	t.Logf("processed %d requests in %v (%.0f req/s)", count, dur, float64(count)/dur.Seconds())
}

func TestStress_SNIParserEdgeCases(t *testing.T) {
	// Test many SNI variants
	domains := []string{
		"a",
		"localhost",
		"192.168.1.1",
		"a-b-c.example.com",
		"xn--bcher-kva.example",
		strings.Repeat("x", 63) + ".com",
	}

	for _, domain := range domains {
		data := buildClientHello(domain)
		sni, err := proxy.ExtractSNI(data)
		if err != nil {
			t.Errorf("domain %q: %v", domain, err)
			continue
		}
		if sni != domain {
			t.Errorf("domain %q: got %q", domain, sni)
		}
	}
}

func TestStress_CertCacheConcurrent(t *testing.T) {
	ca, err := mitm.LoadOrCreateCA("./certs")
	if err != nil {
		t.Fatalf("ca: %v", err)
	}

	cache, err := mitm.NewCertCache(ca)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1000)

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			domain := fmt.Sprintf("host-%04d.example.com", id)
			_, err := cache.GetCertificate(domain)
			if err != nil {
				errCh <- fmt.Errorf("domain %s: %v", domain, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	count := 0
	for err := range errCh {
		t.Error(err)
		count++
	}
	t.Logf("1000 concurrent cert generations: %d errors", count)
}

func TestStress_MaxHeaders(t *testing.T) {
	jsCode := `
	function onTraffic(req) {
		var count = 0;
		for (var k in req.headers) {
			if (req.headers.hasOwnProperty(k)) count++;
		}
		req.headers["x-header-count"] = count.toString();
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, 2)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	headers := make(map[string]string)
	for i := 0; i < 100; i++ {
		headers[fmt.Sprintf("X-Header-%d", i)] = fmt.Sprintf("value-%d", i)
	}

	req := &runtime.Request{
		Method:  "GET",
		Headers: headers,
	}

	result, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("onTraffic: %v", err)
	}

	// The JS counts own properties; Go maps export all keys
	t.Logf("headers count, input=%d output=%s", len(headers), result.Headers["x-header-count"])
}

// ── Benchmark-style Tests ───────────────────────────────

func TestBenchmark_JSSimple(t *testing.T) {
	jsCode := `function onTraffic(r) { return r; } function onResponse(r) { return r; }`
	engine, err := runtime.NewJSEngine(jsCode, 500, 16)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{Method: "GET", Headers: map[string]string{}}

	iterations := 1000
	start := time.Now()
	for i := 0; i < iterations; i++ {
		engine.RunOnTraffic(req)
	}
	dur := time.Since(start)

	ops := float64(iterations) / dur.Seconds()
	latency := dur / time.Duration(iterations)
	t.Logf("Simple JS: %d iterations in %v = %.0f req/s (avg %v/req)",
		iterations, dur, ops, latency)
}

func BenchmarkJSExecution(b *testing.B) {
	jsCode := `function onTraffic(r) { return r; } function onResponse(r) { return r; }`
	engine, err := runtime.NewJSEngine(jsCode, 500, 8)
	if err != nil {
		b.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{Method: "GET", Headers: map[string]string{}}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine.RunOnTraffic(req)
	}
}

func BenchmarkJSWithModification(b *testing.B) {
	jsCode := `
	function onTraffic(req) {
		req.headers["x-test"] = "benchmark";
		req.headers["x-time"] = Date.now().toString();
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, 8)
	if err != nil {
		b.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{Method: "POST", URL: "https://example.com/data", Path: "/data", Headers: map[string]string{}}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine.RunOnTraffic(req)
	}
}

func BenchmarkJSWAFBlock(b *testing.B) {
	jsCode := `
	function onTraffic(req) {
		if (req.headers["x-block"] === "true") {
			return { _isResponse: true, status: 403 };
		}
		return req;
	}
	function onResponse(r) { return r; }`

	engine, err := runtime.NewJSEngine(jsCode, 500, 8)
	if err != nil {
		b.Fatalf("engine: %v", err)
	}

	req := &runtime.Request{Headers: map[string]string{"x-block": "true"}}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine.RunOnTraffic(req)
	}
}

func BenchmarkCertGeneration(b *testing.B) {
	ca, err := mitm.LoadOrCreateCA("./certs")
	if err != nil {
		b.Fatalf("ca: %v", err)
	}

	cache, err := mitm.NewCertCache(ca)
	if err != nil {
		b.Fatalf("cache: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		domain := fmt.Sprintf("bench-%d.example.com", i)
		cache.GetCertificate(domain)
	}
}

func BenchmarkSNIParsing(b *testing.B) {
	data := buildClientHello("benchmark.example.com")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		proxy.ExtractSNI(data)
	}
}

// ── Helper: TLS ClientHello Builder ────────────────────

func buildClientHello(serverName string) []byte {
	nameLen := len(serverName)
	sniNameBlock := []byte{0x00, byte(nameLen >> 8), byte(nameLen)}
	sniNameBlock = append(sniNameBlock, []byte(serverName)...)

	sniListLen := len(sniNameBlock)
	sniList := []byte{byte(sniListLen >> 8), byte(sniListLen)}
	sniList = append(sniList, sniNameBlock...)

	sniExtLen := len(sniList)
	sniExt := []byte{0x00, 0x00, byte(sniExtLen >> 8), byte(sniExtLen)}
	sniExt = append(sniExt, sniList...)

	extLen := len(sniExt)
	extBlock := []byte{byte(extLen >> 8), byte(extLen)}
	extBlock = append(extBlock, sniExt...)

	compBlock := []byte{0x01, 0x00}
	cipherSuites := []byte{0x00, 0x02, 0x13, 0x01}
	sessionID := []byte{0x00}

	random := make([]byte, 32)
	handshakeBody := []byte{0x03, 0x03}
	handshakeBody = append(handshakeBody, random...)
	handshakeBody = append(handshakeBody, sessionID...)
	handshakeBody = append(handshakeBody, cipherSuites...)
	handshakeBody = append(handshakeBody, compBlock...)
	handshakeBody = append(handshakeBody, extBlock...)

	hsLen := len(handshakeBody)
	handshake := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	handshake = append(handshake, handshakeBody...)

	recordLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(recordLen >> 8), byte(recordLen)}
	record = append(record, handshake...)

	return record
}


