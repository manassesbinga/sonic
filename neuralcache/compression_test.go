package neuralcache

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Testes LZ77
// ---------------------------------------------------------------------------

func TestLZ77EncodeDecode_EmptyInput(t *testing.T) {
	tokens := lz77Encode(nil)
	if len(tokens) != 0 {
		t.Fatalf("expected 0 tokens for empty input, got %d", len(tokens))
	}

	result, err := lz77Decode(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(result))
	}
}

func TestLZ77EncodeDecode_SingleByte(t *testing.T) {
	data := []byte{42}
	tokens := lz77Encode(data)
	if len(tokens) != 1 || tokens[0].IsMatch || tokens[0].Literal != 42 {
		t.Fatalf("expected single literal token, got %+v", tokens)
	}

	serialized := serializeTokens(tokens)
	deserialized, err := deserializeTokens(serialized)
	if err != nil {
		t.Fatalf("deserialize error: %v", err)
	}

	result, err := lz77Decode(deserialized)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !bytes.Equal(data, result) {
		t.Fatalf("roundtrip failed: expected %v, got %v", data, result)
	}
}

func TestLZ77EncodeDecode_RepetitiveData(t *testing.T) {
	// "ABCABCABCABC" — deveria encontrar matches para as repetições
	data := []byte("ABCABCABCABC")

	tokens := lz77Encode(data)

	// Verifica que há pelo menos um match (as repetições devem ser detectadas)
	hasMatch := false
	for _, tok := range tokens {
		if tok.IsMatch {
			hasMatch = true
			break
		}
	}
	if !hasMatch {
		t.Fatal("expected at least one match in repetitive data")
	}

	// Roundtrip completo
	serialized := serializeTokens(tokens)
	deserialized, err := deserializeTokens(serialized)
	if err != nil {
		t.Fatalf("deserialize error: %v", err)
	}

	result, err := lz77Decode(deserialized)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, result) {
		t.Fatalf("roundtrip failed:\n  expected: %q\n  got:      %q", data, result)
	}
}

func TestLZ77EncodeDecode_RunLengthPattern(t *testing.T) {
	// Run de bytes iguais — testa o overlap match (offset=1)
	data := bytes.Repeat([]byte("A"), 100)

	tokens := lz77Encode(data)
	serialized := serializeTokens(tokens)
	deserialized, err := deserializeTokens(serialized)
	if err != nil {
		t.Fatalf("deserialize error: %v", err)
	}

	result, err := lz77Decode(deserialized)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, result) {
		t.Fatalf("run-length roundtrip failed: expected %d bytes, got %d bytes", len(data), len(result))
	}

	// Verifica eficiência: deve usar poucos tokens para dados repetitivos
	// 3 literais (A,A,A) + 1 match(offset=1, length=97) = 4 tokens max
	// Ou similar — o ponto é que deve ser << 100 tokens
	if len(tokens) > 10 {
		t.Fatalf("inefficient encoding: %d tokens for 100 repeated bytes (expected << 10)", len(tokens))
	}
}

func TestLZ77EncodeDecode_LargeData(t *testing.T) {
	// HTML-like data com repetições naturais
	var buf bytes.Buffer
	for i := 0; i < 50; i++ {
		buf.WriteString("<div class=\"item\"><h2>Title</h2><p>Description text content</p></div>\n")
	}
	data := buf.Bytes()

	tokens := lz77Encode(data)
	serialized := serializeTokens(tokens)
	deserialized, err := deserializeTokens(serialized)
	if err != nil {
		t.Fatalf("deserialize error: %v", err)
	}

	result, err := lz77Decode(deserialized)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, result) {
		t.Fatalf("large data roundtrip failed: expected %d bytes, got %d bytes", len(data), len(result))
	}

	// Serialized tokens devem ser menores que os dados originais para HTML repetitivo
	t.Logf("Original: %d bytes, Serialized tokens: %d bytes (%.1f%%)",
		len(data), len(serialized), float64(len(serialized))/float64(len(data))*100)
}

