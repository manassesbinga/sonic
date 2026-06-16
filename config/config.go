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

	// Telemetry configures observability (OpenTelemetry + Prometheus).
	Telemetry struct {
		// Enabled enables the telemetry system (default: false).
		Enabled bool `mapstructure:"enabled"`
		// Traces enables OpenTelemetry tracing (default: true).
		Traces bool `mapstructure:"traces"`
		// TracesEndpoint is the OTLP HTTP endpoint (default: "http://localhost:4318").
		TracesEndpoint string `mapstructure:"traces_endpoint"`
		// Metrics enables Prometheus metrics export (default: true).
		Metrics bool `mapstructure:"metrics"`
		// MetricsPath is the HTTP path for Prometheus metrics (default: "/metrics").
		MetricsPath string `mapstructure:"metrics_path"`
		// MetricsPort is the HTTP port for the Prometheus metrics server (default: 9090).
		MetricsPort int `mapstructure:"metrics_port"`
	} `mapstructure:"telemetry"`

	// Security configures the CVE and vulnerability scanning.
	Security struct {
		// Enabled enables payload vulnerability scanning (default: true).
		Enabled bool `mapstructure:"enabled"`
		// Block enables blocking of requests that match CVE patterns (default: true).
		Block bool `mapstructure:"block"`
		// RawTCPTimeoutMS is the initial read timeout for raw TCP connections (default: 100ms).
		RawTCPTimeoutMS int `mapstructure:"raw_tcp_timeout_ms"`
	} `mapstructure:"security"`

	// WebUI configures the secure administrative Web dashboard.
	WebUI struct {
		// Enabled enables the Web UI server (default: true).
		Enabled bool `mapstructure:"enabled"`
		// Port is the HTTP port for the Web UI (default: 9091).
		Port int `mapstructure:"port"`
		// Token is the static access token. If empty, a secure token is generated on startup.
		Token string `mapstructure:"token"`
		// TLSEnabled enables HTTPS using the internal MITM CA (default: false).
		TLSEnabled bool `mapstructure:"tls_enabled"`
	} `mapstructure:"webui"`
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
	cfg.Telemetry.Enabled = false
	cfg.Telemetry.Traces = true
	cfg.Telemetry.TracesEndpoint = "http://localhost:4318"
	cfg.Telemetry.Metrics = true
	cfg.Telemetry.MetricsPath = "/metrics"
	cfg.Telemetry.MetricsPort = 9090
	cfg.Security.Enabled = true
	cfg.Security.Block = true
	cfg.Security.RawTCPTimeoutMS = 100
	cfg.WebUI.Enabled = true
	cfg.WebUI.Port = 9091
	cfg.WebUI.Token = ""

	viper.Reset()

	viper.SetEnvPrefix("SONIC")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	_ = viper.BindEnv("listen_port", "SONIC_LISTEN_PORT")
	_ = viper.BindEnv("mode", "SONIC_MODE")
	_ = viper.BindEnv("tls.ca_dir", "SONIC_TLS_CA_DIR")
	_ = viper.BindEnv("tls.auto_generate", "SONIC_TLS_AUTO_GENERATE")
	_ = viper.BindEnv("runtime.timeout_ms", "SONIC_RUNTIME_TIMEOUT_MS")
	_ = viper.BindEnv("runtime.pool_size", "SONIC_RUNTIME_POOL_SIZE")
	_ = viper.BindEnv("runtime.failsafe", "SONIC_RUNTIME_FAILSAFE")
	_ = viper.BindEnv("telemetry.enabled", "SONIC_TELEMETRY_ENABLED")
	_ = viper.BindEnv("telemetry.traces", "SONIC_TELEMETRY_TRACES")
	_ = viper.BindEnv("telemetry.traces_endpoint", "SONIC_TELEMETRY_TRACES_ENDPOINT")
	_ = viper.BindEnv("telemetry.metrics", "SONIC_TELEMETRY_METRICS")
	_ = viper.BindEnv("telemetry.metrics_path", "SONIC_TELEMETRY_METRICS_PATH")
	_ = viper.BindEnv("telemetry.metrics_port", "SONIC_TELEMETRY_METRICS_PORT")
	_ = viper.BindEnv("security.enabled", "SONIC_SECURITY_ENABLED")
	_ = viper.BindEnv("security.block", "SONIC_SECURITY_BLOCK")
	_ = viper.BindEnv("security.raw_tcp_timeout_ms", "SONIC_SECURITY_RAW_TCP_TIMEOUT_MS")
	_ = viper.BindEnv("webui.enabled", "SONIC_WEBUI_ENABLED")
	_ = viper.BindEnv("webui.port", "SONIC_WEBUI_PORT")
	_ = viper.BindEnv("webui.token", "SONIC_WEBUI_TOKEN")
	_ = viper.BindEnv("webui.tls_enabled", "SONIC_WEBUI_TLS_ENABLED")

	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName("sonic")
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
	if c.Runtime.TimeoutMS <= 0 {
		return fmt.Errorf("runtime.timeout_ms deve ser maior que 0")
	}
	if c.Runtime.PoolSize <= 0 {
		return fmt.Errorf("runtime.pool_size deve ser maior que 0")
	}
	if c.WebUI.Enabled && (c.WebUI.Port <= 0 || c.WebUI.Port > 65535) {
		return fmt.Errorf("webui.port invalida: %d", c.WebUI.Port)
	}
	if c.Security.Enabled && c.Security.RawTCPTimeoutMS <= 0 {
		return fmt.Errorf("security.raw_tcp_timeout_ms deve ser maior que 0")
	}
	if c.TLS.CADir == "" {
		return fmt.Errorf("tls.ca_dir nao pode estar vazia")
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

security:
  enabled: true
  block: true
  raw_tcp_timeout_ms: 100

bypass_domains:
  - "*.stripe.com"
  - "*.amazonaws.com"
  - "*.apple.com"

telemetry:
  enabled: false
  traces: true
  traces_endpoint: "http://localhost:4318"
  metrics: true
  metrics_path: "/metrics"
  metrics_port: 9090

webui:
  enabled: true
  port: 9091
  token: ""
`
	filePath := filepath.Join(dir, "sonic.yaml")
	return os.WriteFile(filePath, []byte(content), 0644)
}
