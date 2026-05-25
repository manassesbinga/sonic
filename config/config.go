package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	ListenPort    int      `mapstructure:"listen_port"`
	TargetPorts   []int    `mapstructure:"target_ports"`
	Mode          string   `mapstructure:"mode"` // "intercept" | "passthrough-all" | "observe"
	BypassDomains []string `mapstructure:"bypass_domains"`

	TLS struct {
		CADir        string `mapstructure:"ca_dir"`
		AutoGenerate bool   `mapstructure:"auto_generate"`
	} `mapstructure:"tls"`

	Runtime struct {
		TimeoutMS int    `mapstructure:"timeout_ms"`
		PoolSize  int    `mapstructure:"pool_size"`
		Failsafe  string `mapstructure:"failsafe"` // "bypass" | "block"
	} `mapstructure:"runtime"`

	Logging struct {
		Level  string `mapstructure:"level"`  // "debug" | "info" | "warn" | "error"
		Format string `mapstructure:"format"` // "json" | "text"
	} `mapstructure:"logging"`
}

func LoadConfig(configPath string) (*Config, error) {
	// 1. Inicializa a struct com os valores padrao nativos do sistema em Go
	cfg := Config{}
	cfg.ListenPort = 8443
	cfg.TargetPorts = []int{80, 443, 8080}
	cfg.Mode = "intercept"
	cfg.TLS.CADir = "./certs"
	cfg.TLS.AutoGenerate = true
	cfg.Runtime.TimeoutMS = 50
	cfg.Runtime.PoolSize = 64
	cfg.Runtime.Failsafe = "bypass"
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "text"

	// Garante que o viper não misture estados entre testes unitários concorrentes
	viper.Reset()

	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName("channelworkers")
		viper.SetConfigType("yaml")
	}

	// Tenta ler o arquivo de configuração
	if err := viper.ReadInConfig(); err != nil {
		// Tolerante a arquivos nao encontrados tanto por busca implicita quanto por caminho explicito
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && !os.IsNotExist(err) {
			return nil, fmt.Errorf("erro ao ler arquivo de configuracao: %w", err)
		}
	}

	// O Unmarshal preenche apenas o que foi lido do arquivo, mantendo os defaults nas propriedades ausentes
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("erro ao fazer parse da configuracao: %w", err)
	}

	// Validações básicas
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.ListenPort <= 0 || c.ListenPort > 65535 {
		return fmt.Errorf("porta de escuta listen_port invalida: %d", c.ListenPort)
	}
	if c.Mode != "intercept" && c.Mode != "passthrough-all" && c.Mode != "observe" {
		return fmt.Errorf("modo de operacao mode invalido: %s", c.Mode)
	}
	if c.Runtime.Failsafe != "bypass" && c.Runtime.Failsafe != "block" {
		return fmt.Errorf("failsafe runtime invalido: %s", c.Runtime.Failsafe)
	}
	return nil
}

func CreateDefaultConfigFile(dir string) error {
	content := `# channelworkers.yaml
listen_port: 8443
target_ports:
  - 80
  - 443
  - 8080
mode: "intercept"

tls:
  ca_dir: "./certs"
  auto_generate: true

runtime:
  timeout_ms: 50
  pool_size: 64
  failsafe: "bypass"

bypass_domains:
  - "*.stripe.com"
  - "*.amazonaws.com"
  - "*.apple.com"

logging:
  level: "info"
  format: "text"
`
	filePath := filepath.Join(dir, "channelworkers.yaml")
	return os.WriteFile(filePath, []byte(content), 0644)
}
