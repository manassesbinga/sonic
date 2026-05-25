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

var (
	cfgFile string
	mode    string
)

var RootCmd = &cobra.Command{
	Use:   "channelw",
	Short: "ChannelWorkers (NetFn) - Transparent L7 In-Network JavaScript Engine",
	Long: `ChannelWorkers (NetFn) eBPF Sockmap-accelerated transparent proxy 
that runs JavaScript directly in the network transmission channel.`,
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Inicializa um novo projeto ChannelWorkers",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Printf("Erro ao obter diretorio atual: %v\n", err)
			return
		}

		// Cria pasta functions/
		functionsDir := filepath.Join(cwd, "functions")
		if err := os.MkdirAll(functionsDir, 0755); err != nil {
			fmt.Printf("Erro ao criar diretorio functions/: %v\n", err)
			return
		}

		// Cria pasta certs/
		certsDir := filepath.Join(cwd, "certs")
		if err := os.MkdirAll(certsDir, 0755); err != nil {
			fmt.Printf("Erro ao criar diretorio certs/: %v\n", err)
			return
		}

		// Cria arquivo de configuracao default
		err = config.CreateDefaultConfigFile(cwd)
		if err != nil {
			fmt.Printf("Erro ao criar arquivo de configuracao: %v\n", err)
			return
		}

		// Cria funcao hello.js de exemplo
		helloJS := `// functions/hello.js

function onTraffic(request) {
    // Executado no canal — antes de chegar ao servidor
    log("Interceptado Request: " + request.method + " " + request.url);
    request.headers["X-Channel-Worker"] = "Active";
    request.headers["X-Request-Time"] = Date.now().toString();
    return request;
}

function onResponse(response) {
    // Executado no canal — antes de chegar ao cliente
    log("Interceptado Response");
    response.headers["X-Processed-By"] = "ChannelWorkers";
    return response;
}
`
		err = os.WriteFile(filepath.Join(functionsDir, "hello.js"), []byte(helloJS), 0644)
		if err != nil {
			fmt.Printf("Erro ao criar hello.js: %v\n", err)
			return
		}

		fmt.Println("🎉 Projeto ChannelWorkers inicializado com sucesso!")
		fmt.Println("Estrutura gerada:")
		fmt.Println("  ├── channelworkers.yaml (configuracao)")
		fmt.Println("  ├── functions/")
		fmt.Println("  │   └── hello.js (exemplo de worker)")
		fmt.Println("  └── certs/ (armazenamento de certificados)")
	},
}

var newCmd = &cobra.Command{
	Use:   "new [name]",
	Short: "Cria uma nova funcao de canal JS",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		fileName := name
		if filepath.Ext(name) != ".js" {
			fileName = name + ".js"
		}

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Printf("Erro ao obter diretorio atual: %v\n", err)
			return
		}

		functionsDir := filepath.Join(cwd, "functions")
		filePath := filepath.Join(functionsDir, fileName)

		_ = os.MkdirAll(functionsDir, 0755)

		if _, err := os.Stat(filePath); err == nil {
			fmt.Printf("Erro: Ficheiro %s ja existe!\n", fileName)
			return
		}

		template := fmt.Sprintf(`// functions/%s

function onTraffic(request) {
    // Intercepta e altera tráfego de ida
    return request;
}

function onResponse(response) {
    // Intercepta e altera tráfego de volta
    return response;
}
`, fileName)

		err = os.WriteFile(filePath, []byte(template), 0644)
		if err != nil {
			fmt.Printf("Erro ao escrever ficheiro: %v\n", err)
			return
		}

		fmt.Printf("✨ Nova funcao criada em: functions/%s\n", fileName)
	},
}

