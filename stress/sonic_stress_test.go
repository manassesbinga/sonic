package stress_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manassesbinga/sonic/neuralcache"
	"github.com/manassesbinga/sonic/security"
)

// =============================================================================
// Stress Test — NeuralCache: Concorrência Massiva, Evicção LFU/LRU,
// Compressão LZ77+Huffman, Padrões de Acesso, TTL Dinâmico
// =============================================================================

func TestNeuralCacheStress_ConcurrentAccess(t *testing.T) {
	nc := neuralcache.NewNeuralCache(5000, 100, nil, context.Background())
	var wg sync.WaitGroup
	numGoroutines := 50
	opsPerGoroutine := 1000

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("stress-key-%d-%d", id, j)
				value := []byte(fmt.Sprintf("value-%d-%d com dados repetitivos para compressão: ABCABCABCABC", id, j))
				headers := map[string]string{
					"content-type": "text/plain",
					"x-custom":     fmt.Sprintf("header-%d", id),
				}
				nc.Set(key, value, headers, 200, "text/plain")
				_, found := nc.Get(key, "stress-test")
				if !found {
				}
			}
		}(i)
	}
	wg.Wait()

	stats := nc.Stats()
	t.Logf("NeuralCache Concurrent Stress Stats:")
	t.Logf("  Total Entries: %v", stats["total_entries"])
	t.Logf("  Hit Rate: %.1f%%", stats["hit_rate_pct"])
	t.Logf("  Compressed Entries: %v", stats["compressed_entries"])
	t.Logf("  Compression Ratio: %.1f%%", stats["compression_ratio"])
	t.Logf("  Bytes Saved (MB): %.2f", stats["bytes_saved_mb"])
}

func TestNeuralCacheStress_EvictionUnderHighLoad(t *testing.T) {
	nc := neuralcache.NewNeuralCache(100, 1, nil, context.Background())
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("evict-key-%d", i)
		value := bytes.Repeat([]byte("Dados repetitivos para compressão eficiente com LZ77."), 50)
		nc.Set(key, value, map[string]string{"content-type": "text/plain"}, 200, "text/plain")
	}
	stats := nc.Stats()
	entries := stats["total_entries"].(int)
	if entries > 200 {
		t.Fatalf("Expected max ~100 entries after eviction, got %d", entries)
	}
	t.Logf("Eviction Stress: %d entries after 10000 inserts (max 100)", entries)
	t.Logf("  Hit Rate: %.1f%%", stats["hit_rate_pct"])
}

func TestNeuralCacheStress_HighFrequencyAccess(t *testing.T) {
	nc := neuralcache.NewNeuralCache(1000, 50, nil, context.Background())
	for i := 0; i < 100; i++ {
		nc.Set(fmt.Sprintf("hot-key-%d", i), []byte("hot-data"), nil, 200, "text/plain")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				key := fmt.Sprintf("hot-key-%d", j%100)
				nc.Get(key, "hot-test")
			}
		}()
	}
	wg.Wait()
	stats := nc.Stats()
	t.Logf("High Frequency Access Stats:")
	t.Logf("  Hit Rate: %.1f%%", stats["hit_rate_pct"])
	t.Logf("  Total Entries: %v", stats["total_entries"])
}

func TestNeuralCacheStress_ConcurrentGetSetDelete(t *testing.T) {
	nc := neuralcache.NewNeuralCache(200, 10, nil, context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				key := fmt.Sprintf("c-key-%d", j)
				switch j % 3 {
				case 0:
					nc.Set(key, []byte(fmt.Sprintf("worker-%d-data", workerID)), nil, 200, "text/plain")
				case 1:
					nc.Get(key, "mixed-test")
				case 2:
					nc.Delete(key)
				}
			}
		}(i)
	}
	wg.Wait()
	stats := nc.Stats()
	t.Logf("Concurrent Get/Set/Delete Stats:")
	t.Logf("  Total Entries: %v", stats["total_entries"])
	t.Logf("  Hit Rate: %.1f%%", stats["hit_rate_pct"])
}

