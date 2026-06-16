package neuralcache

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manassesbinga/sonic/telemetry"
)

// CacheEntry representa uma entrada no cache neural
type CacheEntry struct {
	Key              string
	Value            []byte
	Headers          map[string]string
	Status           int
	CreatedAt        time.Time
	LastAccessedUnix int64
	AccessCount      int64
	TTL              time.Duration
	ContentType      string
	// Compressed indica se Value está comprimido com LZ77+Huffman
	Compressed   bool
	// OriginalSize guarda o tamanho original antes da compressão (para métricas)
	OriginalSize int64
	// heapIndex rastreia a posição no heap para O(log n) Fix()
	heapIndex int
}

// evictEntry ordena por AccessCount (menor primeiro = LFU) e,
// em caso de empate, por LastAccessedUnix (mais antigo primeiro = LRU).
type evictEntry []*CacheEntry

func (h evictEntry) Len() int           { return len(h) }
func (h evictEntry) Less(i, j int) bool {
	if h[i].AccessCount != h[j].AccessCount {
		return h[i].AccessCount < h[j].AccessCount
	}
	return h[i].LastAccessedUnix < h[j].LastAccessedUnix
}
func (h evictEntry) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}
func (h *evictEntry) Push(x interface{}) {
	n := len(*h)
	entry := x.(*CacheEntry)
	entry.heapIndex = n
	*h = append(*h, entry)
}
func (h *evictEntry) Pop() interface{} {
	old := *h
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	entry.heapIndex = -1
	*h = old[0 : n-1]
	return entry
}

// NeuralCache é o sistema de cache inteligente que aprende com o tráfego
type NeuralCache struct {
	mu           sync.RWMutex
	cache        map[string]*CacheEntry
	evictHeap    evictEntry
	patterns     map[string]*PatternStats
	maxEntries   int
	maxSize      int64
	currentSize  int64
	hitCount     int64
	missCount    int64
	// Métricas de compressão LZ77+Huffman
	compressedEntries int64 // Número de entradas armazenadas comprimidas
	compressedBytes   int64 // Total de bytes comprimidos em memória
	savedBytes        int64 // Total de bytes economizados pela compressão
	metrics      *telemetry.Metrics
	ctx          context.Context
	stopCh       chan struct{}
}

// PatternStats armazena estatísticas sobre padrões de acesso
type PatternStats struct {
	TotalAccesses   int
	AvgResponseSize int64
	LastAccess      time.Time
	RecommendedTTL  time.Duration
}

// NewNeuralCache cria uma nova instância do Neural Cache Layer
func NewNeuralCache(maxEntries int, maxSizeMB int, metrics *telemetry.Metrics, ctx context.Context) *NeuralCache {
	nc := &NeuralCache{
		cache:       make(map[string]*CacheEntry),
		evictHeap:   make(evictEntry, 0, maxEntries),
		patterns:    make(map[string]*PatternStats),
		maxEntries:  maxEntries,
		maxSize:     int64(maxSizeMB) * 1024 * 1024,
		metrics:     metrics,
		ctx:         ctx,
		stopCh:      make(chan struct{}),
	}
	heap.Init(&nc.evictHeap)
	go nc.backgroundCleanup()
	return nc
}

func (nc *NeuralCache) backgroundCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			nc.CleanupExpired()
		case <-nc.stopCh:
			return
		}
	}
}

