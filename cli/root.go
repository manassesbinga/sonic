package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var Version = "1.0.0"

var (
	cfgFile string
	mode    string
)

var RootCmd = &cobra.Command{
	Use:   "sonic",
	Short: "Sonic — Multi-Language, Multi-Protocol Edge Engine",
	Long: Cyan(`
  ╔══════════════════════════════════════════════════════╗
  ║                     SONIC                           ║
  ║  Multi-Language, Multi-Protocol Edge Engine       ║
  ║  eBPF-accelerated | KV Store | WASM/JS/Native ║
  ╚══════════════════════════════════════════════════════╝
                      `) + `
Sonic is a platform for running logic over ANY network data!
- Multiple languages: JavaScript (Goja), WebAssembly (Rust/Go/C), Native
- Multiple protocols: HTTP, TCP, UDP, DNS, WebSocket, gRPC, QUIC
- Shared state: Persistent KV Store (bbolt)
- Compatible with Cloudflare Workers API
` + Yellow("  ▶ https://github.com/manassesbinga/sonic"),
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func Execute() {
	RootCmd.AddCommand(initCmd)
	RootCmd.AddCommand(newCmd)
	RootCmd.AddCommand(startCmd)
	RootCmd.AddCommand(devCmd)
	RootCmd.AddCommand(runCmd)
	RootCmd.AddCommand(listCmd)
	RootCmd.AddCommand(infoCmd)
	RootCmd.AddCommand(versionCmd)
	RootCmd.AddCommand(completionCmd)
	RootCmd.AddCommand(statusCmd)
	RootCmd.AddCommand(dashboardCmd)
	caCmd.AddCommand(caInstallCmd)
	RootCmd.AddCommand(caCmd)

	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default ./sonic.yaml)")
	RootCmd.PersistentFlags().StringVarP(&mode, "mode", "m", "", `override mode: "intercept", "passthrough-all", "observe"`)

	RootCmd.CompletionOptions.DisableDefaultCmd = true
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println()
		PrintBanner(Version)
		fmt.Println()

		HRWithLabel("VERSION INFO")
		fmt.Println()

		pairs := [][2]string{
			{"Sonic", "v" + Version},
			{"Engines", "JavaScript (Goja) + WebAssembly (wazero)"},
			{"API", "Cloudflare Workers-compatible"},
			{"Protocols", "HTTP/1.1, TLS 1.2+ (Multi-protocol coming soon)"},
			{"KV Store", "bbolt (Persistent)"},
			{"License", "MIT"},
		}

		for _, p := range pairs {
			KeyValue(p[0], p[1])
		}
		fmt.Println()
		HR()
		fmt.Println()
		PrintArrow("GitHub: " + Blue("https://github.com/manassesbinga/sonic"))
		fmt.Println()
	},
}

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion script",
	Args:  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
		case "bash":
			RootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			RootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			RootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			RootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		}
	},
}

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
		return `
			function onTraffic(r) { return r; }
			function onResponse(r) { return r; }
		`, nil
	}

	return sb.String(), nil
}