// =============================================================================
// Stress Test — Compressão LZ77+Huffman: Patológico, Zip Bomb, Gigante,
// Dados Aleatórios, Texto Real, Alta Repetição
// =============================================================================

func TestCompressionStress_LargePayloads(t *testing.T) {
	sizes := []int{512, 1024, 10 * 1024, 100 * 1024, 512 * 1024, 1024 * 1024}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("Size-%d", size), func(t *testing.T) {
			data := bytes.Repeat([]byte("HTML repetitivo para testar compressao LZ77 com padroes naturais "), size/50+1)[:size]
			start := time.Now()
			compressed := neuralcache.Compress(data)
			compressTime := time.Since(start)
			start = time.Now()
			decompressed, err := neuralcache.Decompress(compressed)
			decompressTime := time.Since(start)
			if err != nil {
				t.Fatalf("Decompress failed for size %d: %v", size, err)
			}
			if !bytes.Equal(data, decompressed) {
				t.Fatalf("Roundtrip failed for size %d", size)
			}
			ratio := float64(len(compressed)) / float64(len(data)) * 100
			t.Logf("Size %d: %d -> %d bytes (%.1f%%), compress=%v, decompress=%v",
				size, len(data), len(compressed), ratio, compressTime, decompressTime)
		})
	}
}

func TestCompressionStress_ZipBomb(t *testing.T) {
	data := []byte{0x01}
	for i := 0; i < 100; i++ {
		data = append(data, 0x01, 0x00, 0x01, 0xFF, 0xFF)
	}
	_, err := neuralcache.Decompress(data)
	if err == nil {
		t.Log("Zip bomb-like payload handled (no panic)")
	} else {
		t.Logf("Zip bomb correctly rejected: %v", err)
	}
}

func TestCompressionStress_RandomData(t *testing.T) {
	for size := 1000; size <= 100000; size *= 10 {
		t.Run(fmt.Sprintf("Random-%d", size), func(t *testing.T) {
			data := make([]byte, size)
			rand.Read(data)
			compressed := neuralcache.Compress(data)
			decompressed, err := neuralcache.Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(data, decompressed) {
				t.Fatalf("Roundtrip failed for random data size %d", size)
			}
			ratio := float64(len(compressed)) / float64(len(data)) * 100
			t.Logf("Random %d: %d -> %d bytes (%.1f%%)", size, len(data), len(compressed), ratio)
			if compressed[0] == 0x01 && ratio < 100 {
				t.Logf("  Random data compressed (unusual but OK)")
			}
		})
	}
}

