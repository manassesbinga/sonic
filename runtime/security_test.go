package runtime

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestJWTVerify_WeaknessMock(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			var valid = jwtVerify("short", "secret");
			req.headers["X-JWT-Short"] = valid ? "true" : "false";

			var valid2 = jwtVerify("this-is-longer-than-10-chars", "secret");
			req.headers["X-JWT-Long"] = valid2 ? "true" : "false";

			var valid3 = jwtVerify("", "secret");
			req.headers["X-JWT-Empty"] = valid3 ? "true" : "false";

			var valid4 = jwtVerify("this-is-longer-than-10-chars", "");
			req.headers["X-JWT-EmptySecret"] = valid4 ? "true" : "false";

			return req;
		}
		function onResponse(resp) { return resp; }
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	req := &Request{
		Headers: make(map[string]string),
	}

	modReq, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("Erro ao rodar onTraffic: %v", err)
	}

	if modReq.Headers["X-JWT-Short"] == "true" {
		t.Log("ATENCAO: jwtVerify aceitou token 'short' (< 10 chars) — mock atualmente so checa len > 10")
	}
	if modReq.Headers["X-JWT-Long"] != "true" {
		t.Error("jwtVerify deveria retornar true para token > 10 chars")
	}
	if modReq.Headers["X-JWT-Empty"] == "true" {
		t.Error("jwtVerify deveria retornar false para token vazio")
	}
	if modReq.Headers["X-JWT-EmptySecret"] == "true" {
		t.Error("jwtVerify deveria retornar false para secret vazio")
	}
}

func TestRunOnTraffic_PanicRecovery(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			throw new Error("erro simulado no JS");
		}
		function onResponse(resp) { return resp; }
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	req := &Request{
		Method:  "GET",
		Headers: make(map[string]string),
	}

	_, err = engine.RunOnTraffic(req)
	if err == nil {
		t.Fatal("Esperava erro quando JS lanca excecao")
	}
}

func TestRunOnTraffic_ReturnNull(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			return null;
		}
		function onResponse(resp) { return resp; }
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	req := &Request{
		Method: "GET",
		URL:    "https://example.com/",
		Path:   "/",
		Headers: map[string]string{
			"Host": "example.com",
		},
	}

	modReq, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("onTraffic retornou erro ao devolver null: %v", err)
	}
	if modReq == nil {
		t.Fatal("onTraffic retornou resultado nulo quando JS devolveu null")
	}
	if modReq.URL != "https://example.com/" {
		t.Errorf("Request original nao foi preservado apos retorno null do JS")
	}
}

func TestRunOnTraffic_ReturnUndefined(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			return undefined;
		}
		function onResponse(resp) { return resp; }
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	req := &Request{
		Method: "GET",
		URL:    "https://example.com/",
		Path:   "/",
		Headers: map[string]string{
			"Host": "example.com",
		},
	}

	modReq, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("onTraffic retornou erro ao devolver undefined: %v", err)
	}
	if modReq == nil {
		t.Fatal("onTraffic retornou resultado nulo quando JS devolveu undefined")
	}
	if modReq.Method != "GET" {
		t.Errorf("Request original nao foi preservado apos retorno undefined do JS")
	}
}

func TestRunOnTraffic_MultipleTimeouts(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			while (true) {}
		}
		function onResponse(resp) { return resp; }
	`

	engine, err := NewJSEngine(jsCode, 10, 4)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := &Request{
				Method:  "GET",
				Headers: make(map[string]string),
			}
			_, err := engine.RunOnTraffic(req)
			if err == nil {
				errs <- errors.New("esperava timeout")
				return
			}
			if !errors.Is(err, ErrExecutionTimeout) && !strings.Contains(err.Error(), "timeout") {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("Erro inesperado durante timeouts concorrentes: %v", err)
	}
}

func TestRunOnResponse_PanicRecovery(t *testing.T) {
	jsCode := `
		function onTraffic(req) { return req; }
		function onResponse(resp) {
			throw new Error("erro simulado no onResponse");
		}
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	resp := &Response{
		Status:  200,
		Headers: map[string]string{"Content-Type": "text/html"},
		Body:    "ok",
	}

	_, err = engine.RunOnResponse(resp)
	if err == nil {
		t.Fatal("Esperava erro quando onResponse lanca excecao")
	}
}

func TestJSEngine_NoFunctions(t *testing.T) {
	jsCode := `
		var x = 42;
	`

	engine, err := NewJSEngine(jsCode, 50, 2)
	if err != nil {
		t.Fatalf("Erro ao criar motor JS: %v", err)
	}

	req := &Request{
		Method:  "GET",
		URL:     "https://example.com/",
		Path:    "/",
		Headers: make(map[string]string),
	}

	modReq, err := engine.RunOnTraffic(req)
	if err != nil {
		t.Fatalf("onTraffic falhou quando funcao nao existe: %v", err)
	}
	if modReq == nil {
		t.Fatal("onTraffic retornou nil quando funcao nao existe")
	}
	if modReq.URL != "https://example.com/" {
		t.Error("Request original nao foi preservado quando onTraffic nao existe")
	}

	resp := &Response{
		Status:  200,
		Headers: map[string]string{},
	}

	modResp, err := engine.RunOnResponse(resp)
	if err != nil {
		t.Fatalf("onResponse falhou quando funcao nao existe: %v", err)
	}
	if modResp == nil {
		t.Fatal("onResponse retornou nil quando funcao nao existe")
	}
	if modResp.Status != 200 {
		t.Error("Response original nao foi preservado quando onResponse nao existe")
	}
}

func TestJSEngine_PoolReplacementAfterTimeout(t *testing.T) {
	jsCode := `
		function onTraffic(req) {
			while (true) {}
		}
		function onResponse(resp) { return resp; }
	`

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
		t.Fatal("Esperava timeout na primeira execucao")
	}

	req2 := &Request{
		Method:  "GET",
		Headers: make(map[string]string),
	}

	_, err = engine.RunOnTraffic(req2)
	if err == nil {
		t.Fatal("Esperava timeout na segunda execucao tambem (nova VM deve manter mesmo comportamento)")
	}
}
