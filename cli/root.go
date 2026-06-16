package cli

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"github.com/manassesbinga/sonic/config"
	"github.com/manassesbinga/sonic/mitm"
	"github.com/manassesbinga/sonic/proxy"
	"github.com/manassesbinga/sonic/runtime"
	"github.com/manassesbinga/sonic/telemetry"
	"github.com/manassesbinga/sonic/webui"
)

var Version = "1.4.0"

func Execute() {
	// Determinar o diretório do executável e setar como diretório de trabalho se for Windows Service
	inSvc, errSvc := isWindowsService()
	if errSvc == nil && inSvc {
		exePath, errExe := os.Executable()
		if errExe == nil {
			_ = os.Chdir(filepath.Dir(exePath))
		}
	}

	// Define as flags do CLI de forma simples
	cfgFile := flag.String("config", "", "config file (default ./sonic.yaml)")
	modeOverride := flag.String("mode", "", "override mode: \"intercept\", \"passthrough-all\", \"observe\"")
	devMode := flag.Bool("dev", false, "start Sonic in development mode (hot-reload)")
	showVersion := flag.Bool("version", false, "print version information")

	// Flags de Serviço do Windows
	serviceInstall := flag.Bool("service-install", false, "install Sonic as a Windows service")
	serviceUninstall := flag.Bool("service-uninstall", false, "uninstall Sonic Windows service")
	serviceStart := flag.Bool("service-start", false, "start Sonic Windows service")
	serviceStop := flag.Bool("service-stop", false, "stop Sonic Windows service")

	// Customiza a ajuda da CLI
	flag.Usage = func() {
		fmt.Println()
		PrintBanner(Version)
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  sonic [flags]")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  -config string")
		fmt.Println("        config file (default ./sonic.yaml)")
		fmt.Println("  -mode string")
		fmt.Println("        override mode: \"intercept\", \"passthrough-all\", \"observe\"")
		fmt.Println("  -dev")
		fmt.Println("        start Sonic in development mode (hot-reload)")
		fmt.Println("  -version")
		fmt.Println("        print version information")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  sonic")
		fmt.Println("  sonic -dev")
		fmt.Println("  sonic -config custom.yaml -mode passthrough-all")
		fmt.Println()
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("Sonic v%s\n", Version)
		return
	}

	// Tratar comandos do Windows Service
	if *serviceInstall {
		ManageService("install")
		return
	}
	if *serviceUninstall {
		ManageService("uninstall")
		return
	}
	if *serviceStart {
		ManageService("start")
		return
	}
	if *serviceStop {
		ManageService("stop")
		return
	}

	// 1. Carrega ou cria a configuração
	configFile := *cfgFile
	if configFile == "" {
		configFile = "./sonic.yaml"
	}

	// Se o arquivo de configuracao padrão nao existir, cria automaticamente
	if _, err := os.Stat(configFile); os.IsNotExist(err) && configFile == "./sonic.yaml" {
		info("configuration file 'sonic.yaml' not found. Creating default configuration...")
		if err := config.CreateDefaultConfigFile("."); err != nil {
			warn(fmt.Sprintf("failed to create default config file: %v", err))
		} else {
			success("default 'sonic.yaml' created successfully")
		}
	}

	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fail(fmt.Sprintf("error loading config: %v", err))
		os.Exit(1)
	}

	if *modeOverride != "" {
		valid := map[string]bool{"intercept": true, "passthrough-all": true, "observe": true}
		if !valid[*modeOverride] {
			fail(fmt.Sprintf("invalid mode: %q (options: intercept, passthrough-all, observe)", *modeOverride))
			os.Exit(1)
		}
		cfg.Mode = *modeOverride
	}

	// 2. Inicializa a Telemetria
	if cfg.Telemetry.Enabled {
		telCfg := telemetry.DefaultTelemetryConfig()
		telCfg.TracesEnabled = cfg.Telemetry.Traces
		telCfg.TracesEndpoint = cfg.Telemetry.TracesEndpoint
		telCfg.MetricsEnabled = cfg.Telemetry.Metrics
		telCfg.MetricsPath = cfg.Telemetry.MetricsPath
		telCfg.MetricsPort = cfg.Telemetry.MetricsPort
		if err := telemetry.InitFromConfig(telCfg); err != nil {
			warn(fmt.Sprintf("telemetry init failed: %v (continuing without observability)", err))
		} else {
			info("telemetry enabled — traces: " + Bold(cfg.Telemetry.TracesEndpoint) + " | metrics: " + Bold(fmt.Sprintf(":%d%s", cfg.Telemetry.MetricsPort, cfg.Telemetry.MetricsPath)))
		}
	}

	// 3. Inicializa o Banco SQLite e o Armazenamento KV
	dbDir := filepath.Join(filepath.Dir(configFile), "data")
	if errDb := runtime.InitDB(dbDir); errDb != nil {
		fail(fmt.Sprintf("error initializing SQLite database: %v", errDb))
		os.Exit(1)
	}

	kvStore, err := runtime.NewKVStore()
	if err != nil {
		fail(fmt.Sprintf("error initializing KV store: %v", err))
		os.Exit(1)
	}

	// 4. Inicializa o WorkerManager e pasta functions
	_ = os.MkdirAll("./functions", 0755)
	manager := runtime.NewWorkerManager(kvStore)
	err = manager.LoadAllWorkers("./functions", cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
	if err != nil {
		fail(fmt.Sprintf("error loading workers: %v", err))
		os.Exit(1)
	}

	// 5. Inicializa o MITMEngine
	mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
	if err != nil {
		fail(fmt.Sprintf("error initializing MITM engine: %v", err))
		os.Exit(1)
	}

	// 6. Inicializa o Proxy e Handover Coordinator
	transparentProxy := proxy.NewTransparentProxy(cfg, mitmEngine, manager)
	coordinator := proxy.NewHandoverCoordinator(transparentProxy)
	listener, err := coordinator.StartListener()
	if err != nil {
		fail(fmt.Sprintf("error setting up listener: %v", err))
		os.Exit(1)
	}
	if err := transparentProxy.StartWithListener(listener); err != nil {
		fail(fmt.Sprintf("error starting proxy: %v", err))
		os.Exit(1)
	}
	coordinator.RegisterHandoverServer(listener)

	// Watch and hot-reload changes from sonic.yaml in real-time
	viper.OnConfigChange(func(e fsnotify.Event) {
		newCfg, err := config.LoadConfig(configFile)
		if err == nil {
			transparentProxy.UpdateConfig(newCfg)
			// Apply CVE Scanner blocker updates dynamically
			if newCfg.Security.Enabled && transparentProxy.CVEScanner() != nil {
				transparentProxy.CVEScanner().Block = newCfg.Security.Block
			}
			success("Configuration file hot-reloaded successfully (bypass domains, failsafe & security settings updated)")
		} else {
			warn(fmt.Sprintf("Failed to hot-reload configuration: %v", err))
		}
	})
	viper.WatchConfig()

	// 7. Inicializa o WebUI se ativado
	var adminServer *webui.AdminServer
	if cfg.WebUI.Enabled {
		var adminErr error
		adminServer, adminErr = webui.NewAdminServer(cfg)
		if adminErr != nil {
			warn(fmt.Sprintf("webui init failed: %v", adminErr))
		} else {
			// Injetar instâncias de armazenamento e workers
			adminServer.SetKVStore(kvStore)
			adminServer.SetWorkerEngine(manager)

			// Conectar o interceptador do Sandbox no proxy transparente
			if adminServer.Sandbox() != nil {
				transparentProxy.SetSandboxInterceptor(adminServer.Sandbox())
			}

			// Habilitar TLS na WebUI usando a CA interna do MITM
			if cfg.WebUI.TLSEnabled {
				if err := adminServer.SetCA(mitmEngine.CA(), true); err != nil {
					warn(fmt.Sprintf("webui tls setup failed: %v (falling back to HTTP)", err))
				}
			}

			if err := adminServer.Start(); err != nil {
				warn(fmt.Sprintf("webui start failed: %v", err))
			} else {
				printWebUIBanner(adminServer.Token(), cfg.WebUI.Port, cfg.WebUI.TLSEnabled)
			}
		}
	}

	// Imprime status de inicialização
	fmt.Println()
	modeName := cfg.Mode
	if *devMode {
		modeName += " (dev)"
	}
	success("Sonic started on port " + Bold(fmt.Sprintf("%d", cfg.ListenPort)))
	info("mode: " + Bold(modeName))
	info("workers: " + Bold(countWorkers()) + " function(s) loaded")
	info("VM pool: " + Bold(fmt.Sprintf("%d", cfg.Runtime.PoolSize)) + " runtimes | timeout: " + Bold(fmt.Sprintf("%dms", cfg.Runtime.TimeoutMS)))
	info("failsafe: " + Bold(cfg.Runtime.Failsafe))
	fmt.Println()

	// 8. Se ativado o modo de desenvolvimento, assiste alterações no diretório functions/
	if *devMode {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			warn(fmt.Sprintf("failed to create file watcher for hot-reload: %v", err))
		} else {
			defer watcher.Close()
			go func() {
				for {
					select {
					case event, ok := <-watcher.Events:
						if !ok {
							return
						}
						ext := strings.ToLower(filepath.Ext(event.Name))
						isWorkerFile := ext == ".js" || ext == ".wasm" || ext == ".py" || ext == ".sh" || ext == ".rb" || ext == ".pl" || ext == ".exe" || ext == ""
						if (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) && isWorkerFile {
							fmt.Println()
							warn("change detected: " + filepath.Base(event.Name))
							info("reloading workers...")

							oldManager := manager
							newManager := runtime.NewWorkerManager(kvStore)
							err = newManager.LoadAllWorkers("./functions", cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
							if err != nil {
								warn(fmt.Sprintf("reload failed: %v (keeping previous version)", err))
								continue
							}

							transparentProxy.SetJSEngine(newManager)
							manager = newManager
							if oldManager != nil {
								_ = oldManager.Close()
							}
							success("hot-reload complete — " + countWorkers() + " worker(s) active")
						}
					case err, ok := <-watcher.Errors:
						if !ok {
							return
						}
						warn(fmt.Sprintf("watcher error: %v", err))
					}
				}
			}()
			_ = watcher.Add("./functions")
			info("hot-reload watching: " + Bold("./functions/"))
			fmt.Println()
		}
	}

	// Canal de sinalização para parada
	stopChan := make(chan struct{})
	shutdownDoneChan := make(chan struct{})

	if inSvc {
		// Executando sob o Windows Service Control Manager
		go runServiceInSCM(stopChan, shutdownDoneChan)
	} else {
		// Executando normalmente no console
		fmt.Println("  " + Primary("Press Ctrl+C to stop"))
		fmt.Println()

		go func() {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan
			close(stopChan)
		}()
	}

	// Aguarda o sinal de encerramento (SCM ou Ctrl+C)
	<-stopChan

	fmt.Println()
	info("shutting down gracefully...")
	if adminServer != nil {
		_ = adminServer.Stop()
	}
	coordinator.Stop()
	transparentProxy.Stop()
	telemetry.Shutdown()
	if manager != nil {
		_ = manager.Close()
	}
	if kvStore != nil {
		_ = kvStore.Close()
	}
	_ = runtime.CloseDB() // Fecha banco de dados SQLite de forma limpa
	success("Sonic stopped")

	if inSvc {
		close(shutdownDoneChan)
		// Pequena folga de tempo para a goroutine do svc.Run enviar o sinal final de paragem
		time.Sleep(200 * time.Millisecond)
	}
}

func countWorkers() string {
	files, err := os.ReadDir("./functions")
	if err != nil {
		return "0"
	}
	count := 0
	supported := map[string]bool{
		".js": true, ".wasm": true, ".py": true, ".sh": true, ".rb": true, ".pl": true, ".exe": true,
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if supported[ext] || ext == "" {
			count++
		}
	}
	return fmt.Sprintf("%d", count)
}

func printWebUIBanner(token string, port int, tlsEnabled bool) {
	protocol := "http"
	if tlsEnabled {
		protocol = "https"
	}
	bannerText := fmt.Sprintf(
		"  SECURE ADMIN WEB UI RUNNING\n  Access: %s://localhost:%d/\n  Token: %s",
		protocol, port, token,
	)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#00f2fe")). // Ciano elétrico
		Padding(1, 3).
		Foreground(lipgloss.Color("#f8fafc")).
		Background(lipgloss.Color("#050811")).
		Bold(true)

	fmt.Println()
	fmt.Println(style.Render(bannerText))
}
