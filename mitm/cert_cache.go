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

// CertCache generates and caches TLS certificates for MITM interception.
// Certificates are signed by the Root CA and generated on-the-fly per domain.
// Thread-safe with double-check locking for concurrent access.
type CertCache struct {
	ca      *CA
	leafKey *rsa.PrivateKey
	cache   map[string]*tls.Certificate
	mutex   sync.RWMutex
}

// NewCertCache creates a new certificate cache with a pre-generated leaf key.
// The leaf key is reused across all generated certificates for performance.
func NewCertCache(ca *CA) (*CertCache, error) {
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

// GetCertificate returns a TLS certificate for the given domain (SNI).
// It checks the cache first; if not found, generates a new certificate
// signed by the Root CA. Thread-safe with double-checked locking.
func (cc *CertCache) GetCertificate(domain string) (*tls.Certificate, error) {
	cc.mutex.RLock()
	cert, ok := cc.cache[domain]
	cc.mutex.RUnlock()

	if ok {
		return cert, nil
	}

	cc.mutex.Lock()
	defer cc.mutex.Unlock()

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
		NotAfter:              time.Now().AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	if ip := net.ParseIP(domain); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{domain}
	}

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
