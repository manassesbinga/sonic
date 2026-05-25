package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefault(t *testing.T) {
	// Carrega configuração inexistente para testar valores default
	cfg, err := LoadConfig("non-existent-config.yaml")
	if err != nil {
		t.Fatalf("Nao deveria dar erro ao carregar config inexistente (espera usar defaults): %v", err)
	}

	if cfg.ListenPort != 8443 {
		t.Errorf("ListenPort default deveria ser 8443, obteve: %d", cfg.ListenPort)
	}
	if cfg.Mode != "intercept" {
		t.Errorf("Mode default deveria ser 'intercept', obteve: %s", cfg.Mode)
	}
	if cfg.TLS.CADir != "./certs" {
		t.Errorf("CADir default deveria ser './certs', obteve: %s", cfg.TLS.CADir)
	}
	if cfg.Runtime.TimeoutMS != 50 {
		t.Errorf("TimeoutMS default deveria ser 50, obteve: %d", cfg.Runtime.TimeoutMS)
	}
	if cfg.Runtime.PoolSize != 64 {
		t.Errorf("PoolSize default deveria ser 64, obteve: %d", cfg.Runtime.PoolSize)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "Porta Valida",
			cfg: Config{
				ListenPort: 8080,
				Mode:       "intercept",
				Runtime: struct {
					TimeoutMS int    `mapstructure:"timeout_ms"`
					PoolSize  int    `mapstructure:"pool_size"`
					Failsafe  string `mapstructure:"failsafe"`
				}{Failsafe: "bypass"},
			},
			wantErr: false,
		},
		{
			name: "Porta Invalida Negativa",
			cfg: Config{
				ListenPort: -1,
				Mode:       "intercept",
				Runtime: struct {
					TimeoutMS int    `mapstructure:"timeout_ms"`
					PoolSize  int    `mapstructure:"pool_size"`
					Failsafe  string `mapstructure:"failsafe"`
				}{Failsafe: "bypass"},
			},
			wantErr: true,
		},
		{
			name: "Porta Invalida Superior",
			cfg: Config{
				ListenPort: 70000,
				Mode:       "intercept",
				Runtime: struct {
					TimeoutMS int    `mapstructure:"timeout_ms"`
					PoolSize  int    `mapstructure:"pool_size"`
					Failsafe  string `mapstructure:"failsafe"`
				}{Failsafe: "bypass"},
			},
			wantErr: true,
		},
		{
			name: "Modo Invalido",
			cfg: Config{
				ListenPort: 8443,
				Mode:       "invalid-mode",
				Runtime: struct {
					TimeoutMS int    `mapstructure:"timeout_ms"`
					PoolSize  int    `mapstructure:"pool_size"`
					Failsafe  string `mapstructure:"failsafe"`
				}{Failsafe: "bypass"},
			},
			wantErr: true,
		},
		{
			name: "Failsafe Invalido",
			cfg: Config{
				ListenPort: 8443,
				Mode:       "intercept",
				Runtime: struct {
					TimeoutMS int    `mapstructure:"timeout_ms"`
					PoolSize  int    `mapstructure:"pool_size"`
					Failsafe  string `mapstructure:"failsafe"`
				}{Failsafe: "invalid-failsafe"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() erro = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateDefaultConfigFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cw-test-*")
	if err != nil {
		t.Fatalf("Erro ao criar diretorio temporario: %v", err)
	}
	defer os.RemoveAll(tempDir)

	err = CreateDefaultConfigFile(tempDir)
	if err != nil {
		t.Fatalf("Erro ao criar arquivo de configuracao padrao: %v", err)
	}

	expectedFile := filepath.Join(tempDir, "channelworkers.yaml")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Errorf("Arquivo de configuracao padrao nao foi gerado em %s", expectedFile)
	}
}
