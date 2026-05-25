package mitm

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

type MITMEngine struct {
	ca    *CA
	cache *CertCache
}

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

// InterceptTermTLS realiza a terminacao TLS MITM concorrente com o cliente.
func (m *MITMEngine) InterceptTermTLS(clientConn net.Conn, domain string) (*tls.Conn, error) {
	// Obtem o certificado correspondente para o dominio da conexao
	tlsCert, err := m.cache.GetCertificate(domain)
	if err != nil {
		return nil, fmt.Errorf("falha ao obter certificado dinamico: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		MinVersion:   tls.VersionTLS12,
	}

	// Termina o TLS do cliente e retorna a conexao limpa
	tlsConn := tls.Server(clientConn, tlsConfig)
	return tlsConn, nil
}

// ConnectRealServer estabelece a conexao TLS encriptada (re-encriptacao) com o servidor de destino real.
func (m *MITMEngine) ConnectRealServer(addr string, domain string) (*tls.Conn, error) {
	tlsConfig := &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: false, // Mantido estrito para seguranca de producao
	}

	// Estabelece conexao TCP L4
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao servidor de destino %s: %w", addr, err)
	}

	// Conecta o TLS L7 re-encriptando
	tlsConn := tls.Client(conn, tlsConfig)
	return tlsConn, nil
}
