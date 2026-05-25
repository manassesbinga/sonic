// Package mitm provides TLS Man-in-the-Middle capabilities for Sonic.
//
// It generates a local Root Certificate Authority and dynamically creates
// TLS certificates for intercepted domains on-the-fly.
//
// Usage:
//
//	ca, err := mitm.LoadOrCreateCA("./certs")
//	cache, err := mitm.NewCertCache(ca)
//	tlsCert, err := cache.GetCertificate("example.com")
package mitm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// CA represents a local Root Certificate Authority used for signing
// dynamic TLS certificates for MITM interception.
type CA struct {
	Cert    *x509.Certificate
	PrivKey *rsa.PrivateKey
}

// LoadOrCreateCA loads an existing CA from disk, or generates a new
// self-signed Root CA if none exists. Certificates are stored as
// ca.pem and ca-key.pem in the specified directory.
//
// The private key file is created with 0600 permissions.
func LoadOrCreateCA(caDir string) (*CA, error) {
	certPath := filepath.Join(caDir, "ca.pem")
	keyPath := filepath.Join(caDir, "ca-key.pem")

	if err := os.MkdirAll(caDir, 0755); err != nil {
		return nil, fmt.Errorf("erro ao criar diretorio da CA: %w", err)
	}

	if _, errCert := os.Stat(certPath); errCert == nil {
		if _, errKey := os.Stat(keyPath); errKey == nil {
			return loadCA(certPath, keyPath)
		}
	}

	return generateAndSaveCA(caDir, certPath, keyPath)
}

func loadCA(certPath, keyPath string) (*CA, error) {
	// ... loading logic
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler certificado CA: %w", err)
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler chave CA: %w", err)
	}

	certBlock, _ := pem.Decode(certBytes)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("bloco PEM de certificado CA invalido")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("erro ao decodificar certificado CA: %w", err)
	}

	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil || (keyBlock.Type != "RSA PRIVATE KEY" && keyBlock.Type != "PRIVATE KEY") {
		return nil, fmt.Errorf("bloco PEM de chave privada CA invalido")
	}

	var privKey *rsa.PrivateKey
	if keyBlock.Type == "RSA PRIVATE KEY" {
		privKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	} else {
		parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err == nil {
			var ok bool
			privKey, ok = parsedKey.(*rsa.PrivateKey)
			if !ok {
				err = fmt.Errorf("chave privada nao e RSA")
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("erro ao decodificar chave privada CA: %w", err)
	}

	return &CA{
		Cert:    cert,
		PrivKey: privKey,
	}, nil
}

func generateAndSaveCA(caDir, certPath, keyPath string) (*CA, error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("erro ao gerar chave RSA da CA: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("erro ao gerar serial number para CA: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"ChannelWorkers CA"},
			Country:      []string{"PT"},
			Province:     []string{"Borda"},
			Locality:     []string{"LocalNode"},
			CommonName:   "ChannelWorkers Root CA",
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, fmt.Errorf("erro ao assinar certificado CA: %w", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar ficheiro ca.pem: %w", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes}); err != nil {
		return nil, fmt.Errorf("erro ao codificar ca.pem: %w", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar ficheiro ca-key.pem: %w", err)
	}
	defer keyOut.Close()

	keyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	}
	if err := pem.Encode(keyOut, keyBlock); err != nil {
		return nil, fmt.Errorf("erro ao codificar ca-key.pem: %w", err)
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, fmt.Errorf("erro ao parsear certificado CA gerado: %w", err)
	}

	return &CA{
		Cert:    cert,
		PrivKey: privKey,
	}, nil
}
