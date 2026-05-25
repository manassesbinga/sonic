// Package sonic provides a unified, embeddable API for the Sonic edge JavaScript engine.
//
// Use this package to embed Sonic in your Go application as a library/SDK.
// It wraps the config, runtime, mitm, and proxy sub-packages into a single interface.
//
// Basic usage:
//
//	import "github.com/manassesbinga/sonic/sdk"
//
//	// Start Sonic with JS worker
//	s, err := sonic.New(sonic.Config{
//	    JSCode: `function onTraffic(r) { r.headers["X-Sonic"]="1"; return r; }`,
//	})
//	if err != nil { log.Fatal(err) }
//	defer s.Stop()
//
//	go s.StartBackground()
//
//	// Or execute a worker directly (no proxy)
//	result, err := s.RunWorker("GET", "https://example.com/")
//
// See the _examples/ directory for complete usage.
package sonic

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/mitm"
	"github.com/manassesbinga/sonic/proxy"
	"github.com/manassesbinga/sonic/runtime"
)

// Config configures the Sonic engine. All fields have sensible defaults.
type Config struct {
	// ListenPort is the proxy listen port (default: 8443).
	ListenPort int

	// Mode sets the proxy mode: "intercept", "passthrough-all", "observe".
	Mode string

	// JSCode is the JavaScript worker source code.
	JSCode string

	// CADir is the directory for CA certificates (default: "./certs").
	CADir string

	// TimeoutMS is the JS execution timeout in milliseconds (default: 50).
	TimeoutMS int

	// PoolSize is the number of pre-warmed JS VM instances (default: 64).
	PoolSize int

	// Failsafe sets behavior on JS error: "bypass" or "block" (default: "bypass").
	Failsafe string
}

func defaultConfig() Config {
	return Config{
		ListenPort: 8443,
		Mode:       "intercept",
		CADir:      "./certs",
		TimeoutMS:  50,
		PoolSize:   64,
		Failsafe:   "bypass",
	}
}

// Sonic is the main engine that ties together the proxy, MITM, and JS runtime.
// Create one via New() or LoadConfig(), then Start() to begin.
type Sonic struct {
	cfg      *config.Config
	jsEngine *runtime.JSEngine
	proxy    *proxy.TransparentProxy
	stopped  chan struct{}
}

// New creates a Sonic engine with the given configuration.
// It initializes the JS runtime, MITM engine, and transparent proxy.
//
// Example:
//
//	s, err := sonic.New(sonic.Config{
//	    JSCode: `function onTraffic(r) { return r; }`,
//	})
func New(cfg Config) (*Sonic, error) {
	if cfg.ListenPort == 0 {
		cfg = defaultConfig()
	}
	if cfg.CADir == "" {
		cfg.CADir = "./certs"
	}
	if cfg.TimeoutMS == 0 {
		cfg.TimeoutMS = 50
	}
	if cfg.PoolSize == 0 {
		cfg.PoolSize = 64
	}
	if cfg.Failsafe == "" {
		cfg.Failsafe = "bypass"
	}
	if cfg.Mode == "" {
		cfg.Mode = "intercept"
	}

	fullCfg := &config.Config{}
	fullCfg.ListenPort = cfg.ListenPort
	fullCfg.Mode = cfg.Mode
	fullCfg.Runtime.TimeoutMS = cfg.TimeoutMS
	fullCfg.Runtime.PoolSize = cfg.PoolSize
	fullCfg.Runtime.Failsafe = cfg.Failsafe
	fullCfg.TLS.CADir = cfg.CADir
	fullCfg.TLS.AutoGenerate = true
	fullCfg.Logging.Level = "info"
	fullCfg.Logging.Format = "text"

	if err := fullCfg.Validate(); err != nil {
		return nil, fmt.Errorf("sonic: invalid config: %w", err)
	}

	jsCode := cfg.JSCode
	if jsCode == "" {
		jsCode = `
			function onTraffic(r) { return r; }
			function onResponse(r) { return r; }
		`
	}

	jsEngine, err := runtime.NewJSEngine(jsCode, fullCfg.Runtime.TimeoutMS, fullCfg.Runtime.PoolSize)
	if err != nil {
		return nil, fmt.Errorf("sonic: js engine: %w", err)
	}

	mitmEngine, err := mitm.NewMITMEngine(fullCfg.TLS.CADir)
	if err != nil {
		return nil, fmt.Errorf("sonic: mitm engine: %w", err)
	}

	transparentProxy := proxy.NewTransparentProxy(fullCfg, mitmEngine, jsEngine)

	return &Sonic{
		cfg:      fullCfg,
		jsEngine: jsEngine,
		proxy:    transparentProxy,
		stopped:  make(chan struct{}),
	}, nil
}

