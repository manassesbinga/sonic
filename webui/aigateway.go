package webui

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manassesbinga/sonic/runtime"
)

// AICachingMode define se o cache usa termos locais ou embeddings da OpenAI.
type AICachingMode string

const (
	CachingModeOffline    AICachingMode = "offline"   // Similaridade TF-IDF de termos
	CachingModeEmbeddings AICachingMode = "embeddings" // Similaridade de Cosseno via embeddings
)

// AIGatewayConfig armazena as configurações do AI Gateway.
type AIGatewayConfig struct {
	Active            bool          `json:"active"`
	Mode              AICachingMode `json:"mode"`
	Threshold         float64       `json:"threshold"` // Ex: 0.88 para embeddings, 0.70 para offline
	OpenAIKey         string        `json:"openAIKey"`
	AnthropicKey      string        `json:"anthropicKey"`
	DefaultProvider   string        `json:"defaultProvider"` // "openai" ou "anthropic"
	FallbackProvider  string        `json:"fallbackProvider"`
	CacheTTLHours     int           `json:"cacheTTLHours"` // TTL de cache em horas (0 = eterno)
}

// AICacheEntry representa uma entrada no cache semântico de IA.
type AICacheEntry struct {
	Prompt    string     `json:"prompt"`
	Response  string     `json:"response"`
	Embedding []float64  `json:"embedding,omitempty"`
	Created   time.Time  `json:"created"`
	Tokens    int        `json:"tokens"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// AIGatewayStats armazena as métricas acumuladas de economia.
type AIGatewayStats struct {
	TotalRequests int64   `json:"totalRequests"`
	CacheHits     int64   `json:"cacheHits"`
	TokensSaved   int64   `json:"tokensSaved"`
	CostSavedUSD  float64 `json:"costSavedUSD"`
}

// AIGateway gerencia o roteamento de IA e o cache semântico.
type AIGateway struct {
	mu      sync.RWMutex
	config  AIGatewayConfig
	stats   AIGatewayStats
	kvStore *runtime.KVStore
	token   string
	aesKey  []byte
}

// NewAIGateway cria uma nova instância do AIGateway.
func NewAIGateway(kv *runtime.KVStore, token string) *AIGateway {
	g := &AIGateway{
		kvStore: kv,
		token:   token,
	}
	g.initCryptoKey()
	g.loadConfigAndStats()
	return g
}

func (g *AIGateway) initCryptoKey() {
	keyDir := filepath.Join(".", "data")
	_ = os.MkdirAll(keyDir, 0700)

	keyPath := filepath.Join(keyDir, "sonic.key")
	
	keyBytes, err := os.ReadFile(keyPath)
	if err == nil && len(keyBytes) == 32 {
		g.aesKey = keyBytes
		return
	}

	newKey := make([]byte, 32)
	if _, errRand := io.ReadFull(rand.Reader, newKey); errRand != nil {
		hash := sha256.Sum256([]byte(g.token))
		g.aesKey = hash[:]
		return
	}

	_ = os.WriteFile(keyPath, newKey, 0600)
	g.aesKey = newKey
}

func (g *AIGateway) encryptGCM(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(g.aesKey)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aesgcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (g *AIGateway) decryptGCM(cryptoText string) (string, error) {
	if cryptoText == "" {
		return "", nil
	}
	ciphertext, err := base64.StdEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(g.aesKey)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := aesgcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesgcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func decryptGCMWithToken(cryptoText string, token string) (string, error) {
	if cryptoText == "" {
		return "", nil
	}
	ciphertext, err := base64.StdEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := aesgcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesgcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (g *AIGateway) decryptKeyWithFallback(ciphertext string) string {
	if ciphertext == "" {
		return ""
	}

	// 1. Tenta decifrar com a chave simétrica física
	decrypted, err := g.decryptGCM(ciphertext)
	if err == nil {
		return decrypted
	}

	// 2. Se falhar, tenta com o token de administração para compatibilidade
	if g.token != "" {
		decryptedWithToken, errToken := decryptGCMWithToken(ciphertext, g.token)
		if errToken == nil {
			return decryptedWithToken
		}
	}

	// 3. Se falhar, verifica se é uma chave em texto plano
	if strings.HasPrefix(ciphertext, "sk-") {
		return ciphertext
	}

	return ""
}

// loadConfigAndStats carrega configurações e estatísticas do bbolt.
func (g *AIGateway) loadConfigAndStats() {
	if g.kvStore == nil {
		return
	}
	// Config
	if data := g.kvStore.Get("_sys:ai:config"); data != nil {
		var cfg AIGatewayConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			cfg.OpenAIKey = g.decryptKeyWithFallback(cfg.OpenAIKey)
			cfg.AnthropicKey = g.decryptKeyWithFallback(cfg.AnthropicKey)
			g.config = cfg
		}
	} else {
		g.config = AIGatewayConfig{
			Active:          false,
			Mode:            CachingModeOffline,
			Threshold:       0.75,
			DefaultProvider: "openai",
			CacheTTLHours:   24, // 24 horas por padrão
		}
	}
	// Stats
	if data := g.kvStore.Get("_sys:ai:stats"); data != nil {
		var st AIGatewayStats
		if err := json.Unmarshal(data, &st); err == nil {
			g.stats = st
		}
	}
}

// SaveConfig guarda as configurações no KVStore.
func (g *AIGateway) SaveConfig(cfg AIGatewayConfig) error {
	encryptedCfg := cfg
	var err error
	encryptedCfg.OpenAIKey, err = g.encryptGCM(cfg.OpenAIKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt OpenAI key: %w", err)
	}
	encryptedCfg.AnthropicKey, err = g.encryptGCM(cfg.AnthropicKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt Anthropic key: %w", err)
	}

	g.mu.Lock()
	g.config = cfg
	g.mu.Unlock()

	if g.kvStore == nil {
		return nil
	}
	data, err := json.Marshal(encryptedCfg)
	if err != nil {
		return err
	}
	return g.kvStore.Set("_sys:ai:config", data)
}

// GetConfig retorna a configuração atual.
func (g *AIGateway) GetConfig() AIGatewayConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.config
}

// GetStats retorna as métricas atuais do Gateway.
func (g *AIGateway) GetStats() AIGatewayStats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.stats
}

// ClearCache apaga todas as chaves de cache semântico.
func (g *AIGateway) ClearCache() {
	db := runtime.GetDB()
	if db != nil {
		db.Lock()
		_, _ = db.DB().Exec("DELETE FROM ai_cache;")
		_, _ = db.DB().Exec("DELETE FROM ai_cache_fts;")
		db.Unlock()
	}

	if g.kvStore == nil {
		return
	}
	keys := g.kvStore.Keys()
	for _, k := range keys {
		if strings.HasPrefix(k, "_sys:aicache:") {
			_ = g.kvStore.Delete(k)
		}
	}
}

// EvictCacheEntry remove um registro do cache manualmente com base no prompt.
func (g *AIGateway) EvictCacheEntry(prompt string) error {
	db := runtime.GetDB()
	if db != nil {
		db.Lock()
		defer db.Unlock()

		// Localiza o rowid correspondente para também limpar do FTS5
		var rowid int64
		err := db.DB().QueryRow("SELECT rowid FROM ai_cache WHERE prompt = ?;", prompt).Scan(&rowid)
		if err == nil {
			_, _ = db.DB().Exec("DELETE FROM ai_cache_fts WHERE rowid = ?;", rowid)
		}
		_, err = db.DB().Exec("DELETE FROM ai_cache WHERE prompt = ?;", prompt)
		return err
	}
	return fmt.Errorf("database not initialized")
}

func (g *AIGateway) recordHit(tokensSaved int) {
	g.mu.Lock()
	g.stats.TotalRequests++
	g.stats.CacheHits++
	g.stats.TokensSaved += int64(tokensSaved)
	g.stats.CostSavedUSD += (float64(tokensSaved) / 1000.0) * 0.010
	stats := g.stats
	g.mu.Unlock()

	if g.kvStore != nil {
		data, _ := json.Marshal(stats)
		_ = g.kvStore.Set("_sys:ai:stats", data)
	}
}

func (g *AIGateway) recordMiss() {
	g.mu.Lock()
	g.stats.TotalRequests++
	stats := g.stats
	g.mu.Unlock()

	if g.kvStore != nil {
		data, _ := json.Marshal(stats)
		_ = g.kvStore.Set("_sys:ai:stats", data)
	}
}

// ProcessChatCompletion intercepta chamadas do ChatCompletion para gerenciar cache/fallback.
func (g *AIGateway) ProcessChatCompletion(ctx context.Context, reqBody []byte) (*AICacheEntry, []byte, error) {
	g.mu.RLock()
	active := g.config.Active
	mode := g.config.Mode
	threshold := g.config.Threshold
	openAIKey := g.config.OpenAIKey
	defaultProvider := g.config.DefaultProvider
	fallbackProvider := g.config.FallbackProvider
	anthropicKey := g.config.AnthropicKey
	g.mu.RUnlock()

	if !active {
		return nil, nil, nil
	}

	// 1. Extrair o prompt (mensagem do utilizador) do JSON
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqBody, &parsed); err != nil || len(parsed.Messages) == 0 {
		return nil, nil, nil
	}

	prompt := ""
	for i := len(parsed.Messages) - 1; i >= 0; i-- {
		if parsed.Messages[i].Role == "user" {
			prompt = parsed.Messages[i].Content
			break
		}
	}
	if prompt == "" {
		return nil, nil, nil
	}

	// 2. Tentar encontrar similaridade semântica (SQLite FTS5 ou BoltDB fallback)
	var hitEntry *AICacheEntry
	var bestScore float64
	var fetchedEmb []float64

	// Atalho rápido: correspondência exata primeiro (Short-circuit)
	exactHit, errExact := g.searchCacheExact(ctx, prompt)
	if errExact == nil && exactHit != nil {
		hitEntry = exactHit
		bestScore = 1.0
	}

	var cachedEntries []AICacheEntry
	var searchErr error

	if hitEntry == nil {
		if runtime.GetDB() != nil {
			cachedEntries, searchErr = g.searchCacheFTS(ctx, prompt)
		}

		if runtime.GetDB() == nil || searchErr != nil {
			cachedEntries = g.getAllCacheEntries()
		}

		if mode == CachingModeOffline {
			for _, entry := range cachedEntries {
				score := CosineSimilarityText(prompt, entry.Prompt)
				if score > bestScore {
					bestScore = score
					hitEntry = &entry
				}
			}
				} else if mode == CachingModeEmbeddings && openAIKey != "" && len(cachedEntries) > 0 {
			emb, err := g.fetchOpenAIEmbedding(ctx, prompt, openAIKey)
			if err == nil {
				fetchedEmb = emb
				for _, entry := range cachedEntries {
					if len(entry.Embedding) > 0 {
						score := CosineSimilarityVectors(emb, entry.Embedding)
						if score > bestScore {
							bestScore = score
							hitEntry = &entry
						}
					}
				}
			}
		}
	}

	if hitEntry != nil && bestScore >= threshold {
		g.recordHit(hitEntry.Tokens)
		
		// Constrói resposta no formato OpenAI
		respJSON, _ := json.Marshal(map[string]interface{}{
			"id":      fmt.Sprintf("chatcmpl-sonic-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "sonic-semantic-cache",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": hitEntry.Response,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": hitEntry.Tokens,
				"total_tokens":      10 + hitEntry.Tokens,
			},
		})
		return hitEntry, respJSON, nil
	}

	g.recordMiss()

	// 3. Chamar a API real (com suporte a Fallback Dinâmico)
	var respBytes []byte
	var callErr error

	if defaultProvider == "openai" && openAIKey != "" {
		respBytes, callErr = g.forwardOpenAI(ctx, reqBody, openAIKey)
		if callErr != nil && fallbackProvider == "anthropic" && anthropicKey != "" {
			// Fallback dinâmico real convertendo os payloads
			translated, transErr := TranslateOpenAIToAnthropic(reqBody)
			if transErr == nil {
				respAnthropic, errAnth := g.forwardAnthropic(ctx, translated, anthropicKey)
				if errAnth == nil {
					respBytes, callErr = TranslateAnthropicResponseToOpenAI(respAnthropic)
				}
			}
		}
	} else if defaultProvider == "anthropic" && anthropicKey != "" {
		respBytes, callErr = g.forwardAnthropic(ctx, reqBody, anthropicKey)
		if callErr != nil && fallbackProvider == "openai" && openAIKey != "" {
			translated, transErr := TranslateAnthropicToOpenAI(reqBody)
			if transErr == nil {
				respOpenAI, errOpen := g.forwardOpenAI(ctx, translated, openAIKey)
				if errOpen == nil {
					respBytes, callErr = TranslateOpenAIResponseToAnthropic(respOpenAI)
				}
			}
		}
	}

	if callErr != nil {
		return nil, nil, callErr
	}

	// 4. Salvar resposta no cache
	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}

	assistantReply := ""
	tokensCount := 100

	if err := json.Unmarshal(respBytes, &openaiResp); err == nil && len(openaiResp.Choices) > 0 {
		assistantReply = openaiResp.Choices[0].Message.Content
		if openaiResp.Usage.TotalTokens > 0 {
			tokensCount = openaiResp.Usage.TotalTokens
		}
	} else {
		var anthropicResp struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(respBytes, &anthropicResp); err == nil && len(anthropicResp.Content) > 0 {
			assistantReply = anthropicResp.Content[0].Text
			tokensCount = anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens
		}
	}

	if assistantReply != "" {
		var embVector []float64
		if mode == CachingModeEmbeddings && openAIKey != "" {
			if len(fetchedEmb) > 0 {
				embVector = fetchedEmb
			} else {
				embVector, _ = g.fetchOpenAIEmbedding(ctx, prompt, openAIKey)
			}
		}

		g.SaveCache(prompt, assistantReply, embVector, tokensCount)
	}

	return nil, respBytes, nil
}

// SaveCache persiste um registro de similaridade no SQLite FTS5 ou BoltDB.
func (g *AIGateway) SaveCache(prompt string, response string, embedding []float64, tokens int) {
	g.mu.RLock()
	ttlHours := g.config.CacheTTLHours
	g.mu.RUnlock()

	var expires *time.Time
	if ttlHours > 0 {
		t := time.Now().Add(time.Duration(ttlHours) * time.Hour)
		expires = &t
	}

	db := runtime.GetDB()
	if db != nil {
		db.Lock()
		defer db.Unlock()

		uuid := fmt.Sprintf("%x", time.Now().UnixNano())
		embBytes, _ := json.Marshal(embedding)
		
		var expStr sql.NullString
		if expires != nil {
			expStr.Valid = true
			expStr.String = expires.Format(time.RFC3339)
		}

		_, err := db.DB().Exec(`
			INSERT INTO ai_cache (id, prompt, response, embedding, created, tokens, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?);
		`, uuid, prompt, response, embBytes, time.Now().Format(time.RFC3339), tokens, expStr)

		if err == nil {
			var rowid int64
			errRow := db.DB().QueryRow("SELECT last_insert_rowid();").Scan(&rowid)
			if errRow == nil {
				_, _ = db.DB().Exec(`
					INSERT INTO ai_cache_fts (rowid, prompt, response)
					VALUES (?, ?, ?);
				`, rowid, prompt, response)
			}
		}
		return
	}

	// Fallback para BoltDB
	entry := AICacheEntry{
		Prompt:    prompt,
		Response:  response,
		Embedding: embedding,
		Created:   time.Now(),
		Tokens:    tokens,
		ExpiresAt: expires,
	}
	g.saveCacheEntry(entry)
}

// searchCacheFTS busca registros no SQLite FTS5 filtrando apenas os 10 mais relevantes.
func (g *AIGateway) searchCacheFTS(ctx context.Context, prompt string) ([]AICacheEntry, error) {
	db := runtime.GetDB()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	cleanedQuery := cleanFTSQueryString(prompt)
	if cleanedQuery == "" {
		return nil, nil
	}

	db.RLock()
	defer db.RUnlock()

	nowStr := time.Now().Format(time.RFC3339)

		rows, err := db.DB().QueryContext(ctx, `
			SELECT c.prompt, c.response, c.embedding, c.created, c.tokens, c.expires_at
			FROM ai_cache c
			JOIN ai_cache_fts f ON f.rowid = c.rowid
			WHERE ai_cache_fts MATCH ? AND (c.expires_at IS NULL OR c.expires_at > ?)
			LIMIT 10;
		`, cleanedQuery, nowStr)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

	var result []AICacheEntry
	for rows.Next() {
		var entry AICacheEntry
		var createdStr, expiresStr sql.NullString
		var embBytes []byte
		err := rows.Scan(&entry.Prompt, &entry.Response, &embBytes, &createdStr, &entry.Tokens, &expiresStr)
		if err != nil {
			continue
		}

		if createdStr.Valid {
			entry.Created, _ = time.Parse(time.RFC3339, createdStr.String)
		}
		if expiresStr.Valid {
			t, _ := time.Parse(time.RFC3339, expiresStr.String)
			entry.ExpiresAt = &t
		}

		if len(embBytes) > 0 {
			_ = json.Unmarshal(embBytes, &entry.Embedding)
		}
		result = append(result, entry)
	}
	return result, nil
}

// searchCacheExact tenta encontrar uma resposta exata baseada no prompt (Short-circuit).
func (g *AIGateway) searchCacheExact(ctx context.Context, prompt string) (*AICacheEntry, error) {
	db := runtime.GetDB()
	if db != nil {
		db.RLock()
		defer db.RUnlock()

		nowStr := time.Now().Format(time.RFC3339)
		var entry AICacheEntry
		var createdStr, expiresStr sql.NullString
		var embBytes []byte

		err := db.DB().QueryRowContext(ctx, `
			SELECT prompt, response, embedding, created, tokens, expires_at
			FROM ai_cache
			WHERE prompt = ? AND (expires_at IS NULL OR expires_at > ?)
			LIMIT 1;
		`, prompt, nowStr).Scan(&entry.Prompt, &entry.Response, &embBytes, &createdStr, &entry.Tokens, &expiresStr)

		if err == nil {
			if createdStr.Valid {
				entry.Created, _ = time.Parse(time.RFC3339, createdStr.String)
			}
			if expiresStr.Valid {
				t, _ := time.Parse(time.RFC3339, expiresStr.String)
				entry.ExpiresAt = &t
			}
			if len(embBytes) > 0 {
				_ = json.Unmarshal(embBytes, &entry.Embedding)
			}
			return &entry, nil
		}
	}

	// Fallback para bbolt KVStore se o SQLite não estiver disponível
	if g.kvStore != nil {
		keys := g.kvStore.Keys()
		for _, k := range keys {
			if strings.HasPrefix(k, "_sys:aicache:") {
				data := g.kvStore.Get(k)
				if data != nil {
					var entry AICacheEntry
					if err := json.Unmarshal(data, &entry); err == nil {
						if entry.Prompt == prompt {
							if entry.ExpiresAt != nil && time.Now().After(*entry.ExpiresAt) {
								_ = g.kvStore.Delete(k)
								continue
							}
							return &entry, nil
						}
					}
				}
			}
		}
	}

	return nil, nil
}

func cleanFTSQueryString(q string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9\s]`)
	cleaned := reg.ReplaceAllString(q, " ")
	words := strings.Fields(cleaned)
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " OR ")
}

