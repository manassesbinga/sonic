package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"channelworkers/config"
	"channelworkers/mitm"
	"channelworkers/proxy"
	"channelworkers/runtime"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start Sonic in production mode",
	Long: `Start the Sonic proxy engine in production mode.

Loads configuration, initializes the JS runtime pool and TLS MITM engine,
then starts listening for transparent proxy connections.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fail(fmt.Sprintf("error loading config: %v", err))
			os.Exit(1)
		}

		if mode != "" {
			cfg.Mode = mode
		}

		jsCode, err := readAndUnifyFunctions("./functions")
		if err != nil {
			fail(fmt.Sprintf("error reading JS functions: %v", err))
			os.Exit(1)
		}

		jsEngine, err := runtime.NewJSEngine(jsCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
		if err != nil {
			fail(fmt.Sprintf("error initializing JS engine: %v", err))
			os.Exit(1)
		}

		mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
		if err != nil {
			fail(fmt.Sprintf("error initializing MITM engine: %v", err))
			os.Exit(1)
		}

		transparentProxy := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)
		if err := transparentProxy.Start(); err != nil {
			fail(fmt.Sprintf("error starting proxy: %v", err))
			os.Exit(1)
		}

		fmt.Println()
		success("Sonic started on port " + Bold(fmt.Sprintf("%d", cfg.ListenPort)))
		info("mode: " + Bold(cfg.Mode))
		info("workers: " + Bold(countWorkers()) + " function(s) loaded")
		info("VM pool: " + Bold(fmt.Sprintf("%d", cfg.Runtime.PoolSize)) + " runtimes | timeout: " + Bold(fmt.Sprintf("%dms", cfg.Runtime.TimeoutMS)))
		info("failsafe: " + Bold(cfg.Runtime.Failsafe))
		fmt.Println()
		fmt.Println("  " + Yellow("Press Ctrl+C to stop"))
		fmt.Println()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		fmt.Println()
		info("shutting down gracefully...")
		transparentProxy.Stop()
		success("Sonic stopped")
	},
}

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Start Sonic in development mode (hot-reload)",
	Long: `Start Sonic with hot-reload enabled.

Watches the functions/ directory for changes and automatically
reloads the JS runtime pool without stopping the proxy.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fail(fmt.Sprintf("error loading config: %v", err))
			os.Exit(1)
		}

		if mode != "" {
			cfg.Mode = mode
		}

		_ = os.MkdirAll("./functions", 0755)

		jsCode, err := readAndUnifyFunctions("./functions")
		if err != nil {
			fail(fmt.Sprintf("error reading JS functions: %v", err))
			os.Exit(1)
		}

		jsEngine, err := runtime.NewJSEngine(jsCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
		if err != nil {
			fail(fmt.Sprintf("error initializing JS engine: %v", err))
			os.Exit(1)
		}

		mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
		if err != nil {
			fail(fmt.Sprintf("error initializing MITM engine: %v", err))
			os.Exit(1)
		}

		transparentProxy := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)
		if err := transparentProxy.Start(); err != nil {
			fail(fmt.Sprintf("error starting proxy: %v", err))
			os.Exit(1)
		}

		fmt.Println()
		success("Sonic [dev] started on port " + Bold(fmt.Sprintf("%d", cfg.ListenPort)))
		info("hot-reload watching: " + Bold("./functions/"))
		info("workers: " + Bold(countWorkers()) + " function(s) loaded")
		fmt.Println()

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			fail(fmt.Sprintf("error creating file watcher: %v", err))
			os.Exit(1)
		}
		defer watcher.Close()

		go func() {
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					if (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) && strings.HasSuffix(event.Name, ".js") {
						fmt.Println()
						warn("change detected: " + filepath.Base(event.Name))
						info("reloading workers...")

						newCode, err := readAndUnifyFunctions("./functions")
						if err != nil {
							warn(fmt.Sprintf("reload failed: %v (keeping previous version)", err))
							continue
						}

						newEngine, err := runtime.NewJSEngine(newCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
						if err != nil {
							warn(fmt.Sprintf("invalid JS code: %v (keeping previous version)", err))
							continue
						}

						transparentProxy.SetJSEngine(newEngine)
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
		info("watching for changes in ./functions/")

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		fmt.Println()
		info("shutting down gracefully...")
		transparentProxy.Stop()
		success("Sonic stopped")
	},
}

var caCmd = &cobra.Command{
	Use:   "ca",
	Short: "Certificate authority management",
}

var caInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Generate the local Root CA",
	Long: `Generate a local Root Certificate Authority for TLS interception.

The CA certificate (ca.pem) must be installed in the system trust store
of any device whose HTTPS traffic will be intercepted by Sonic.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fail(fmt.Sprintf("error loading config: %v", err))
			os.Exit(1)
		}

		info("generating Root CA in: " + cfg.TLS.CADir)
		_, err = mitm.LoadOrCreateCA(cfg.TLS.CADir)
		if err != nil {
			fail(fmt.Sprintf("error generating CA: %v", err))
			os.Exit(1)
		}

		fmt.Println()
		success("Root CA generated successfully")
		fmt.Println("  " + Cyan("├──") + " Certificate: " + Bold(cfg.TLS.CADir+"/ca.pem"))
		fmt.Println("  " + Cyan("└──") + " Private Key: " + Bold(cfg.TLS.CADir+"/ca-key.pem") + " (protected)")
		fmt.Println()
		info("Install ca.pem in your system/device trust store")
		fmt.Println("  " + Yellow("Linux:   sudo cp ca.pem /usr/local/share/ca-certificates/ && sudo update-ca-certificates"))
		fmt.Println("  " + Yellow("macOS:   sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.pem"))
		fmt.Println("  " + Yellow("Windows: certutil -addstore Root ca.pem"))
		fmt.Println()
	},
}

func countWorkers() string {
	files, err := os.ReadDir("./functions")
	if err != nil {
		return "0"
	}
	count := 0
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".js") {
			count++
		}
	}
	return fmt.Sprintf("%d", count)
}
