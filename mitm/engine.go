package mitm

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/manassesbinga/sonic/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/proxy"
)

// MITMEngine handles TLS Man-in-the-Middle operations.
// It terminates TLS from the client (using dynamic certificates) and
// re-encrypts to the real server with strict certificate verification.
type MITMEngine struct {
	ca    *CA
	cache *CertCache
}

// CA retorna a Autoridade Certificadora raiz usada pelo motor MITM.
func (m *MITMEngine) CA() *CA {
	return m.ca
}

// NewMITMEngine creates a new MITM engine, loading or creating the Root CA.
// The CA directory must contain or will receive ca.pem and ca-key.pem.
func NewMITMEngine(caDir string) (*MITMEngine, error) {
	ca, err := LoadOrCreateCA(caDir)
	if err != nil {
		return nil, err
	}

	cache, err := NewCertCache(ca)
	if err != nil {
		return nil, err
	}

	return &MITMEngine{
		ca:    ca,
		cache: cache,
	}, nil
}

// InterceptTermTLS performs TLS termination with the client.
// It generates a dynamic certificate for the SNI domain and completes
// the TLS handshake as the server. Returns the decrypted connection.
//
// The clientConn should be a raw TCP connection from the client.
// domain is the SNI hostname extracted from the ClientHello.
func (m *MITMEngine) InterceptTermTLS(ctx context.Context, clientConn net.Conn, domain string) (*tls.Conn, error) {
	if t := telemetry.GetTelemetry(); t != nil {
		var span trace.Span
		ctx, span = t.StartSpan(ctx, "mitm.intercept_term_tls",
			trace.WithAttributes(attribute.String("sonic.domain", domain)),
		)
		defer span.End()
	}

	tlsCert, err := m.cache.GetCertificate(domain)
	if err != nil {
		return nil, fmt.Errorf("falha ao obter certificado dinamico: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}

	tlsConn := tls.Server(clientConn, tlsConfig)
	return tlsConn, nil
}

// ConnectRealServer establishes a re-encrypted TLS connection to the real
// upstream server. Uses strict certificate verification (InsecureSkipVerify: false).
//
// addr is the server address (e.g. "example.com:443").
// domain is the SNI hostname for TLS verification.
func (m *MITMEngine) ConnectRealServer(ctx context.Context, addr string, domain string) (*tls.Conn, error) {
	if t := telemetry.GetTelemetry(); t != nil {
		var span trace.Span
		ctx, span = t.StartSpan(ctx, "mitm.connect_upstream",
			trace.WithAttributes(
				attribute.String("sonic.domain", domain),
				attribute.String("sonic.upstream_addr", addr),
			),
		)
		defer span.End()
	}

	tlsConfig := &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: false,
		RootCAs:            nil, // uses system pool by default
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	dialer := proxy.FromEnvironment()
	var conn net.Conn
	var err error
	if cDialer, ok := dialer.(interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}); ok {
		conn, err = cDialer.DialContext(dialCtx, "tcp", addr)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao servidor de destino %s: %w", addr, err)
	}

	// Define um limite de tempo para o handshake TLS para evitar travar caso o upstream congele
	err = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("falha ao definir deadline para handshake TLS: %w", err)
	}

	tlsConn := tls.Client(conn, tlsConfig)
	err = tlsConn.Handshake()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("falha no handshake TLS com o servidor de destino %s: %w", addr, err)
	}

	// Limpa o deadline após o handshake para permitir operações normais de leitura/escrita
	_ = conn.SetDeadline(time.Time{})
	return tlsConn, nil
}