// GenerateKey gera uma chave única para a requisição
func (nc *NeuralCache) GenerateKey(method, path, body string) string {
	data := method + ":" + path + ":" + body
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// Get busca uma entrada no cache.
// Se a entrada estiver comprimida (LZ77+Huffman), descomprime transparentemente
// antes de retornar ao caller. O caller nunca precisa saber se os dados
// estavam comprimidos.
func (nc *NeuralCache) Get(key string, domain string) (*CacheEntry, bool) {
	nc.mu.RLock()
	entry, exists := nc.cache[key]
	if !exists {
		nc.mu.RUnlock()
		atomic.AddInt64(&nc.missCount, 1)
		if nc.metrics != nil {
			telemetry.RecordNeuralCacheMiss(nc.metrics, nc.ctx, domain)
		}
		return nil, false
	}

	// Verifica se a entrada expirou
	if entry.TTL > 0 && time.Since(entry.CreatedAt) > entry.TTL {
		nc.mu.RUnlock()

		nc.mu.Lock()
		heap.Init(&nc.evictHeap)
		if entry, exists = nc.cache[key]; exists && entry.TTL > 0 && time.Since(entry.CreatedAt) > entry.TTL {
			nc.deleteEntry(key, domain)
		}
		nc.mu.Unlock()

		atomic.AddInt64(&nc.missCount, 1)
		if nc.metrics != nil {
			telemetry.RecordNeuralCacheMiss(nc.metrics, nc.ctx, domain)
		}
		return nil, false
	}

	atomic.StoreInt64(&entry.LastAccessedUnix, time.Now().UnixNano())
	atomic.AddInt64(&entry.AccessCount, 1)
	nc.mu.RUnlock()

	atomic.AddInt64(&nc.hitCount, 1)
	if nc.metrics != nil {
		telemetry.RecordNeuralCacheHit(nc.metrics, nc.ctx, domain)
	}

	// Descomprime transparentemente se os dados estiverem comprimidos
	if entry.Compressed {
		decompressed, err := Decompress(entry.Value)
		if err != nil {
			// Se a descompressão falhar, retorna miss em vez de dados corrompidos
			return nil, false
		}
		// Retorna uma cópia com dados descomprimidos (não altera o entry armazenado)
		decompressedEntry := &CacheEntry{
			Key:              entry.Key,
			Value:            decompressed,
			Headers:          entry.Headers,
			Status:           entry.Status,
			CreatedAt:        entry.CreatedAt,
			LastAccessedUnix: atomic.LoadInt64(&entry.LastAccessedUnix),
			AccessCount:      atomic.LoadInt64(&entry.AccessCount),
			TTL:              entry.TTL,
			ContentType:      entry.ContentType,
			Compressed:       false,
			OriginalSize:     entry.OriginalSize,
		}
		return decompressedEntry, true
	}

	return entry, true
}

// Set armazena uma entrada no cache com TTL automático.
// Se o content-type for compressível e o payload for grande o suficiente,
// os dados são comprimidos automaticamente com LZ77+Huffman antes de armazenar.
// Isto é transparente — o Get() descomprime automaticamente.
func (nc *NeuralCache) Set(key string, value []byte, headers map[string]string, status int, contentType string) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Reconfigura o heap com base nos contadores de acessos atômicos atualizados concorrentemente pelas leituras
	heap.Init(&nc.evictHeap)

	originalSize := int64(len(value))
	compressed := false
	storeValue := value

	// Tenta comprimir se o content-type for elegível
	if ShouldCompress(contentType, len(value)) {
		compressedValue := Compress(value)
		// Usa versão comprimida apenas se realmente reduziu o tamanho
		if int64(len(compressedValue)) < originalSize {
			storeValue = compressedValue
			compressed = true
		}
	}

	// Calcula o tamanho real em memória (comprimido ou não)
	entrySize := int64(len(storeValue))
	for k, v := range headers {
		entrySize += int64(len(k) + len(v))
	}

	// Remove entrada existente se houver (evita heap leak)
	if oldEntry, exists := nc.cache[key]; exists {
		oldSize := int64(len(oldEntry.Value))
		for k, v := range oldEntry.Headers {
			oldSize += int64(len(k) + len(v))
		}
		nc.removeFromHeap(oldEntry)
		delete(nc.cache, key)
		nc.currentSize -= oldSize
		if oldEntry.Compressed {
			nc.compressedEntries--
			nc.compressedBytes -= int64(len(oldEntry.Value))
			nc.savedBytes -= oldEntry.OriginalSize - int64(len(oldEntry.Value))
		}
	}

	// Limpa entradas antigas se necessário
	nc.evictIfNeeded(entrySize, contentType)

	// Determina o TTL automático baseado em padrões
	patternKey := contentType
	ttl := nc.calculateAutoTTL(patternKey, originalSize)

	entry := &CacheEntry{
		Key:              key,
		Value:            storeValue,
		Headers:          headers,
		Status:           status,
		CreatedAt:        time.Now(),
		LastAccessedUnix: time.Now().UnixNano(),
		AccessCount:      1,
		TTL:              ttl,
		ContentType:      contentType,
		Compressed:       compressed,
		OriginalSize:     originalSize,
	}

	nc.cache[key] = entry
	heap.Push(&nc.evictHeap, entry)
	nc.currentSize += entrySize

	// Atualiza métricas de compressão
	if compressed {
		nc.compressedEntries++
		nc.compressedBytes += int64(len(storeValue))
		nc.savedBytes += originalSize - int64(len(storeValue))
	}

	// Atualiza estatísticas do padrão
	nc.updatePatternStats(patternKey, originalSize)
}