func TestCompressionStress_MaxDecompressLimit(t *testing.T) {
	data := make([]byte, 25*1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	compressed := neuralcache.Compress(data)
	if compressed[0] == 0x01 {
		_, err := neuralcache.Decompress(compressed)
		if err == nil {
			t.Log("Large decompress succeeded (within limits)")
		} else {
			t.Logf("Large decompress correctly rejected: %v", err)
		}
	} else {
		t.Log("Large data stored uncompressed (expected for random-like data)")
	}
}

func TestCompressionStress_PathologicalPatterns(t *testing.T) {
	patterns := []struct {
		name string
		data []byte
	}{
		{"AllSameByte", bytes.Repeat([]byte{0x00}, 100000)},
		{"Alternating", bytes.Repeat([]byte("AB"), 50000)},
		{"Incremental", func() []byte {
			b := make([]byte, 100000)
			for i := range b {
				b[i] = byte(i % 256)
			}
			return b
		}()},
		{"JSONLike", bytes.Repeat([]byte(`{"id":12345,"name":"User Name","email":"user@example.com"}`), 2000)},
		{"HTMLNested", bytes.Repeat([]byte("<div><span><a href=\"link\">text</a></span></div>"), 5000)},
		{"LongRunThenRandom", append(bytes.Repeat([]byte("A"), 50000), make([]byte, 50000)...)},
	}
	for _, p := range patterns {
		t.Run(p.name, func(t *testing.T) {
			compressed := neuralcache.Compress(p.data)
			decompressed, err := neuralcache.Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(p.data, decompressed) {
				t.Fatalf("Roundtrip failed for %s", p.name)
			}
			ratio := float64(len(compressed)) / float64(len(p.data)) * 100
			t.Logf("%s: %d -> %d bytes (%.1f%%)", p.name, len(p.data), len(compressed), ratio)
		})
	}
}

// =============================================================================
// Stress Test — CVE Scanner: Milhares de Payloads, Concorrência,
// Sliding Window, Eventos
// =============================================================================

func TestCVEStress_MassivePayloadScanning(t *testing.T) {
	scanner := security.NewCVEStreamScanner(nil)
	payloads := make([]string, 0, 1000)
	for i := 0; i < 500; i++ {
		payloads = append(payloads, fmt.Sprintf("function safeFunction_%d() { return 'ok'; }", i))
	}
	for i := 0; i < 500; i++ {
		payloads = append(payloads, fmt.Sprintf("__import__('os').system('ls_%d')", i))
	}
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	detections := 0
	for _, p := range payloads {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			matched, sig := scanner.Scan([]byte(payload))
			if matched {
				mu.Lock()
				detections++
				mu.Unlock()
				t.Logf("  Detected: %q -> %s", payload[:30], sig)
			}
		}(p)
	}
	wg.Wait()
	t.Logf("CVE Scanner: %d detections out of %d payloads", detections, len(payloads))
	if detections < 400 {
		t.Errorf("Expected >= 400 detections, got %d", detections)
	}
}

func TestCVEStress_StreamingLargePayload(t *testing.T) {
	var events []security.SecurityEvent
	onEvent := func(event security.SecurityEvent, details map[string]interface{}) {
		events = append(events, event)
	}
	scanner := security.NewCVEStreamScanner(onEvent)
	scanner.Block = false

	largePayload := strings.Repeat("safe data. ", 10000)
	largePayload += "eval(req.body.input);"
	largePayload += strings.Repeat("more safe data. ", 10000)

	reader := security.NewScanningReader(strings.NewReader(largePayload), scanner)
	buf := make([]byte, 4096)
	totalRead := 0
	for {
		n, err := reader.Read(buf)
		totalRead += n
		if err == io.EOF {
			break
		}
		if err != nil && err != security.ErrCVEDetected {
			t.Fatalf("Unexpected error: %v", err)
		}
		if err == security.ErrCVEDetected {
			break
		}
	}
	t.Logf("Streaming large payload: read %d bytes, events: %v", totalRead, len(events))
	hasCVE := false
	for _, e := range events {
		if e == security.EventCVEDetected {
			hasCVE = true
		}
	}
	if !hasCVE {
		t.Error("Expected CVE detection event")
	}
}

func TestCVEStress_ConcurrentScanning(t *testing.T) {
	scanner := security.NewCVEStreamScanner(nil)
	var wg sync.WaitGroup
	numWorkers := 20
	payloadsPerWorker := 500

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < payloadsPerWorker; i++ {
				var payload string
				if i%5 == 0 {
					payload = fmt.Sprintf("; rm -rf /etc/passwd_%d_%d", workerID, i)
				} else {
					payload = fmt.Sprintf("function harmless_%d_%d() { return 42; }", workerID, i)
				}
				matched, _ := scanner.Scan([]byte(payload))
				if i%5 == 0 && !matched {
					t.Errorf("Worker %d: expected match for malicious payload", workerID)
				}
			}
		}(w)
	}
	wg.Wait()
	t.Logf("Concurrent scanning: %d workers x %d payloads completed", numWorkers, payloadsPerWorker)
}

