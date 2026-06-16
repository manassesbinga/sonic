package webui

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/manassesbinga/sonic/runtime"
)

func TestCosineSimilarityText(t *testing.T) {
	tests := []struct {
		s1       string
		s2       string
		minScore float64
	}{
		// Frases idênticas
		{"Como está o tempo hoje?", "Como está o tempo hoje?", 0.99},
		// Frases semanticamente parecidas com palavras idênticas
		{"Qual é a capital de França?", "Diz-me a capital de França", 0.60},
		// Frases sem qualquer relação
		{"Qual é a capital de França?", "Como fazer bolo de chocolate", 0.0},
	}

	for _, tt := range tests {
		score := CosineSimilarityText(tt.s1, tt.s2)
		if score < tt.minScore {
			t.Errorf("similarity between %q and %q was %f, expected at least %f", tt.s1, tt.s2, score, tt.minScore)
		}
		if score > 1.0 {
			t.Errorf("similarity score exceeded 1.0: %f", score)
		}
	}
}

func TestAIGateway_CacheStorageAndHit(t *testing.T) {
	kv, err := runtime.NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("failed to create memory KV: %v", err)
	}
	defer kv.Clear()

	gateway := NewAIGateway(kv, "test-token-123")
	defer os.RemoveAll("./data")
	
	// Testar criptografia de chaves
	t.Run("API Keys Encryption", func(t *testing.T) {
		cfg := AIGatewayConfig{
			Active:       true,
			Mode:         CachingModeOffline,
			Threshold:    0.8,
			OpenAIKey:    "openai-secret-key-12345",
			AnthropicKey: "anthropic-secret-key-67890",
		}
		
		err := gateway.SaveConfig(cfg)
		if err != nil {
			t.Fatalf("failed to save config: %v", err)
		}
		
		// 1. Verificar se na memória está descriptografado
		memConfig := gateway.GetConfig()
		if memConfig.OpenAIKey != "openai-secret-key-12345" {
			t.Errorf("expected plaintext key in memory, got: %q", memConfig.OpenAIKey)
		}
		if memConfig.AnthropicKey != "anthropic-secret-key-67890" {
			t.Errorf("expected plaintext key in memory, got: %q", memConfig.AnthropicKey)
		}
		
		// 2. Verificar se no banco físico (KVStore) está encriptado (não legível em texto plano)
		rawBytes := kv.Get("_sys:ai:config")
		if rawBytes == nil {
			t.Fatal("config not found in KV Store")
		}
		
		var rawMap map[string]interface{}
		if err := json.Unmarshal(rawBytes, &rawMap); err != nil {
			t.Fatalf("failed to parse stored json: %v", err)
		}
		
		storedOpenAIKey := rawMap["openAIKey"].(string)
		storedAnthropicKey := rawMap["anthropicKey"].(string)
		
		if storedOpenAIKey == "openai-secret-key-12345" {
			t.Error("OpenAIKey was stored in plain text in the KV store")
		}
		if storedAnthropicKey == "anthropic-secret-key-67890" {
			t.Error("AnthropicKey was stored in plain text in the KV store")
		}
		
		// 3. Simular reinicialização carregando do banco encriptado
		newGateway := NewAIGateway(kv, "test-token-123")
		loadedConfig := newGateway.GetConfig()
		
		if loadedConfig.OpenAIKey != "openai-secret-key-12345" {
			t.Errorf("expected decrypted key after reload, got: %q", loadedConfig.OpenAIKey)
		}
		if loadedConfig.AnthropicKey != "anthropic-secret-key-67890" {
			t.Errorf("expected decrypted key after reload, got: %q", loadedConfig.AnthropicKey)
		}
	})

	// Salvar configuração com threshold de 0.8 para teste offline
	gateway.SaveConfig(AIGatewayConfig{
		Active:          true,
		Mode:            CachingModeOffline,
		Threshold:       0.8,
		DefaultProvider: "openai",
	})

	// Simular o armazenamento de uma entrada no cache
	entry := AICacheEntry{
		Prompt:   "O que é o Sonic Edge Proxy?",
		Response: "O Sonic é um edge proxy multi-linguagem de alta performance escrito em Go.",
		Created:  time.Now(),
		Tokens:   15,
	}
	gateway.saveCacheEntry(entry)

	// Validar que o cache semântico retorna hit para uma pergunta muito parecida
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "user", "content": "O que é o Sonic Edge Proxy?"},
		},
	})

	ctx := context.Background()
	hit, respBytes, err := gateway.ProcessChatCompletion(ctx, reqBody)
	if err != nil {
		t.Fatalf("failed processing chat completion: %v", err)
	}

	if hit == nil {
		t.Fatal("expected semantic cache HIT, got MISS")
	}

	if hit.Response != entry.Response {
		t.Errorf("expected response %q, got %q", entry.Response, hit.Response)
	}

	// Verificar se o JSON de retorno tem o formato de completions correto
	var parsedResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &parsedResp); err != nil {
		t.Fatalf("failed parsing response JSON: %v", err)
	}

	if len(parsedResp.Choices) == 0 || parsedResp.Choices[0].Message.Content != entry.Response {
		t.Errorf("unexpected completion content: %v", parsedResp.Choices)
	}
}