// Delete remove uma entrada do cache
func (nc *NeuralCache) Delete(key string) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	heap.Init(&nc.evictHeap)

	if entry, exists := nc.cache[key]; exists {
		entrySize := int64(len(entry.Value))
		for k, v := range entry.Headers {
			entrySize += int64(len(k) + len(v))
		}
		nc.removeFromHeap(entry)
		delete(nc.cache, key)
		nc.currentSize -= entrySize
	}
}

// Clear limpa todo o cache
func (nc *NeuralCache) Clear() {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	nc.cache = make(map[string]*CacheEntry)
	nc.evictHeap = make(evictEntry, 0, nc.maxEntries)
	heap.Init(&nc.evictHeap)
	nc.currentSize = 0
}

// Stats retorna estatísticas do cache, incluindo métricas de compressão
func (nc *NeuralCache) Stats() map[string]interface{} {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	hits := atomic.LoadInt64(&nc.hitCount)
	misses := atomic.LoadInt64(&nc.missCount)
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	// Calcula ratio de compressão
	compBytes := atomic.LoadInt64(&nc.compressedBytes)
	savedBytes := atomic.LoadInt64(&nc.savedBytes)
	compressionRatio := 0.0
	if compBytes+savedBytes > 0 {
		compressionRatio = float64(compBytes) / float64(compBytes+savedBytes) * 100
	}

	return map[string]interface{}{
		"total_entries":      len(nc.cache),
		"current_size_mb":    float64(nc.currentSize) / (1024 * 1024),
		"max_size_mb":        float64(nc.maxSize) / (1024 * 1024),
		"hit_count":          hits,
		"miss_count":         misses,
		"hit_rate_pct":       hitRate,
		"compressed_entries": atomic.LoadInt64(&nc.compressedEntries),
		"compression_ratio":  compressionRatio,
		"bytes_saved_mb":     float64(savedBytes) / (1024 * 1024),
	}
}

// calculateAutoTTL calcula o TTL ideal baseado em padrões históricos
func (nc *NeuralCache) calculateAutoTTL(patternKey string, entrySize int64) time.Duration {
	stats, exists := nc.patterns[patternKey]
	if !exists {
		// TTL padrão baseado no tamanho
		if entrySize < 1024 { // < 1KB
			return 1 * time.Hour
		} else if entrySize < 100*1024 { // < 100KB
			return 30 * time.Minute
		} else if entrySize < 1024*1024 { // < 1MB
			return 15 * time.Minute
		}
		return 5 * time.Minute
	}

	// Ajusta TTL baseado na frequência de acesso
	if stats.TotalAccesses > 100 {
		return 2 * time.Hour
	} else if stats.TotalAccesses > 50 {
		return 1 * time.Hour
	} else if stats.TotalAccesses > 10 {
		return 30 * time.Minute
	}

	return stats.RecommendedTTL
}

