package proxy

import (
	"crypto/rand"
	"io"
	"net"
	"testing"

	"channelworkers/config"
)

func buildTLSClientHello(serverName string) []byte {
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

	sessionID := []byte{0x20}
	sid := make([]byte, 32)
	rand.Read(sid)
	sessionID = append(sessionID, sid...)

	random := make([]byte, 32)
	rand.Read(random)

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

func TestExtractSNI_Valid(t *testing.T) {
	data := buildTLSClientHello("example.com")
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI falhou: %v", err)
	}
	if sni != "example.com" {
		t.Errorf("SNI esperado 'example.com', obteve '%s'", sni)
	}
}

func TestExtractSNI_LongDomain(t *testing.T) {
	data := buildTLSClientHello("a-very-long-subdomain.example.com")
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI falhou: %v", err)
	}
	if sni != "a-very-long-subdomain.example.com" {
		t.Errorf("SNI inesperado: '%s'", sni)
	}
}

func TestExtractSNI_IPAddress(t *testing.T) {
	data := buildTLSClientHello("1.2.3.4")
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI falhou para IP: %v", err)
	}
	if sni != "1.2.3.4" {
		t.Errorf("SNI esperado '1.2.3.4', obteve '%s'", sni)
	}
}

func TestExtractSNI_NotTLS(t *testing.T) {
	_, err := ExtractSNI([]byte{0x00, 0x00, 0x00, 0x00, 0x00})
	if err == nil {
		t.Fatal("Esperava erro para dados nao-TLS (primeiro byte != 0x16)")
	}
}

func TestExtractSNI_NotClientHello(t *testing.T) {
	data := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x02, 0x00, 0x00, 0x00, 0x00}
	_, err := ExtractSNI(data)
	if err == nil {
		t.Fatal("Esperava erro para record que nao e ClientHello (tipo != 0x01)")
	}
}

func TestExtractSNI_TruncatedHeader(t *testing.T) {
	_, err := ExtractSNI([]byte{0x16, 0x03})
	if err == nil {
		t.Fatal("Esperava erro para dados com < 5 bytes")
	}
}

func TestExtractSNI_TruncatedRecord(t *testing.T) {
	data := []byte{0x16, 0x03, 0x01, 0x05, 0x00, 0x01}
	_, err := ExtractSNI(data)
	if err == nil {
		t.Fatal("Esperava erro para record incompleto (len declarado > dados)")
	}
}

func TestExtractSNI_NoExtensions(t *testing.T) {
	random := make([]byte, 32)
	rand.Read(random)
	sidLen := 0
	handshakeBody := []byte{0x03, 0x03}
	handshakeBody = append(handshakeBody, random...)
	handshakeBody = append(handshakeBody, byte(sidLen))
	handshakeBody = append(handshakeBody, 0x00, 0x02, 0x13, 0x01)
	handshakeBody = append(handshakeBody, 0x01, 0x00)
	hsLen := len(handshakeBody)
	handshake := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	handshake = append(handshake, handshakeBody...)
	recordLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(recordLen >> 8), byte(recordLen)}
	record = append(record, handshake...)

	_, err := ExtractSNI(record)
	if err == nil {
		t.Fatal("Esperava erro sem extensoes TLS")
	}
}

func TestExtractSNI_NoSNI(t *testing.T) {
	supportedGroups := []byte{
		0x00, 0x0a,
		0x00, 0x04,
		0x00, 0x1d, 0x00, 0x17, 0x00, 0x18,
	}
	extLen := len(supportedGroups)
	extBlock := []byte{byte(extLen >> 8), byte(extLen)}
	extBlock = append(extBlock, supportedGroups...)

	random := make([]byte, 32)
	rand.Read(random)
	handshakeBody := []byte{0x03, 0x03}
	handshakeBody = append(handshakeBody, random...)
	handshakeBody = append(handshakeBody, 0x00)
	handshakeBody = append(handshakeBody, 0x00, 0x02, 0x13, 0x01)
	handshakeBody = append(handshakeBody, 0x01, 0x00)
	handshakeBody = append(handshakeBody, extBlock...)

	hsLen := len(handshakeBody)
	handshake := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	handshake = append(handshake, handshakeBody...)
	recordLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(recordLen >> 8), byte(recordLen)}
	record = append(record, handshake...)

	_, err := ExtractSNI(record)
	if err == nil {
		t.Fatal("Esperava erro quando extensao SNI nao esta presente")
	}
}

func TestExtractSNI_BufferOverflowSessionID(t *testing.T) {
	data := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x00, 0x00}
	data[7] = 0xFF
	_, err := ExtractSNI(data)
	if err == nil {
		t.Fatal("Esperava erro para SessionIDLen malformado")
	}
}

