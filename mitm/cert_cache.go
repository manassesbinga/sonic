package mitm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

type CertCache struct {
	ca      *CA
	leafKey *rsa.PrivateKey // Chave privada de folha unica gerada no warm-up
	cache   map[string]*tls.Certificate
	mutex   sync.RWMutex
}

func NewCertCache(ca *CA) (*CertCache, error) {
	// Gera uma unica chave de folha RSA de 2048 bits para reutilizacao concorrente
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("erro ao gerar chave de folha reutilizavel: %w", err)
	}

	return &CertCache{
		ca:      ca,
		leafKey: leafKey,
		cache:   make(map[string]*tls.Certificate),
	}, nil
}

// GetCertificate retorna o certificado TLS para o dominio (SNI) a partir da cache ou gera um novo on-the-fly.
func (cc *CertCache) GetCertificate(domain string) (*tls.Certificate, error) {
	cc.mutex.RLock()
	cert, ok := cc.cache[domain]
	cc.mutex.RUnlock()

	if ok {
		return cert, nil
	}

	// Se nao tiver na cache, gera um novo certificado assinado
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	// Double-check caso outra thread tenha gerado enquanto aguardava o lock
	if cert, ok = cc.cache[domain]; ok {
		return cert, nil
	}

	generatedCert, err := cc.generateCert(domain)
	if err != nil {
		return nil, err
	}

	cc.cache[domain] = generatedCert
	return generatedCert, nil
}

func (cc *CertCache) generateCert(domain string) (*tls.Certificate, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("erro ao gerar serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"ChannelWorkers Intercept Node"},
			CommonName:   domain,
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(2, 0, 0), // Valido por 2 anos
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Adiciona SAN (Subject Alternative Name) para IPs ou DNS
	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{domain}
	}

	// Assina o certificado usando a chave privada da CA e a chave de folha reutilizada do Host
	certBytes, err := x509.CreateCertificate(rand.Reader, template, cc.ca.Cert, &cc.leafKey.PublicKey, cc.ca.PrivKey)
	if err != nil {
		return nil, fmt.Errorf("erro ao assinar certificado para %s: %w", domain, err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certBytes, cc.ca.Cert.Raw},
		PrivateKey:  cc.leafKey,
	}

	return tlsCert, nil
}
