package runtime

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PipelineStepType define o tipo de passo no pipeline.
type PipelineStepType string

const (
	StepTypeCVEScanner PipelineStepType = "cve_scanner"
	StepTypeWorker     PipelineStepType = "worker"
	StepTypeRateLimiter PipelineStepType = "rate_limiter"
)

var (
	regexCache       sync.Map // string -> *regexp.Regexp
	pipelinesCache   []Pipeline
	pipelinesCacheMu sync.RWMutex
	pipelinesLoaded  bool
)

func getCompiledRegex(pattern string) (*regexp.Regexp, error) {
	if val, ok := regexCache.Load(pattern); ok {
		return val.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// PipelineStep representa um passo individual configurado no middleware.
type PipelineStep struct {
	Type    PipelineStepType `json:"type"`
	Enabled bool             `json:"enabled"`
	Name    string           `json:"name,omitempty"`    // Nome do worker (ex: "auth.js")
	Limit   int              `json:"limit,omitempty"`   // Limite do rate limiter
	Window  int              `json:"window,omitempty"`  // Janela do rate limiter (em milissegundos)
}

// Pipeline representa um fluxo de processamento de pacotes orquestrado visualmente.
type Pipeline struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Pattern  string         `json:"pattern"` // Padrão de URL (ex: "api.exemplo.com/v1/*")
	Methods  []string       `json:"methods"` // Métodos HTTP (ex: ["GET", "POST"])
	Steps    []PipelineStep `json:"steps"`
	Enabled  bool           `json:"enabled"`
	Priority int            `json:"priority"` // Prioridade de execução (maior executa primeiro)
}

// Match verifica se o pacote (Request) corresponde a este pipeline.
func (p *Pipeline) Match(req *Request) bool {
	if !p.Enabled || req == nil {
		return false
	}

	// 1. Validar Método HTTP (se especificado)
	if len(p.Methods) > 0 {
		matchedMethod := false
		reqMethod := strings.ToUpper(req.Method)
		for _, m := range p.Methods {
			if strings.ToUpper(m) == reqMethod {
				matchedMethod = true
				break
			}
		}
		if !matchedMethod {
			return false
		}
	}

	// 2. Validar Padrão de URL (Host + Path)
	// Limpar URLs e comparar
	cleanURL := req.URL
	// Remover schema se houver
	if idx := strings.Index(cleanURL, "://"); idx >= 0 {
		cleanURL = cleanURL[idx+3:]
	}

	// Suporte a Regex se iniciar com "re:"
	if strings.HasPrefix(p.Pattern, "re:") {
		regexStr := p.Pattern[3:]
		re, err := getCompiledRegex(regexStr)
		if err == nil {
			return re.MatchString(cleanURL)
		}
	}

	return MatchPattern(cleanURL, p.Pattern)
}

// MatchPattern implementa uma correspondência simplificada com suporte a curingas (*).
func MatchPattern(val, pattern string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}

	// Se não tiver curingas, correspondência exata
	if !strings.Contains(pattern, "*") {
		return val == pattern
	}

	// Correspondência baseada em curingas simples (*.exemplo.com ou api.exemplo.com/*)
	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		prefix := parts[0]
		suffix := parts[1]
		if prefix != "" && !strings.HasPrefix(val, prefix) {
			return false
		}
		if suffix != "" && !strings.HasSuffix(val, suffix) {
			return false
		}
		return true
	}

	// Correspondência genérica simplificada (usa filepath.Match adaptado)
	matched, err := filepath.Match(pattern, val)
	if err == nil && matched {
		return true
	}

	// Fallback para index de sub-elementos se for parecido com um path
	return strings.Contains(val, strings.ReplaceAll(pattern, "*", ""))
}

// InvalidatePipelinesCache limpa o cache em memória dos pipelines, forçando um reload.
func InvalidatePipelinesCache() {
	pipelinesCacheMu.Lock()
	pipelinesLoaded = false
	pipelinesCache = nil
	pipelinesCacheMu.Unlock()
}