// ForwardStream intercepta requisições de completions de streaming (SSE).
func (g *AIGateway) ForwardStream(ctx context.Context, bodyBytes []byte, w http.ResponseWriter) error {
	g.mu.RLock()
	openAIKey := g.config.OpenAIKey
	anthropicKey := g.config.AnthropicKey
	defaultProvider := g.config.DefaultProvider
	fallbackProvider := g.config.FallbackProvider
	g.mu.RUnlock()

	// 1. Identificar o prompt da última mensagem do usuário
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	prompt := ""
	if err := json.Unmarshal(bodyBytes, &parsed); err == nil && len(parsed.Messages) > 0 {
		for i := len(parsed.Messages) - 1; i >= 0; i-- {
			if parsed.Messages[i].Role == "user" {
				prompt = parsed.Messages[i].Content
				break
			}
		}
	}

	var req *http.Request
	var clientErr error

	if defaultProvider == "openai" && openAIKey != "" {
		req, _ = http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+openAIKey)
		req.Header.Set("Content-Type", "application/json")
	} else if defaultProvider == "anthropic" && anthropicKey != "" {
		req, _ = http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
		req.Header.Set("x-api-key", anthropicKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
	} else {
		return fmt.Errorf("no default provider API key configured")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, clientErr := client.Do(req)

	// Lógica de Fallback de Roteamento Dinâmico se falhar a API principal
	if clientErr != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		if defaultProvider == "openai" && fallbackProvider == "anthropic" && anthropicKey != "" {
			translated, _ := TranslateOpenAIToAnthropic(bodyBytes)
			req, _ = http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(translated))
			req.Header.Set("x-api-key", anthropicKey)
			req.Header.Set("anthropic-version", "2023-06-01")
			req.Header.Set("Content-Type", "application/json")
			resp, clientErr = client.Do(req)
		} else if defaultProvider == "anthropic" && fallbackProvider == "openai" && openAIKey != "" {
			translated, _ := TranslateAnthropicToOpenAI(bodyBytes)
			req, _ = http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(translated))
			req.Header.Set("Authorization", "Bearer "+openAIKey)
			req.Header.Set("Content-Type", "application/json")
			resp, clientErr = client.Do(req)
		}
	}

	if clientErr != nil {
		return clientErr
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream API returned error %d: %s", resp.StatusCode, string(bodyErr))
	}

	// Configura os headers de streaming para o cliente da WebUI
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var responseBuilder strings.Builder
	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		// Repassa a linha em tempo real para o cliente
		_, _ = w.Write([]byte(line))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Parse textual simples para acumular a resposta gerada para o cache semântico
		if strings.HasPrefix(line, "data:") {
			dataContent := strings.TrimSpace(line[5:])
			if dataContent == "[DONE]" {
				continue
			}

			// Tenta OpenAI Delta text parsing
			var openAIDelta struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if errJson := json.Unmarshal([]byte(dataContent), &openAIDelta); errJson == nil && len(openAIDelta.Choices) > 0 {
				responseBuilder.WriteString(openAIDelta.Choices[0].Delta.Content)
			} else {
				// Tenta Anthropic event parsing
				var anthropicDelta struct {
					Delta struct {
						Text string `json:"text"`
					} `json:"delta"`
				}
				if errJson2 := json.Unmarshal([]byte(dataContent), &anthropicDelta); errJson2 == nil {
					responseBuilder.WriteString(anthropicDelta.Delta.Text)
				}
			}
		}
	}

	// 5. Após fechar o streaming com sucesso, salva a resposta no cache semântico de forma assíncrona
	finalReply := responseBuilder.String()
	if prompt != "" && finalReply != "" {
		go func() {
			var embVector []float64
			g.mu.RLock()
			mode := g.config.Mode
			openAIKey := g.config.OpenAIKey
			g.mu.RUnlock()

			if mode == CachingModeEmbeddings && openAIKey != "" {
				embVector, _ = g.fetchOpenAIEmbedding(context.Background(), prompt, openAIKey)
			}
			g.SaveCache(prompt, finalReply, embVector, len(strings.Fields(finalReply)))
		}()
	}

	return nil
}

