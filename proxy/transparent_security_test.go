package proxy

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/security"
)

func TestTransparentProxy_SecurityScannerInitialization(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.Enabled = true
	cfg.Security.Block = true

	// mitmEngine can be nil for struct checks
	proxy := NewTransparentProxy(cfg, nil, nil)

	if proxy.cveScanner == nil {
		t.Error("expected cveScanner to be initialized when security.enabled is true, but got nil")
	}

	if !proxy.cveScanner.Block {
		t.Error("expected cveScanner.Block to be true, but got false")
	}

	// Test with security disabled
	cfgDisabled := &config.Config{}
	cfgDisabled.Security.Enabled = false
	proxyDisabled := NewTransparentProxy(cfgDisabled, nil, nil)
	if proxyDisabled.cveScanner != nil {
		t.Error("expected cveScanner to be nil when security.enabled is false, but got non-nil")
	}
}

func TestTransparentProxy_ScanningRequestFlow(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.Enabled = true
	cfg.Security.Block = true

	proxy := NewTransparentProxy(cfg, nil, nil)
	if proxy.cveScanner == nil {
		t.Fatal("cveScanner not initialized")
	}

	// Legitimate body flow
	legitimateBody := `{"message": "Hello World"}`
	reqReader := security.NewScanningReader(strings.NewReader(legitimateBody), proxy.cveScanner)

	_, err := io.ReadAll(reqReader)
	if err != nil {
		t.Errorf("expected legitimate request to pass, but got error: %v", err)
	}

	// Malicious body flow (blocking mode)
	maliciousBody := `const exploit = req.body.constructor.prototype.polluted = true;`
	maliciousReader := security.NewScanningReader(strings.NewReader(maliciousBody), proxy.cveScanner)

	_, err = io.ReadAll(maliciousReader)
	if !errors.Is(err, security.ErrCVEDetected) {
		t.Errorf("expected ErrCVEDetected on malicious request, but got: %v", err)
	}

	// Malicious body flow (non-blocking/warn mode)
	cfg.Security.Block = false
	proxyNonBlocking := NewTransparentProxy(cfg, nil, nil)
	if proxyNonBlocking.cveScanner == nil {
		t.Fatal("cveScanner not initialized")
	}

	maliciousReaderNonBlock := security.NewScanningReader(strings.NewReader(maliciousBody), proxyNonBlocking.cveScanner)
	out, err := io.ReadAll(maliciousReaderNonBlock)
	if err != nil {
		t.Errorf("expected malicious request to pass in non-blocking mode, but got error: %v", err)
	}
	if string(out) != maliciousBody {
		t.Errorf("expected payload to match input in non-blocking mode, got %q", string(out))
	}
}