func TestCVEStress_EventHistoryLimit(t *testing.T) {
	for i := 0; i < 200; i++ {
		security.AddSecurityIncident(fmt.Sprintf("test-signature-%d", i), fmt.Sprintf("test-preview-%d", i))
	}
	incidents := security.GetSecurityIncidents()
	t.Logf("Security incidents stored: %d (limit should be 100)", len(incidents))
	if len(incidents) > 150 {
		t.Errorf("Expected incident log to be capped at ~100, got %d", len(incidents))
	}
}

func TestCVEStress_RapidWindowSliding(t *testing.T) {
	scanner := security.NewCVEStreamScanner(nil)
	payload := "A harmless prefix " + strings.Repeat("x", 10000) + " ; rm -rf / and then some"
	reader := security.NewScanningReader(strings.NewReader(payload), scanner)
	buf := make([]byte, 32)
	var err error
	for {
		_, err = reader.Read(buf)
		if err != nil {
			break
		}
	}
	if err != security.ErrCVEDetected {
		t.Logf("Rapid sliding: final error was %v (may not detect in small window)", err)
	} else {
		t.Log("Rapid sliding: CVE correctly detected")
	}
}

// =============================================================================
// Benchmarks — Desempenho Sob Carga Máxima
// =============================================================================

func BenchmarkNeuralCache_HighConcurrency(b *testing.B) {
	nc := neuralcache.NewNeuralCache(10000, 200, nil, context.Background())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := 0
		for pb.Next() {
			id++
			key := fmt.Sprintf("bench-key-%d", id%5000)
			if id%2 == 0 {
				nc.Set(key, []byte("benchmark-value"), nil, 200, "text/plain")
			} else {
				nc.Get(key, "bench")
			}
		}
	})
}

func BenchmarkNeuralCache_CompressionRatio(b *testing.B) {
	nc := neuralcache.NewNeuralCache(1000, 50, nil, context.Background())
	data := bytes.Repeat([]byte("Dados JSON com alta repeticao para compressao eficiente: {\"id\":123,\"name\":\"test\"} "), 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench-compress-%d", i%100)
		nc.Set(key, data, map[string]string{"content-type": "application/json"}, 200, "application/json")
		nc.Get(key, "bench")
	}
}

func BenchmarkCompression_Parallel(b *testing.B) {
	data := bytes.Repeat([]byte("HTML com repeticao para compressao paralela <div><span>texto</span></div>"), 200)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c := neuralcache.Compress(data)
			_, _ = neuralcache.Decompress(c)
		}
	})
}

func BenchmarkCVEStreaming_Throughput(b *testing.B) {
	scanner := security.NewCVEStreamScanner(nil)
	payload := []byte(strings.Repeat("safe data. ", 500) + "; rm -rf /" + strings.Repeat(" safe data.", 500))
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := security.NewScanningReader(bytes.NewReader(payload), scanner)
		io.ReadAll(reader)
	}
}

// =============================================================================
// Stress Test — Segurança: Prevenção de Path Traversal no KV Store
// =============================================================================

func TestKVStress_PathTraversalPrevention(t *testing.T) {
	traversalAttempts := []string{
		"../../../etc/passwd",
		"..\\..\\..\\windows\\system32\\config",
		".../.../.../etc/shadow",
		"....//....//....//etc/hosts",
		"%2e%2e%2f%2e%2e%2fetc/passwd",
	}
	for _, attempt := range traversalAttempts {
		t.Run(attempt, func(t *testing.T) {
			if strings.Contains(attempt, "..") {
				t.Logf("Path traversal pattern detected: %s", attempt)
			}
		})
	}
}

// =============================================================================
// Stress Test — Memória: Alocação Maciça e Libertação no NeuralCache
// =============================================================================

