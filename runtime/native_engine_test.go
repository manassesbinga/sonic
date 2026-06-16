package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeEnginePayloadRestoration(t *testing.T) {
	// 1. Criar script Python temporário para processamento de pacotes
	scriptContent := `
import sys
import json

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        packet = json.loads(line)
        req = packet.get("request")
        
        result = {
            "allow": True,
            "packet": packet
        }
        
        if req:
            url = req.get("url", "")
            if url == "empty.local":
                result["packet"]["request"]["body"] = ""
            elif url == "modified.local":
                result["packet"]["request"]["body"] = "custom content"
                
        sys.stdout.write(json.dumps(result) + "\n")
        sys.stdout.flush()
    except Exception as e:
        sys.stderr.write(str(e) + "\n")
        sys.stderr.flush()
`
	tmpDir, err := os.MkdirTemp("", "sonic-native-test-*")
	if err != nil {
		t.Fatalf("falha ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "test_worker.py")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("falha ao escrever script Python temporario: %v", err)
	}

	// Criar KVStore em memória
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("falha ao criar KVStore: %v", err)
	}
	defer kv.Close()

	// 2. Instanciar o NativeEngine
	engine, err := NewNativeEngine(scriptPath, 2000, 2, kv)
	if err != nil {
		t.Fatalf("falha ao criar NativeEngine: %v", err)
	}
	defer engine.Close()

	// 3. Caso de Teste A: Payload Pequeno (deve passar direto sem alterações)
	smallBody := "small request body"
	packetSmall := &Packet{
		ID: "p1",
		Request: &Request{
			ID:     "p1",
			Method: "POST",
			URL:    "normal.local",
			Body:   smallBody,
		},
	}
	resSmall, err := engine.ProcessPacket(packetSmall)
	if err != nil {
		t.Fatalf("erro ao processar pacote pequeno: %v", err)
	}
	if resSmall.Packet.Request.Body != smallBody {
		t.Errorf("esperava corpo '%s', obteve '%s'", smallBody, resSmall.Packet.Request.Body)
	}

	// 4. Caso de Teste B: Payload Grande Intocado (deve ser restaurado)
	largeBody := strings.Repeat("A", 100*1024) // 100 KB
	packetLargeUntouched := &Packet{
		ID: "p2",
		Request: &Request{
			ID:     "p2",
			Method: "POST",
			URL:    "normal.local",
			Body:   largeBody,
		},
	}
	resLargeUntouched, err := engine.ProcessPacket(packetLargeUntouched)
	if err != nil {
		t.Fatalf("erro ao processar pacote grande intocado: %v", err)
	}
	if len(resLargeUntouched.Packet.Request.Body) != len(largeBody) {
		t.Errorf("esperava corpo de tamanho %d (restaurado), obteve %d", len(largeBody), len(resLargeUntouched.Packet.Request.Body))
	}

	// 5. Caso de Teste C: Payload Grande Limpo (deve permanecer limpo/vazio)
	packetLargeEmpty := &Packet{
		ID: "p3",
		Request: &Request{
			ID:     "p3",
			Method: "POST",
			URL:    "empty.local",
			Body:   largeBody,
		},
	}
	resLargeEmpty, err := engine.ProcessPacket(packetLargeEmpty)
	if err != nil {
		t.Fatalf("erro ao processar pacote grande limpo: %v", err)
	}
	if resLargeEmpty.Packet.Request.Body != "" {
		t.Errorf("esperava corpo vazio, mas foi restaurado indevidamente para tamanho %d", len(resLargeEmpty.Packet.Request.Body))
	}

	// 6. Caso de Teste D: Payload Grande Modificado (deve conter a modificação e não restaurar)
	packetLargeModified := &Packet{
		ID: "p4",
		Request: &Request{
			ID:     "p4",
			Method: "POST",
			URL:    "modified.local",
			Body:   largeBody,
		},
	}
	resLargeModified, err := engine.ProcessPacket(packetLargeModified)
	if err != nil {
		t.Fatalf("erro ao processar pacote grande modificado: %v", err)
	}
	expectedModifiedBody := "custom content"
	if resLargeModified.Packet.Request.Body != expectedModifiedBody {
		t.Errorf("esperava corpo modificado '%s', mas obteve '%s'", expectedModifiedBody, resLargeModified.Packet.Request.Body)
	}
}

func TestNativeEngineConcurrencyAndTimeout(t *testing.T) {
	// Script que demora 1.5s
	scriptContent := `
import sys
import json
import time

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    packet = json.loads(line)
    req = packet.get("request")
    if req and req.get("url") == "slow.local":
        time.sleep(1.5)
    sys.stdout.write(json.dumps({"allow": True, "packet": packet}) + "\n")
    sys.stdout.flush()
`
	tmpDir, err := os.MkdirTemp("", "sonic-native-timeout-*")
	if err != nil {
		t.Fatalf("falha ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "slow_worker.py")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("falha ao escrever script lento: %v", err)
	}

	kv, _ := NewInMemoryKVStore()
	defer kv.Close()

	// Timeout de 200ms
	engine, err := NewNativeEngine(scriptPath, 200, 2, kv)
	if err != nil {
		t.Fatalf("falha ao criar slow engine: %v", err)
	}
	defer engine.Close()

	packetSlow := &Packet{
		ID: "p_slow",
		Request: &Request{
			ID:     "p_slow",
			Method: "GET",
			URL:    "slow.local",
			Body:   "slow data",
		},
	}

	_, err = engine.ProcessPacket(packetSlow)
	if err == nil {
		t.Error("esperava erro de timeout, mas processamento retornou sucesso")
	} else if !strings.Contains(err.Error(), "timeout excedido") {
		t.Errorf("esperava erro de timeout, obteve: %v", err)
	}
}