// LoadConfig creates a Sonic engine from a YAML configuration file.
// JS worker code is loaded from ./functions/ directory.
//
// Example:
//
//	s, err := sonic.LoadConfig("sonic.yaml")
func LoadConfig(configPath string) (*Sonic, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("sonic: config: %w", err)
	}

	jsCode := ""
	if files, err := os.ReadDir("./functions"); err == nil {
		var sb strings.Builder
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".js") {
				data, _ := os.ReadFile(filepath.Join("./functions", f.Name()))
				sb.Write(data)
				sb.WriteString("\n")
			}
		}
		jsCode = sb.String()
	}
	if jsCode == "" {
		jsCode = `
			function onTraffic(r) { return r; }
			function onResponse(r) { return r; }
		`
	}

	jsEngine, err := runtime.NewJSEngine(jsCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
	if err != nil {
		return nil, fmt.Errorf("sonic: js engine: %w", err)
	}

	mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
	if err != nil {
		return nil, fmt.Errorf("sonic: mitm engine: %w", err)
	}

	transparentProxy := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)

	return &Sonic{
		cfg:      cfg,
		jsEngine: jsEngine,
		proxy:    transparentProxy,
		stopped:  make(chan struct{}),
	}, nil
}

// Start begins the transparent proxy listener and blocks until Stop() is called
// or a SIGINT/SIGTERM signal is received.
func (s *Sonic) Start() error {
	if err := s.proxy.Start(); err != nil {
		return fmt.Errorf("sonic: proxy: %w", err)
	}

	<-s.stopped
	return nil
}

// StartBackground begins the proxy listener in a non-blocking goroutine.
// Call Stop() to shut down.
func (s *Sonic) StartBackground() error {
	return s.proxy.Start()
}

// Stop gracefully shuts down the proxy engine and waits for connections to drain.
func (s *Sonic) Stop() {
	select {
	case <-s.stopped:
	default:
		close(s.stopped)
	}
	s.proxy.Stop()
}

// WaitForSignal blocks until SIGINT or SIGTERM, then stops the engine.
func (s *Sonic) WaitForSignal() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	s.Stop()
}

// RunWorker executes the onTraffic function with a synthetic request and returns the result.
// Useful for testing workers or using Sonic as a JS execution engine without the proxy.
//
// Parameters:
//   - method: HTTP method (GET, POST, etc.)
//   - url: request URL
//   - headers: optional "Key: Value" pairs
//
// Example:
//
//	result, err := s.RunWorker("GET", "https://api.example.com/",
//	    "Authorization: Bearer token123",
//	    "X-Debug: true",
//	)
func (s *Sonic) RunWorker(method, url string, headers ...string) (*runtime.TrafficResult, error) {
	req := &runtime.Request{
		Method:  method,
		URL:     url,
		Path:    extractPath(url),
		Headers: make(map[string]string),
	}

	for _, h := range headers {
		if idx := strings.Index(h, ":"); idx >= 0 {
			k := strings.TrimSpace(h[:idx])
			v := strings.TrimSpace(h[idx+1:])
			if k != "" {
				req.Headers[k] = v
			}
		}
	}

	return s.jsEngine.RunOnTraffic(req)
}

// Config returns the current configuration.
func (s *Sonic) Config() *config.Config {
	return s.cfg
}

// JSEngine returns the underlying JS runtime for direct access.
func (s *Sonic) JSEngine() *runtime.JSEngine {
	return s.jsEngine
}

func extractPath(url string) string {
	if idx := strings.Index(url, "://"); idx >= 0 {
		after := url[idx+3:]
		if slashIdx := strings.Index(after, "/"); slashIdx >= 0 {
			return after[slashIdx:]
		}
	}
	return "/"
}