// TRANSLATION PAYLOAD UTILITIES

func TranslateOpenAIToAnthropic(openaiJSON []byte) ([]byte, error) {
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature float64 `json:"temperature,omitempty"`
		MaxTokens   int     `json:"max_tokens,omitempty"`
	}
	if err := json.Unmarshal(openaiJSON, &parsed); err != nil {
		return nil, err
	}

	type anthropicMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	var systemPrompt string
	var messages []anthropicMsg

	for _, m := range parsed.Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
		} else {
			role := m.Role
			if role != "assistant" {
				role = "user"
			}
			messages = append(messages, anthropicMsg{
				Role:    role,
				Content: m.Content,
			})
		}
	}

	maxT := parsed.MaxTokens
	if maxT <= 0 {
		maxT = 1024
	}

	payload := map[string]interface{}{
		"model":      "claude-3-5-sonnet-20241022",
		"max_tokens": maxT,
		"messages":   messages,
	}
	if systemPrompt != "" {
		payload["system"] = systemPrompt
	}
	if parsed.Temperature > 0 {
		payload["temperature"] = parsed.Temperature
	}

	return json.Marshal(payload)
}

func TranslateAnthropicResponseToOpenAI(anthropicResp []byte) ([]byte, error) {
	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(anthropicResp, &parsed); err != nil {
		return nil, err
	}

	content := ""
	if len(parsed.Content) > 0 {
		content = parsed.Content[0].Text
	}

	total := parsed.Usage.InputTokens + parsed.Usage.OutputTokens

	res := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-sonic-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "claude-fallback-wrapped",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     parsed.Usage.InputTokens,
			"completion_tokens": parsed.Usage.OutputTokens,
			"total_tokens":      total,
		},
	}

	return json.Marshal(res)
}