// SavePipeline guarda o pipeline no KV Store e invalida o cache.
func SavePipeline(kv *KVStore, p *Pipeline) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	err = kv.Set("_sys:pipeline:"+p.ID, data)
	if err == nil {
		InvalidatePipelinesCache()
	}
	return err
}

// DeletePipeline remove o pipeline do KV Store e invalida o cache.
func DeletePipeline(kv *KVStore, id string) error {
	err := kv.Delete("_sys:pipeline:" + id)
	if err == nil {
		InvalidatePipelinesCache()
	}
	return err
}

// GetPipelines recupera todos os pipelines cadastrados no KV Store usando cache em RAM rápida.
func GetPipelines(kv *KVStore) ([]Pipeline, error) {
	pipelinesCacheMu.RLock()
	if pipelinesLoaded {
		listCopy := make([]Pipeline, len(pipelinesCache))
		copy(listCopy, pipelinesCache)
		pipelinesCacheMu.RUnlock()
		return listCopy, nil
	}
	pipelinesCacheMu.RUnlock()

	pipelinesCacheMu.Lock()
	defer pipelinesCacheMu.Unlock()

	// Double-check lock
	if pipelinesLoaded {
		listCopy := make([]Pipeline, len(pipelinesCache))
		copy(listCopy, pipelinesCache)
		return listCopy, nil
	}

	keys := kv.Keys()
	var list []Pipeline
	for _, key := range keys {
		if strings.HasPrefix(key, "_sys:pipeline:") {
			data := kv.Get(key)
			if data != nil {
				var p Pipeline
				if err := json.Unmarshal(data, &p); err == nil {
					list = append(list, p)
				}
			}
		}
	}

	pipelinesCache = list
	pipelinesLoaded = true

	listCopy := make([]Pipeline, len(pipelinesCache))
	copy(listCopy, pipelinesCache)
	return listCopy, nil
}

type rateLimitMemoryEntry struct {
	mu        sync.Mutex
	count     int
	expiresAt int64
}

var (
	memoryRateLimits sync.Map // key: string (ip + ":" + pipelineID), value: *rateLimitMemoryEntry
)

// ExecuteRateLimiter executa a lógica de rate limiting usando RAM rápida.
func ExecuteRateLimiter(kv *KVStore, clientIP, pipelineID string, limit int, windowMs int) bool {
	if limit <= 0 || windowMs <= 0 {
		return true
	}

	key := clientIP + ":" + pipelineID
	now := time.Now().UnixNano()

	// Carrega ou armazena uma nova entrada de rate limit de forma thread-safe
	actual, _ := memoryRateLimits.LoadOrStore(key, &rateLimitMemoryEntry{})
	entry := actual.(*rateLimitMemoryEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Se a janela expirou ou a entrada acabou de ser criada, inicia uma nova janela
	if entry.expiresAt == 0 || now > entry.expiresAt {
		entry.count = 1
		entry.expiresAt = now + int64(windowMs)*int64(time.Millisecond)
		return true
	}

	// Se atingiu o limite de requests permitido na janela
	if entry.count >= limit {
		return false // Bloqueado
	}

	entry.count++
	return true
}


// CleanupExpiredMemoryRateLimits varre o cache de rate limits em RAM e remove entradas expiradas,
// prevenindo vazamento de memória com o tempo sob tráfego de IPs dinâmicos.
func CleanupExpiredMemoryRateLimits() {
	now := time.Now().UnixNano()
	memoryRateLimits.Range(func(key, value interface{}) bool {
		entry := value.(*rateLimitMemoryEntry)
		entry.mu.Lock()
		if entry.expiresAt > 0 && now > entry.expiresAt {
			memoryRateLimits.Delete(key)
		}
		entry.mu.Unlock()
		return true
	})
}