func TestLZ77_DeserializeCorruptedData(t *testing.T) {
	// Flag inválida
	_, err := deserializeTokens([]byte{0xFF})
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for invalid flag, got %v", err)
	}

	// Match truncado (flag 0x01 mas sem offset/length)
	_, err = deserializeTokens([]byte{0x01, 0x00})
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for truncated match, got %v", err)
	}

	// Literal truncado (flag 0x00 sem byte)
	_, err = deserializeTokens([]byte{0x00})
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for truncated literal, got %v", err)
	}
}

func TestLZ77Decode_InvalidOffset(t *testing.T) {
	// Offset que referencia antes do início do output
	tokens := []lz77Token{
		{IsMatch: true, Offset: 10, Length: 5}, // offset=10 mas output está vazio
	}
	_, err := lz77Decode(tokens)
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for invalid offset, got %v", err)
	}

	// Offset zero (inválido)
	tokens = []lz77Token{
		{IsMatch: false, Literal: 'A'},
		{IsMatch: true, Offset: 0, Length: 3},
	}
	_, err = lz77Decode(tokens)
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for zero offset, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Testes Huffman
// ---------------------------------------------------------------------------

func TestHuffmanEncodeDecode_EmptyInput(t *testing.T) {
	result := huffmanEncode(nil)
	if result != nil {
		t.Fatalf("expected nil for empty input, got %d bytes", len(result))
	}

	decoded, err := huffmanDecode(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded != nil {
		t.Fatalf("expected nil for empty compressed, got %d bytes", len(decoded))
	}
}

func TestHuffmanEncodeDecode_SingleSymbol(t *testing.T) {
	// Caso especial: todos os bytes iguais → 1 nó na árvore
	data := bytes.Repeat([]byte{0x42}, 100)

	compressed := huffmanEncode(data)
	if compressed == nil {
		t.Fatal("expected non-nil compressed output")
	}

	decompressed, err := huffmanDecode(compressed)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("single-symbol roundtrip failed")
	}

	// O comprimido deve ser significativamente menor (100 bytes → ~12.5 bytes de bitstream + header)
	t.Logf("Single symbol: %d → %d bytes", len(data), len(compressed))
}

func TestHuffmanEncodeDecode_TwoSymbols(t *testing.T) {
	// Dois símbolos com frequências diferentes
	data := make([]byte, 100)
	for i := 0; i < 80; i++ {
		data[i] = 'A' // 80%
	}
	for i := 80; i < 100; i++ {
		data[i] = 'B' // 20%
	}

	compressed := huffmanEncode(data)
	decompressed, err := huffmanDecode(compressed)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("two-symbol roundtrip failed")
	}

	// 2 símbolos → 1 bit por símbolo → 100 bits = 12.5 bytes + header
	t.Logf("Two symbols: %d → %d bytes", len(data), len(compressed))
}

func TestHuffmanEncodeDecode_AllBytes(t *testing.T) {
	// Todos os 256 valores de byte possíveis
	data := make([]byte, 256)
	for i := 0; i < 256; i++ {
		data[i] = byte(i)
	}

	compressed := huffmanEncode(data)
	decompressed, err := huffmanDecode(compressed)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("all-bytes roundtrip failed")
	}
}

func TestHuffmanEncodeDecode_NaturalText(t *testing.T) {
	data := []byte("The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog again and again.")

	compressed := huffmanEncode(data)
	decompressed, err := huffmanDecode(compressed)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("natural text roundtrip failed:\n  expected: %q\n  got:      %q", data, decompressed)
	}

	t.Logf("Natural text: %d → %d bytes (%.1f%% of original)",
		len(data), len(compressed), float64(len(compressed))/float64(len(data))*100)
}

func TestHuffmanDecode_CorruptedData(t *testing.T) {
	// Truncated header
	_, err := huffmanDecode([]byte{0x01})
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData, got %v", err)
	}

	// Invalid padding bits (> 7)
	data := make([]byte, 4+2+1)
	data[6] = 8 // padding = 8 (invalid)
	_, err = huffmanDecode(data)
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for invalid padding, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Testes do Pipeline Completo (Compress + Decompress)
// ---------------------------------------------------------------------------

