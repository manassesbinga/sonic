// Package config provides configuration loading for Sonic.
//
// It supports YAML files, environment variables (SONIC_*), and defaults.
// Priority: env vars > YAML file > hardcoded defaults.
//
// Usage:
//
//	cfg, err := config.LoadConfig("sonic.yaml")
//	cfg.ListenPort = 9000  // override at runtime
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all Sonic configuration.
// Fields map to sonic.yaml keys and SONIC_* environment variables.
type Config struct {
	// ListenPort is the port the transparent proxy listens on (default: 8443).
	ListenPort int `mapstructure:"listen_port"`

	// TargetPorts lists ports to intercept (default: [80, 443, 8080]).
	TargetPorts []int `mapstructure:"target_ports"`

	// Mode sets the proxy mode: "intercept", "passthrough-all", or "observe".
	Mode string `mapstructure:"mode"`

	// BypassDomains lists domains passed through without interception.
	// Supports wildcard: "*.stripe.com".
	BypassDomains []string `mapstructure:"bypass_domains"`

	// TLS configures the MITM certificate authority.
	TLS struct {
		// CADir is the directory for CA certificates (default: "./certs").
		CADir string `mapstructure:"ca_dir"`
		// AutoGenerate creates the CA automatically if missing.
		AutoGenerate bool `mapstructure:"auto_generate"`
	} `mapstructure:"tls"`

	// Runtime configures the JavaScript VM pool.
	Runtime struct {
		// TimeoutMS is the max execution time per JS call (default: 50ms).
		TimeoutMS int `mapstructure:"timeout_ms"`
		// PoolSize is the number of pre-warmed VM instances (default: 64).
		PoolSize int `mapstructure:"pool_size"`
		// Failsafe sets behavior on JS error: "bypass" or "block" (default: "bypass").
		Failsafe string `mapstructure:"failsafe"`
	} `mapstructure:"runtime"`

	// Logging configures log output.
	Logging struct {
		// Level is "debug", "info", "warn", or "error" (default: "info").
		Level string `mapstructure:"level"`
		// Format is "json" or "text" (default: "text").
		Format string `mapstructure:"format"`
	} `mapstructure:"logging"`
}

// LoadConfig reads configuration from a YAML file and environment variables.
// If configPath is empty, it looks for ./sonic.yaml.
// Returns defaults if no file is found. Errors on invalid values.
//
// Env vars use the SONIC_ prefix: SONIC_LISTEN_PORT=9000 overrides listen_port.
func LoadConfig(configPath string) (*Config, error) {
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

	viper.Reset()

	viper.SetEnvPrefix("SONIC")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName("channelworkers")
		viper.SetConfigType("yaml")
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && !os.IsNotExist(err) {
			return nil, fmt.Errorf("erro ao ler arquivo de configuracao: %w", err)
		}
	}

	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("erro ao fazer parse da configuracao: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks config values and returns an error if any are invalid.
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

// CreateDefaultConfigFile writes a default sonic.yaml to dir.
func CreateDefaultConfigFile(dir string) error {
	content := `# sonic.yaml
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
	filePath := filepath.Join(dir, "sonic.yaml")
	return os.WriteFile(filePath, []byte(content), 0644)
}