// readAndUnifyFunctions le e concatena todos os arquivos .js do diretorio functions/
func readAndUnifyFunctions(dir string) (string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	var foundAny bool

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".js") {
			content, err := os.ReadFile(filepath.Join(dir, file.Name()))
			if err != nil {
				return "", fmt.Errorf("erro ao ler %s: %w", file.Name(), err)
			}
			sb.Write(content)
			sb.WriteString("\n\n")
			foundAny = true
		}
	}

	if !foundAny {
		// Retorna stub padrão vazio se nao houver nenhum script na pasta
		return `
			function onTraffic(r) { return r; }
			function onResponse(r) { return r; }
		`, nil
	}

	return sb.String(), nil
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Inicia o motor ChannelWorkers em producao",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fmt.Printf("Erro ao carregar configuracao: %v\n", err)
			os.Exit(1)
		}

		if mode != "" {
			cfg.Mode = mode
		}

		// Carrega e unifica scripts do utilizador
		jsCode, err := readAndUnifyFunctions("./functions")
		if err != nil {
			fmt.Printf("Erro ao ler funcoes JS: %v\n", err)
			os.Exit(1)
		}

		// Inicializa motores
		jsEngine, err := runtime.NewJSEngine(jsCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
		if err != nil {
			fmt.Printf("Erro ao inicializar motor JS: %v\n", err)
			os.Exit(1)
		}

		mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
		if err != nil {
			fmt.Printf("Erro ao inicializar motor TLS MITM: %v\n", err)
			os.Exit(1)
		}

		transparentProxy := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)
		err = transparentProxy.Start()
		if err != nil {
			fmt.Printf("Erro ao iniciar proxy: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("🚀 ChannelWorkers iniciado com sucesso no porto %d!\n", cfg.ListenPort)
		fmt.Printf("Modo de Operacao: [%s]\n", cfg.Mode)
		fmt.Println("Pressione Ctrl+C para encerrar...")

		// Aguarda sinais do sistema operacional
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		fmt.Println("\nEncerrando motor de forma graciosa...")
		transparentProxy.Stop()
		fmt.Println("Motor encerrado. Bye!")
	},
}

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Inicia o motor em modo desenvolvimento (hot-reload)",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fmt.Printf("Erro ao carregar configuracao: %v\n", err)
			os.Exit(1)
		}

		if mode != "" {
			cfg.Mode = mode
		}

		// Garante que o diretorio functions existe
		_ = os.MkdirAll("./functions", 0755)

		// Carrega e unifica scripts
		jsCode, err := readAndUnifyFunctions("./functions")
		if err != nil {
			fmt.Printf("Erro ao ler funcoes JS: %v\n", err)
			os.Exit(1)
		}

		// Inicializa motores
		jsEngine, err := runtime.NewJSEngine(jsCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
		if err != nil {
			fmt.Printf("Erro ao inicializar motor JS: %v\n", err)
			os.Exit(1)
		}

		mitmEngine, err := mitm.NewMITMEngine(cfg.TLS.CADir)
		if err != nil {
			fmt.Printf("Erro ao inicializar motor TLS MITM: %v\n", err)
			os.Exit(1)
		}

		transparentProxy := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)
		err = transparentProxy.Start()
		if err != nil {
			fmt.Printf("Erro ao iniciar proxy: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("🛠️  Modo Desenvolvimento (Hot-Reload) iniciado no porto %d!\n", cfg.ListenPort)
		fmt.Println("Monitorando alteracoes em ./functions/*.js...")

		// Inicializa fsnotify watcher
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			fmt.Printf("Erro ao criar monitor de arquivos: %v\n", err)
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
					// Se for escrita ou criacao de arquivo JS
					if (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) && strings.HasSuffix(event.Name, ".js") {
						fmt.Printf("🔄 Alteracao detectada: %s. Atualizando motor...\n", filepath.Base(event.Name))

						newCode, err := readAndUnifyFunctions("./functions")
						if err != nil {
							fmt.Printf("[HOT-RELOAD ERROR] Falha ao ler scripts: %v\n", err)
							continue
						}

						newEngine, err := runtime.NewJSEngine(newCode, cfg.Runtime.TimeoutMS, cfg.Runtime.PoolSize)
						if err != nil {
							fmt.Printf("[HOT-RELOAD ERROR] Codigo JS invalido: %v. Mantendo versao anterior.\n", err)
							continue
						}

						// Atualiza o JSEngine no proxy de forma thread-safe!
						transparentProxy.SetJSEngine(newEngine)
						fmt.Println("⚡ Hot-Reload concluido com sucesso! Pool de VMs atualizado.")
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					fmt.Printf("[WATCHER ERROR] %v\n", err)
				}
			}
		}()

		// Adiciona a pasta ./functions ao watcher
		err = watcher.Add("./functions")
		if err != nil {
			fmt.Printf("Erro ao monitorar pasta ./functions: %v\n", err)
			os.Exit(1)
		}

		// Aguarda sinais
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		fmt.Println("\nEncerrando motor de forma graciosa...")
		transparentProxy.Stop()
		fmt.Println("Motor encerrado. Bye!")
	},
}

var caCmd = &cobra.Command{
	Use:   "ca",
	Short: "Gestao de CA Raiz local",
}

var caInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Gera a CA Raiz local",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fmt.Printf("Erro ao carregar configuracao: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Gerando CA Raiz local no diretorio: %s...\n", cfg.TLS.CADir)
		_, err = mitm.LoadOrCreateCA(cfg.TLS.CADir)
		if err != nil {
			fmt.Printf("Erro ao gerar CA: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("🎉 CA Raiz gerada com sucesso!")
		fmt.Printf("  ├── Certificado: %s/ca.pem\n", cfg.TLS.CADir)
		fmt.Printf("  └── Chave Privada: %s/ca-key.pem (Protegida)\n", cfg.TLS.CADir)
		fmt.Println("\nIMPORTANTE: Instale o ca.pem na trust store do seu sistema/dispositivo cliente para que ele confie nas interceptacoes transparentes HTTPS.")
	},
}

func Execute() {
	RootCmd.AddCommand(initCmd)
	RootCmd.AddCommand(newCmd)
	RootCmd.AddCommand(startCmd)
	RootCmd.AddCommand(devCmd)
	caCmd.AddCommand(caInstallCmd)
	RootCmd.AddCommand(caCmd)

	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./channelworkers.yaml)")
	RootCmd.PersistentFlags().StringVar(&mode, "mode", "", "mode overrides YAML (intercept, passthrough-all, observe)")
}