func TestCompressDecompress_EmptyInput(t *testing.T) {
	compressed := Compress(nil)
	if len(compressed) != 1 || compressed[0] != flagUncompressed {
		t.Fatalf("expected single uncompressed flag, got %v", compressed)
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}
	if len(decompressed) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(decompressed))
	}
}

func TestCompressDecompress_SmallPayload(t *testing.T) {
	// Payload menor que compressMinSize — não deve ser comprimido
	data := []byte("small")
	compressed := Compress(data)

	if compressed[0] != flagUncompressed {
		t.Fatal("expected uncompressed flag for small payload")
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}
	if !bytes.Equal(data, decompressed) {
		t.Fatalf("small payload roundtrip failed")
	}
}

func TestCompressDecompress_HTMLPayload(t *testing.T) {
	// Simula um payload HTML típico com muita repetição
	var buf bytes.Buffer
	buf.WriteString("<!DOCTYPE html><html><head><title>Test Page</title></head><body>")
	for i := 0; i < 100; i++ {
		buf.WriteString("<div class=\"container\"><div class=\"row\"><div class=\"col-md-6\">")
		buf.WriteString("<h3 class=\"title\">Section Title</h3>")
		buf.WriteString("<p class=\"description\">Lorem ipsum dolor sit amet, consectetur adipiscing elit.</p>")
		buf.WriteString("</div></div></div>\n")
	}
	buf.WriteString("</body></html>")
	data := buf.Bytes()

	compressed := Compress(data)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("HTML roundtrip failed: expected %d bytes, got %d bytes", len(data), len(decompressed))
	}

	// HTML repetitivo deve comprimir bem
	ratio := float64(len(compressed)) / float64(len(data)) * 100
	t.Logf("HTML payload: %d → %d bytes (%.1f%% — compressão de %.1f%%)",
		len(data), len(compressed), ratio, 100-ratio)

	if compressed[0] == flagCompressed && ratio > 80 {
		t.Logf("AVISO: ratio de compressão baixa (%.1f%%), mas dados podem ser atípicos", ratio)
	}
}

func TestCompressDecompress_JSONPayload(t *testing.T) {
	// Simula um payload JSON de API REST
	var buf bytes.Buffer
	buf.WriteString("[")
	for i := 0; i < 50; i++ {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(`{"id":`)
		buf.WriteString(strings.Repeat("1", 5))
		buf.WriteString(`,"name":"User Name","email":"user@example.com","role":"admin","active":true,"created_at":"2024-01-01T00:00:00Z"}`)
	}
	buf.WriteString("]")
	data := buf.Bytes()

	compressed := Compress(data)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("JSON roundtrip failed")
	}

	ratio := float64(len(compressed)) / float64(len(data)) * 100
	t.Logf("JSON payload: %d → %d bytes (%.1f%%)", len(data), len(compressed), ratio)
}

func TestCompressDecompress_RandomData(t *testing.T) {
	// Dados aleatórios — quase impossíveis de comprimir
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, 2000)
	for i := range data {
		data[i] = byte(rng.Intn(256))
	}

	compressed := Compress(data)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("random data roundtrip failed")
	}

	// Para dados aleatórios, o flag deve ser uncompressed (compressão não ajuda)
	if compressed[0] == flagCompressed {
		ratio := float64(len(compressed)) / float64(len(data)) * 100
		t.Logf("Random data was compressed (unusual): %d → %d bytes (%.1f%%)",
			len(data), len(compressed), ratio)
	} else {
		t.Logf("Random data stored uncompressed (expected): %d → %d bytes",
			len(data), len(compressed))
	}
}