func TestExtractSNI_BufferOverflowCipherSuites(t *testing.T) {
	random := make([]byte, 32)
	rand.Read(random)
	sessionID := byte(0)
	partial := []byte{0x03, 0x03}
	partial = append(partial, random...)
	partial = append(partial, sessionID)
	badLen := []byte{0xFF, 0xFF}
	partial = append(partial, badLen...)
	hsLen := len(partial)
	handshake := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	handshake = append(handshake, partial...)
	recordLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(recordLen >> 8), byte(recordLen)}
	record = append(record, handshake...)

	_, err := ExtractSNI(record)
	if err == nil {
		t.Fatal("Esperava erro para CipherSuitesLen exagerado")
	}
}

func TestExtractSNI_MalformedSNIExtLength(t *testing.T) {
	sniData := []byte{0x00, 0x00, 0x00, 0x01, 0x00}
	sniExt := []byte{0x00, 0x00, byte(len(sniData) >> 8), byte(len(sniData))}
	sniExt = append(sniExt, sniData...)
	extLen := len(sniExt)
	extBlock := []byte{byte(extLen >> 8), byte(extLen)}
	extBlock = append(extBlock, sniExt...)

	random := make([]byte, 32)
	rand.Read(random)
	handshakeBody := []byte{0x03, 0x03}
	handshakeBody = append(handshakeBody, random...)
	handshakeBody = append(handshakeBody, 0x00)
	handshakeBody = append(handshakeBody, 0x00, 0x02, 0x13, 0x01)
	handshakeBody = append(handshakeBody, 0x01, 0x00)
	handshakeBody = append(handshakeBody, extBlock...)

	hsLen := len(handshakeBody)
	handshake := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	handshake = append(handshake, handshakeBody...)
	recordLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(recordLen >> 8), byte(recordLen)}
	record = append(record, handshake...)

	_, err := ExtractSNI(record)
	if err == nil {
		t.Fatal("Esperava erro para SNI extension malformada")
	}
}

func TestExtractSNI_LargeSNIData(t *testing.T) {
	hugeSNI := make([]byte, 5000)
	for i := range hugeSNI {
		hugeSNI[i] = 'x'
	}
	data := buildTLSClientHello(string(hugeSNI))
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI falhou para SNI grande: %v", err)
	}
	if len(sni) != 5000 {
		t.Errorf("SNI length esperado 5000, obteve %d", len(sni))
	}
}

func TestExtractSNI_DomainWithLeadingDot(t *testing.T) {
	data := buildTLSClientHello(".example.com")
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI falhou: %v", err)
	}
	if sni != ".example.com" {
		t.Errorf("SNI esperado '.example.com', obteve '%s'", sni)
	}
}

func TestExtractSNI_ServerNameNotFirst(t *testing.T) {
	nameTypeOther := []byte{0x01, 0x00, 0x01, 0x78, 0x00, 0x00, 0x05, 0x00, 0x00}
	sniListLen := len(nameTypeOther)
	sniList := []byte{byte(sniListLen >> 8), byte(sniListLen)}
	sniList = append(sniList, nameTypeOther...)
	sniExtLen := len(sniList)
	sniExt := []byte{0x00, 0x00, byte(sniExtLen >> 8), byte(sniExtLen)}
	sniExt = append(sniExt, sniList...)
	extLen := len(sniExt)
	extBlock := []byte{byte(extLen >> 8), byte(extLen)}
	extBlock = append(extBlock, sniExt...)

	random := make([]byte, 32)
	rand.Read(random)
	handshakeBody := []byte{0x03, 0x03}
	handshakeBody = append(handshakeBody, random...)
	handshakeBody = append(handshakeBody, 0x00)
	handshakeBody = append(handshakeBody, 0x00, 0x02, 0x13, 0x01)
	handshakeBody = append(handshakeBody, 0x01, 0x00)
	handshakeBody = append(handshakeBody, extBlock...)

	hsLen := len(handshakeBody)
	handshake := []byte{0x01, byte(hsLen >> 16), byte(hsLen >> 8), byte(hsLen)}
	handshake = append(handshake, handshakeBody...)
	recordLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(recordLen >> 8), byte(recordLen)}
	record = append(record, handshake...)

	_, err := ExtractSNI(record)
	if err == nil {
		t.Fatal("Esperava erro quando o primeiro nome na lista SNI nao e host_name (type 0)")
	}
}

func TestExtractSNI_TruncatedMidExtension(t *testing.T) {
	valid := buildTLSClientHello("example.com")
	truncated := valid[:len(valid)-10]
	_, err := ExtractSNI(truncated)
	if err == nil {
		t.Fatal("Esperava erro para dados truncados no meio das extensoes")
	}
}