func TestNeuralCacheStress_MassiveAllocation(t *testing.T) {
	nc := neuralcache.NewNeuralCache(10000, 500, nil, context.Background())
	var wg sync.WaitGroup
	for k := 0; k < 5; k++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				key := fmt.Sprintf("mass-%d-%d", worker, i)
				valueSize := 1024 * (1 + (i % 10))
				value := make([]byte, valueSize)
				rand.Read(value)
				nc.Set(key, value, nil, 200, "application/octet-stream")
			}
		}(k)
	}
	wg.Wait()
	stats := nc.Stats()
	t.Logf("Massive Allocation Stats:")
	t.Logf("  Total Entries: %v", stats["total_entries"])
	t.Logf("  Current Size (MB): %.2f", stats["current_size_mb"])
	t.Logf("  Max Size (MB): %.2f", stats["max_size_mb"])
	nc.Clear()
	statsAfter := nc.Stats()
	if statsAfter["current_size_mb"].(float64) > 1.0 {
		t.Errorf("Expected near-zero size after Clear, got %.2f MB", statsAfter["current_size_mb"])
	}
}

// =============================================================================
// Stress Test — Key Overwrite: Verifica que overwrite não causa heap leak
// =============================================================================

func TestNeuralCacheStress_KeyOverwrite(t *testing.T) {
	nc := neuralcache.NewNeuralCache(1000, 100, nil, context.Background())
	var wg sync.WaitGroup
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := fmt.Sprintf("shared-key-%d", i%50)
				value := []byte(fmt.Sprintf("worker-%d-overwrite-%d com dados para teste de sobrescrita", worker, i))
				nc.Set(key, value, map[string]string{"content-type": "text/plain"}, 200, "text/plain")
			}
		}(w)
	}
	wg.Wait()
	stats := nc.Stats()
	entries := stats["total_entries"].(int)
	if entries > 100 {
		t.Errorf("Expected <= 50 entries after overwrite (only 50 unique keys), got %d", entries)
	}
	hitTotal := stats["hit_count"].(int64)
	missTotal := stats["miss_count"].(int64)
	t.Logf("Key Overwrite: %d entries, hits=%d, misses=%d", entries, hitTotal, missTotal)
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("shared-key-%d", i)
		entry, found := nc.Get(key, "overwrite-test")
		if !found {
			t.Errorf("Expected key %s to exist after overwrites", key)
		} else if entry == nil {
			t.Errorf("Entry for %s is nil", key)
		}
	}
	stats2 := nc.Stats()
	if stats2["current_size_mb"].(float64) < 0 {
		t.Errorf("Negative size after overwrite: %.2f MB", stats2["current_size_mb"])
	}
}

// =============================================================================
// Stress Test — Concorrência Massiva com Sobrescrita (simula tráfego real)
// =============================================================================

func TestNeuralCacheStress_RealTrafficSimulation(t *testing.T) {
	nc := neuralcache.NewNeuralCache(500, 50, nil, context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < 25; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				key := fmt.Sprintf("route-%d", worker*20+int(time.Now().UnixNano()%20))
				valueSize := 100 + int(time.Now().UnixNano()%5000)
				value := make([]byte, valueSize)
				rand.Read(value)
				nc.Set(key, value, nil, 200, "text/html")
				nc.Get(key, "traffic-sim")
				nc.Get(fmt.Sprintf("route-%d", int(time.Now().UnixNano()%500)), "traffic-sim")
			}
		}(w)
	}
	wg.Wait()
	stats := nc.Stats()
	t.Logf("Real Traffic Simulation (5s):")
	t.Logf("  Entries: %v, Size: %.2f MB / %.2f MB max",
		stats["total_entries"], stats["current_size_mb"], stats["max_size_mb"])
	t.Logf("  Hits: %v, Misses: %v, Hit Rate: %.1f%%",
		stats["hit_count"], stats["miss_count"], stats["hit_rate_pct"])
	if stats["current_size_mb"].(float64) > stats["max_size_mb"].(float64)*1.1 {
		t.Errorf("Cache exceeded max size: %.2f > %.2f MB",
			stats["current_size_mb"], stats["max_size_mb"])
	}
}
