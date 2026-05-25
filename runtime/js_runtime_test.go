package runtime

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestJSEngineNormalExecution(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			req.headers["X-Test"] = "Success";
			req.path = "/new-path";
			return req;
		}

		function onResponse(resp) {
			resp.headers["X-Processed"] = "Yes";
			resp.status = 201;
			return resp;
		}
	`

	engine, err := NewJSEngine(jsCode, 50, 4)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	// 1. Testa Request (onTraffic)
	req := &Request{
		Method: "GET",
		URL:    "https://example.com/original",
		Path:   "/original",
		Headers: map[string]string{
			"Host": "example.com",
		},
		Body: "",
	}

	modReq, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("Erro ao rodar onTraffic: %v", err)
	}

	if modReq.Headers["X-Test"] != "Success" {
		t.Errorf("Header X-Test nao foi modificado. Obteve: %s", modReq.Headers["X-Test"])
	}
	if modReq.Path != "/new-path" {
		t.Errorf("Path nao foi modificado. Obteve: %s", modReq.Path)
	}

	// 2. Testa Response (onResponse)
	resp := &Response{
		Status: 200,
		Headers: map[string]string{
			"Content-Type": "text/html",
		},
		Body: "hello",
	}

	modResp, err := engine.RunOnResponse(resp)
	if err != nil {
		t.Fatalf("Erro ao rodar onResponse: %v", err)
	}

	if modResp.Headers["X-Processed"] != "Yes" {
		t.Errorf("Header X-Processed nao foi modificado. Obteve: %s", modResp.Headers["X-Processed"])
	}
	if modResp.Status != 201 {
		t.Errorf("Status nao foi modificado. Obteve: %d", modResp.Status)
	}
}

func TestJSEngineCPUWatchdog(t *testing.T) {
	// Script com loop infinito de proposito para estourar o CPU Watchdog
	jsCode := `
		function onTraffic(req) {
			var i = 0;
			while (true) {
				i++;
			}
			return req;
		}

		function onResponse(resp) {
			return resp;
		}
	`

	// Timeout super baixo de 10ms
	engine, err := NewJSEngine(jsCode, 10, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	req := &Request{
		Method:  "GET",
		Headers: make(map[string]string),
	}

	_, err = engine.RunOnTraffic(req)
	if err == nil {
		t.Fatal("Esperava erro de timeout de execucao, mas o script executou com sucesso")
	}

	if !errors.Is(err, ErrExecutionTimeout) && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("Esperava erro ErrExecutionTimeout, obteve: %v", err)
	}
}

func TestJSEngineConcurrency(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			req.headers["X-Thread-ID"] = req.headers["X-Input-ID"];
			return req;
		}

		function onResponse(resp) {
			return resp;
		}
	`

	engine, err := NewJSEngine(jsCode, 200, 8)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	var wg sync.WaitGroup
	numRequests := 20

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			inputVal := string(rune(id))
			req := &Request{
				Headers: map[string]string{
					"X-Input-ID": inputVal,
				},
			}

			modReq, err := engine.RunOnTraffic(req)
			if err != nil {
				t.Errorf("Erro na Goroutine %d: %v", id, err)
				return
			}

			if modReq.Headers["X-Thread-ID"] != inputVal {
				t.Errorf("Poluicao de estado concorrente! Goroutine %d obteve %s em vez de %s",
					id, modReq.Headers["X-Thread-ID"], inputVal)
			}
		}(i)
	}

	wg.Wait()
}

func TestJSEngineCloudflareWorkersCompatibility(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			// Teste do construtor de Headers e métodos
			let headers = new Headers();
			headers.set("X-WAF-Block", "True");
			headers.set("Content-Type", "text/plain");

			// Se o request tiver um header específico de teste de bloqueio
			if (req.headers.get("x-waf-trigger") === "active") {
				// Retorna um Response direto do onTraffic (bloqueio WAF)
				return new Response("Blocked by net WAF", {
					status: 403,
					headers: headers
				});
			}

			// Modifica e retorna o request
			req.headers.set("X-Compatibility", "StandardWebAPIs");
			return req;
		}

		function onResponse(resp) {
			resp.headers.set("X-Powered-By-Self-Hosted", "NetFn");
			return resp;
		}
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	// 1. Testa bloqueio direto (onTraffic retornando Response)
	reqBlock := &Request{
		Method: "GET",
		URL:    "https://example.com/",
		Path:   "/",
		Headers: map[string]string{
			"x-waf-trigger": "active",
		},
	}

	resBlock, err := engine.RunOnTraffic(reqBlock)
	if err != nil {
		t.Fatalf("Erro ao rodar onTraffic com bloqueio: %v", err)
	}

	if !resBlock.IsResponse {
		t.Fatal("Esperava que IsResponse fosse true (bloqueio do WAF)")
	}
	if resBlock.Status != 403 {
		t.Errorf("Esperava status 403, obteve: %d", resBlock.Status)
	}
	if resBlock.Headers["x-waf-block"] != "True" {
		t.Errorf("Esperava header x-waf-block, obteve: %s", resBlock.Headers["x-waf-block"])
	}
	if resBlock.Body != "Blocked by net WAF" {
		t.Errorf("Esperava body bloqueado, obteve: %s", resBlock.Body)
	}

	// 2. Testa fluxo normal com modificação de request
	reqPass := &Request{
		Method: "GET",
		URL:    "https://example.com/",
		Path:   "/",
		Headers: map[string]string{
			"x-waf-trigger": "inactive",
		},
	}

	resPass, err := engine.RunOnTraffic(reqPass)
	if err != nil {
		t.Fatalf("Erro ao rodar onTraffic normal: %v", err)
	}

	if resPass.IsResponse {
		t.Fatal("Esperava que IsResponse fosse false (fluxo normal)")
	}
	if resPass.Headers["x-compatibility"] != "StandardWebAPIs" {
		t.Errorf("Esperava header x-compatibility, obteve: %s", resPass.Headers["x-compatibility"])
	}
}