// updatePatternStats atualiza as estatísticas de padrões
func (nc *NeuralCache) updatePatternStats(patternKey string, entrySize int64) {
	stats, exists := nc.patterns[patternKey]
	if !exists {
		stats = &PatternStats{
			RecommendedTTL: 30 * time.Minute,
		}
		nc.patterns[patternKey] = stats
	}

	stats.TotalAccesses++
	stats.LastAccess = time.Now()

	// Ajusta a média do tamanho de resposta (EMA com α=0.1)
	if stats.AvgResponseSize == 0 {
		stats.AvgResponseSize = entrySize
	} else {
		stats.AvgResponseSize = int64(float64(stats.AvgResponseSize)*0.9 + float64(entrySize)*0.1)
	}

	// Ajusta o TTL recomendado dinamicamente
	if stats.TotalAccesses > 0 {
		switch {
		case stats.TotalAccesses > 1000:
			stats.RecommendedTTL = 4 * time.Hour
		case stats.TotalAccesses > 100:
			stats.RecommendedTTL = 2 * time.Hour
		case stats.TotalAccesses > 10:
			stats.RecommendedTTL = 1 * time.Hour
		default:
			stats.RecommendedTTL = 30 * time.Minute
		}
	}
}

// removeFromHeap remove uma entry do heap marcando com heapIndex=-1
func (nc *NeuralCache) removeFromHeap(entry *CacheEntry) {
	if entry.heapIndex >= 0 && entry.heapIndex < len(nc.evictHeap) && nc.evictHeap[entry.heapIndex] == entry {
		heap.Remove(&nc.evictHeap, entry.heapIndex)
	}
	entry.heapIndex = -1
}

// evictIfNeeded remove entradas antigas se o cache estiver cheio
func (nc *NeuralCache) evictIfNeeded(neededSize int64, domain string) {
	// Não faz varredura linear de expirados aqui (delegado ao backgroundCleanup).
	// Usa o heap LFU/LRU para evicção em O(log n).

	// Remove padrões antigos se houver mais de 100
	if len(nc.patterns) > 100 {
		var oldestPattern string
		var oldestTime time.Time
		for k, v := range nc.patterns {
			if oldestPattern == "" || v.LastAccess.Before(oldestTime) {
				oldestPattern = k
				oldestTime = v.LastAccess
			}
		}
		if oldestPattern != "" {
			delete(nc.patterns, oldestPattern)
		}
	}

	// Evicção via heap: remove as entradas menos frequentemente usadas (LFU)
	// com desempate LRU (mais antiga primeiro).
	for nc.currentSize+neededSize > nc.maxSize || len(nc.cache) >= nc.maxEntries {
		if nc.evictHeap.Len() == 0 {
			break
		}

		entry := heap.Pop(&nc.evictHeap).(*CacheEntry)
		if entry == nil {
			break
		}
		entry.heapIndex = -1

		entrySize := int64(len(entry.Value))
		for k, v := range entry.Headers {
			entrySize += int64(len(k) + len(v))
		}
		delete(nc.cache, entry.Key)
		nc.currentSize -= entrySize
		if nc.metrics != nil {
			telemetry.RecordNeuralCacheEviction(nc.metrics, nc.ctx, domain)
		}
	}
}

// deleteEntry remove uma entrada e atualiza o tamanho
func (nc *NeuralCache) deleteEntry(key string, domain string) {
	if entry, exists := nc.cache[key]; exists {
		entrySize := int64(len(entry.Value))
		for k, v := range entry.Headers {
			entrySize += int64(len(k) + len(v))
		}
		nc.removeFromHeap(entry)
		delete(nc.cache, key)
		nc.currentSize -= entrySize
		if nc.metrics != nil {
			telemetry.RecordNeuralCacheEviction(nc.metrics, nc.ctx, domain)
		}
	}
}

// Close encerra o background cleanup e libera recursos.
func (nc *NeuralCache) Close() {
	close(nc.stopCh)
}

// CleanupExpired remove todas as entradas expiradas (executado em background a cada 5min)
func (nc *NeuralCache) CleanupExpired() {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	heap.Init(&nc.evictHeap)

	now := time.Now()
	for key, entry := range nc.cache {
		if entry.TTL > 0 && now.Sub(entry.CreatedAt) > entry.TTL {
			nc.removeFromHeap(entry)
			delete(nc.cache, key)
			entrySize := int64(len(entry.Value))
			for k, v := range entry.Headers {
				entrySize += int64(len(k) + len(v))
			}
			nc.currentSize -= entrySize
			if nc.metrics != nil {
				telemetry.RecordNeuralCacheEviction(nc.metrics, nc.ctx, entry.ContentType)
			}
		}
	}
}
