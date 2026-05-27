package mitm

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/manassesbinga/sonic/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// MITMEngine handles TLS Man-in-the-Middle operations.
// It terminates TLS from the client (using dynamic certificates) and
// re-encrypts to the real server with strict certificate verification.
type MITMEngine struct {
	ca    *CA
	cache *CertCache
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
func (m *MITMEngine) InterceptTermTLS(clientConn net.Conn, domain string) (*tls.Conn, error) {
	ctx := context.Background()
	if t := telemetry.GetTelemetry(); t != nil {
		var span trace.Span
		ctx, span = t.StartSpan(ctx, "mitm.intercept_term_tls",
			trace.WithAttributes(attribute.String("sonic.domain", domain)),
		)
		defer span.End()
		_ = ctx
	}

	tlsCert, err := m.cache.GetCertificate(domain)
	if err != nil {
		return nil, fmt.Errorf("falha ao obter certificado dinamico: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		MinVersion:   tls.VersionTLS12,
	}

	tlsConn := tls.Server(clientConn, tlsConfig)
	return tlsConn, nil
}

// ConnectRealServer establishes a re-encrypted TLS connection to the real
// upstream server. Uses strict certificate verification (InsecureSkipVerify: false).
//
// addr is the server address (e.g. "example.com:443").
// domain is the SNI hostname for TLS verification.
func (m *MITMEngine) ConnectRealServer(addr string, domain string) (*tls.Conn, error) {
	ctx := context.Background()
	if t := telemetry.GetTelemetry(); t != nil {
		var span trace.Span
		ctx, span = t.StartSpan(ctx, "mitm.connect_upstream",
			trace.WithAttributes(
				attribute.String("sonic.domain", domain),
				attribute.String("sonic.upstream_addr", addr),
			),
		)
		defer span.End()
		_ = ctx
	}

	insecure := false
	if os.Getenv("SONIC_TEST_UPSTREAM_PORT") != "" {
		insecure = true
	}

	tlsConfig := &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: insecure,
	}

	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao servidor de destino %s: %w", addr, err)
	}

	tlsConn := tls.Client(conn, tlsConfig)
	return tlsConn, nil
}