func TestExtractSNI_EmptyRecord(t *testing.T) {
	_, err := ExtractSNI([]byte{})
	if err == nil {
		t.Fatal("Esperava erro para record vazio")
	}
}

func TestExtractSNI_MinimalValid(t *testing.T) {
	data := buildTLSClientHello("a")
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI falhou para dominio minimo: %v", err)
	}
	if sni != "a" {
		t.Errorf("SNI esperado 'a', obteve '%s'", sni)
	}
}

func TestShouldBypass_ExactMatch(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode: "intercept",
			BypassDomains: []string{
				"*.stripe.com",
				"api.example.com",
			},
		},
	}
	if !p.shouldBypass("api.example.com") {
		t.Error("shouldBypass deveria retornar true para bypass exato")
	}
}

func TestShouldBypass_WildcardMatch(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode: "intercept",
			BypassDomains: []string{
				"*.stripe.com",
			},
		},
	}
	if !p.shouldBypass("api.stripe.com") {
		t.Error("shouldBypass deveria retornar true para wildcard *.stripe.com")
	}
	if !p.shouldBypass("checkout.stripe.com") {
		t.Error("shouldBypass deveria retornar true para wildcard *.stripe.com")
	}
}

func TestShouldBypass_WildcardNoMatch(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode: "intercept",
			BypassDomains: []string{
				"*.stripe.com",
			},
		},
	}
	if p.shouldBypass("stripe.com") {
		t.Error("shouldBypass nao deveria casar 'stripe.com' com '*.stripe.com'")
	}
	if p.shouldBypass("notstripe.com") {
		t.Error("shouldBypass nao deveria casar dominios nao relacionados")
	}
}

func TestShouldBypass_PassthroughAll(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode:          "passthrough-all",
			BypassDomains: []string{},
		},
	}
	if !p.shouldBypass("qualquer.coisa.com") {
		t.Error("shouldBypass deveria retornar true para PASSTHROUGH-ALL mode")
	}
}

func TestShouldBypass_NoMatch(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode: "intercept",
			BypassDomains: []string{
				"*.stripe.com",
			},
		},
	}
	if p.shouldBypass("example.com") {
		t.Error("shouldBypass nao deveria casar example.com")
	}
}

func TestShouldBypass_EmptyList(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode:          "intercept",
			BypassDomains: []string{},
		},
	}
	if p.shouldBypass("example.com") {
		t.Error("shouldBypass deveria retornar false com lista vazia")
	}
}

func TestShouldBypass_MultipleWildcards(t *testing.T) {
	p := &TransparentProxy{
		cfg: &config.Config{
			Mode: "intercept",
			BypassDomains: []string{
				"*.apple.com",
				"*.amazonaws.com",
				"*.stripe.com",
			},
		},
	}
	if !p.shouldBypass("api.stripe.com") {
		t.Error("Falhou para api.stripe.com com multiplos wildcards")
	}
	if !p.shouldBypass("s3.amazonaws.com") {
		t.Error("Falhou para s3.amazonaws.com com multiplos wildcards")
	}
	if !p.shouldBypass("www.apple.com") {
		t.Error("Falhou para www.apple.com com multiplos wildcards")
	}
	if p.shouldBypass("www.google.com") {
		t.Error("Nao deveria casar www.google.com")
	}
}

func TestBufferedConn_PeekAndRead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write([]byte("GET / HTTP/1.1\r\nHost: test\r\n\r\n"))
		client.Close()
	}()

	bc := NewBufferedConn(server)
	peeked, err := bc.Peek(10)
	if err != nil {
		t.Fatalf("Peek falhou: %v", err)
	}
	if string(peeked) != "GET / HTTP" {
		t.Errorf("Peek esperava 'GET / HTTP', obteve '%s'", string(peeked))
	}

	readBuf := make([]byte, 100)
	n, err := bc.Read(readBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read falhou: %v", err)
	}
	if n < 10 {
		t.Fatalf("Read leu poucos bytes: %d", n)
	}
	if string(readBuf[:n]) != "GET / HTTP/1.1\r\nHost: test\r\n\r\n" {
		t.Errorf("Read nao retornou dados completos (incluindo peek)")
	}
}

func TestBufferedConn_PeekShort(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write([]byte("ab"))
		client.Close()
	}()

	bc := NewBufferedConn(server)
	_, err := bc.Peek(5)
	if err == nil {
		t.Fatal("Esperava erro ao fazer peek de mais bytes que o disponivel")
	}
}