func TestCompressDecompress_AllSameBytes(t *testing.T) {
	// Todos os bytes iguais — compressão máxima
	data := bytes.Repeat([]byte{0x00}, 5000)

	compressed := Compress(data)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("all-same roundtrip failed")
	}

	ratio := float64(len(compressed)) / float64(len(data)) * 100
	t.Logf("All-same-bytes: %d → %d bytes (%.1f%% — compressão extrema)",
		len(data), len(compressed), ratio)

	// Deve comprimir enormemente
	if ratio > 5 {
		t.Fatalf("expected extreme compression for uniform data, got %.1f%%", ratio)
	}
}

// ---------------------------------------------------------------------------
// Testes ShouldCompress
// ---------------------------------------------------------------------------

func TestShouldCompress(t *testing.T) {
	tests := []struct {
		contentType string
		size        int
		expected    bool
		desc        string
	}{
		{"text/html", 1000, true, "HTML deve comprimir"},
		{"text/css", 1000, true, "CSS deve comprimir"},
		{"text/plain", 1000, true, "Plain text deve comprimir"},
		{"application/json", 1000, true, "JSON deve comprimir"},
		{"application/xml", 1000, true, "XML deve comprimir"},
		{"application/javascript", 1000, true, "JS deve comprimir"},
		{"application/svg+xml", 1000, true, "SVG deve comprimir"},
		{"application/ld+json", 1000, true, "JSON-LD deve comprimir"},
		{"image/png", 1000, false, "PNG já é comprimido"},
		{"image/jpeg", 1000, false, "JPEG já é comprimido"},
		{"video/mp4", 1000, false, "Vídeo já é comprimido"},
		{"application/octet-stream", 1000, false, "Binário genérico não deve comprimir"},
		{"application/zip", 1000, false, "ZIP já é comprimido"},
		{"text/html", 100, false, "Payload pequeno não deve comprimir"},
		{"text/html", 511, false, "Abaixo do threshold (512)"},
		{"text/html", 512, true, "Exactamente no threshold"},
		{"", 1000, false, "Content-type vazio não deve comprimir"},
	}

	for _, tt := range tests {
		result := ShouldCompress(tt.contentType, tt.size)
		if result != tt.expected {
			t.Errorf("[%s] ShouldCompress(%q, %d) = %v, expected %v",
				tt.desc, tt.contentType, tt.size, result, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Testes de Decompress com dados inválidos
// ---------------------------------------------------------------------------

func TestDecompress_EmptyInput(t *testing.T) {
	_, err := Decompress(nil)
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for nil input, got %v", err)
	}

	_, err = Decompress([]byte{})
	if err != ErrCorruptedData {
		t.Fatalf("expected ErrCorruptedData for empty input, got %v", err)
	}
}

func TestDecompress_InvalidFlag(t *testing.T) {
	_, err := Decompress([]byte{0xFF, 0x01, 0x02})
	if err != ErrInvalidHeader {
		t.Fatalf("expected ErrInvalidHeader, got %v", err)
	}
}

func TestDecompress_CorruptedCompressed(t *testing.T) {
	// Flag de comprimido mas dados truncados/inválidos
	_, err := Decompress([]byte{flagCompressed, 0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for corrupted compressed data")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkCompress_HTML(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		buf.WriteString("<div class=\"container\"><p>Content text here for benchmarking purposes</p></div>\n")
	}
	data := buf.Bytes()

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		Compress(data)
	}
}

func BenchmarkDecompress_HTML(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		buf.WriteString("<div class=\"container\"><p>Content text here for benchmarking purposes</p></div>\n")
	}
	data := buf.Bytes()
	compressed := Compress(data)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = Decompress(compressed)
	}
}

func BenchmarkCompress_JSON(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString("[")
	for i := 0; i < 50; i++ {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(`{"id":12345,"name":"User","email":"user@example.com","active":true}`)
	}
	buf.WriteString("]")
	data := buf.Bytes()

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		Compress(data)
	}
}

func BenchmarkLZ77Encode(b *testing.B) {
	data := bytes.Repeat([]byte("ABCDEFGHIJ"), 500)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lz77Encode(data)
	}
}

func BenchmarkHuffmanEncode(b *testing.B) {
	data := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		huffmanEncode(data)
	}
}