func TranslateAnthropicToOpenAI(anthropicJSON []byte) ([]byte, error) {
	var parsed struct {
		System   string `json:"system,omitempty"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature float64 `json:"temperature,omitempty"`
		MaxTokens   int     `json:"max_tokens,omitempty"`
	}
	if err := json.Unmarshal(anthropicJSON, &parsed); err != nil {
		return nil, err
	}

	type openaiMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	var messages []openaiMsg
	if parsed.System != "" {
		messages = append(messages, openaiMsg{
			Role:    "system",
			Content: parsed.System,
		})
	}

	for _, m := range parsed.Messages {
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, openaiMsg{
			Role:    role,
			Content: m.Content,
		})
	}

	payload := map[string]interface{}{
		"model":    "gpt-4o-mini",
		"messages": messages,
	}
	if parsed.Temperature > 0 {
		payload["temperature"] = parsed.Temperature
	}
	if parsed.MaxTokens > 0 {
		payload["max_tokens"] = parsed.MaxTokens
	}

	return json.Marshal(payload)
}

func TranslateOpenAIResponseToAnthropic(openaiResp []byte) ([]byte, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(openaiResp, &parsed); err != nil {
		return nil, err
	}

	content := ""
	if len(parsed.Choices) > 0 {
		content = parsed.Choices[0].Message.Content
	}

	res := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": content,
			},
		},
		"usage": map[string]interface{}{
			"input_tokens":  parsed.Usage.PromptTokens,
			"output_tokens": parsed.Usage.CompletionTokens,
		},
	}

	return json.Marshal(res)
}

func (g *AIGateway) getAllCacheEntries() []AICacheEntry {
	if g.kvStore == nil {
		return nil
	}
	keys := g.kvStore.Keys()
	var list []AICacheEntry
	for _, k := range keys {
		if strings.HasPrefix(k, "_sys:aicache:") {
			data := g.kvStore.Get(k)
			if data != nil {
				var entry AICacheEntry
				if err := json.Unmarshal(data, &entry); err == nil {
					// Verifica expiração no BoltDB
					if entry.ExpiresAt != nil && time.Now().After(*entry.ExpiresAt) {
						_ = g.kvStore.Delete(k)
						continue
					}
					list = append(list, entry)
				}
			}
		}
	}
	return list
}

func (g *AIGateway) saveCacheEntry(entry AICacheEntry) {
	if g.kvStore == nil {
		return
	}
	hash := fmt.Sprintf("%x", time.Now().UnixNano())
	data, _ := json.Marshal(entry)
	_ = g.kvStore.Set("_sys:aicache:"+hash, data)
}

func (g *AIGateway) fetchOpenAIEmbedding(ctx context.Context, text string, apiKey string) ([]float64, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"input": text,
		"model": "text-embedding-3-small",
	})
	
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embedding API returned status: %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil || len(parsed.Data) == 0 {
		return nil, fmt.Errorf("failed to parse embedding response")
	}

	return parsed.Data[0].Embedding, nil
}

func (g *AIGateway) forwardOpenAI(ctx context.Context, body []byte, apiKey string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func (g *AIGateway) forwardAnthropic(ctx context.Context, body []byte, apiKey string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func (g *AIGateway) forwardOpenAIFallback(ctx context.Context, prompt string, apiKey string) ([]byte, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	return g.forwardOpenAI(ctx, body, apiKey)
}

func (g *AIGateway) forwardAnthropicFallback(ctx context.Context, prompt string, apiKey string) ([]byte, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model": "claude-3-5-sonnet-20241022",
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	return g.forwardAnthropic(ctx, body, apiKey)
}

func CosineSimilarityVectors(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

func CosineSimilarityText(s1, s2 string) float64 {
	w1 := tokenize(s1)
	w2 := tokenize(s2)

	if len(w1) == 0 || len(w2) == 0 {
		return 0
	}

	vocab := make(map[string]bool)
	for w := range w1 {
		vocab[w] = true
	}
	for w := range w2 {
		vocab[w] = true
	}

	var dotProduct, normA, normB float64
	for w := range vocab {
		v1 := float64(w1[w])
		v2 := float64(w2[w])
		dotProduct += v1 * v2
		normA += v1 * v1
		normB += v2 * v2
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

var cleanRegexp = regexp.MustCompile(`[^a-zA-Z0-9\s]`)

func tokenize(s string) map[string]int {
	s = strings.ToLower(s)
	s = cleanRegexp.ReplaceAllString(s, "")
	words := strings.Fields(s)
	
	freq := make(map[string]int)
	for _, w := range words {
		if len(w) > 2 {
			freq[w]++
		}
	}
	return freq
}

// CheckCacheHit verifica se o corpo da requisição bate com alguma resposta armazenada.
func (g *AIGateway) CheckCacheHit(ctx context.Context, reqBody []byte) *AICacheEntry {
	g.mu.RLock()
	active := g.config.Active
	mode := g.config.Mode
	threshold := g.config.Threshold
	openAIKey := g.config.OpenAIKey
	g.mu.RUnlock()

	if !active {
		return nil
	}

	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqBody, &parsed); err != nil || len(parsed.Messages) == 0 {
		return nil
	}

	prompt := ""
	for i := len(parsed.Messages) - 1; i >= 0; i-- {
		if parsed.Messages[i].Role == "user" {
			prompt = parsed.Messages[i].Content
			break
		}
	}
	if prompt == "" {
		return nil
	}

	var hitEntry *AICacheEntry
	var bestScore float64

	// Atalho rápido: correspondência exata primeiro (Short-circuit)
	exactHit, errExact := g.searchCacheExact(ctx, prompt)
	if errExact == nil && exactHit != nil {
		hitEntry = exactHit
		bestScore = 1.0
	}

	var cachedEntries []AICacheEntry
	var searchErr error

	if hitEntry == nil {
		if runtime.GetDB() != nil {
			cachedEntries, searchErr = g.searchCacheFTS(ctx, prompt)
		}
		if runtime.GetDB() == nil || searchErr != nil {
			cachedEntries = g.getAllCacheEntries()
		}

		if mode == CachingModeOffline {
			for _, entry := range cachedEntries {
				score := CosineSimilarityText(prompt, entry.Prompt)
				if score > bestScore {
					bestScore = score
					hitEntry = &entry
				}
			}
		} else if mode == CachingModeEmbeddings && openAIKey != "" && len(cachedEntries) > 0 {
			emb, err := g.fetchOpenAIEmbedding(ctx, prompt, openAIKey)
			if err == nil {
				for _, entry := range cachedEntries {
					if len(entry.Embedding) > 0 {
						score := CosineSimilarityVectors(emb, entry.Embedding)
						if score > bestScore {
							bestScore = score
							hitEntry = &entry
						}
					}
				}
			}
		}
	}

	if hitEntry != nil && bestScore >= threshold {
		return hitEntry
	}
	return nil
}
