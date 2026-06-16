package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateValidConfig(t *testing.T) {
	cfg := &Config{
		ListenPort: 8443,
		Mode:       "intercept",
	}
	cfg.Runtime.Failsafe = "bypass"
	cfg.Runtime.TimeoutMS = 50
	cfg.Runtime.PoolSize = 64
	cfg.TLS.CADir = "./certs"
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := &Config{
		ListenPort: 0,
		Mode:       "intercept",
	}
	cfg.Runtime.Failsafe = "bypass"
	cfg.Runtime.TimeoutMS = 50
	cfg.Runtime.PoolSize = 64
	cfg.TLS.CADir = "./certs"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid port, got nil")
	}
}

func TestValidateInvalidMode(t *testing.T) {
	cfg := &Config{
		ListenPort: 8443,
		Mode:       "invalid",
	}
	cfg.Runtime.Failsafe = "bypass"
	cfg.Runtime.TimeoutMS = 50
	cfg.Runtime.PoolSize = 64
	cfg.TLS.CADir = "./certs"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid mode, got nil")
	}
}

func TestValidateInvalidFailsafe(t *testing.T) {
	cfg := &Config{
		ListenPort: 8443,
		Mode:       "intercept",
	}
	cfg.Runtime.Failsafe = "invalid"
	cfg.Runtime.TimeoutMS = 50
	cfg.Runtime.PoolSize = 64
	cfg.TLS.CADir = "./certs"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid failsafe, got nil")
	}
}

func TestCreateDefaultConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := CreateDefaultConfigFile(dir); err != nil {
		t.Fatalf("CreateDefaultConfigFile failed: %v", err)
	}
	path := filepath.Join(dir, "sonic.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected config file to exist")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig with no file should return defaults, got: %v", err)
	}
	if cfg.ListenPort != 8443 {
		t.Errorf("expected default port 8443, got %d", cfg.ListenPort)
	}
	if cfg.Mode != "intercept" {
		t.Errorf("expected default mode 'intercept', got %s", cfg.Mode)
	}
	if cfg.Runtime.Failsafe != "bypass" {
		t.Errorf("expected default failsafe 'bypass', got %s", cfg.Runtime.Failsafe)
	}
}
