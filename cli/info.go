package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Display project and system information",
	Long: `Display detailed information about the Sonic project,
system capabilities, and configuration.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println()
		fmt.Println("  " + Bold("Sonic") + " " + Version)
		fmt.Println("  " + strings.Repeat("─", 40))
		fmt.Println()

		fmt.Println("  " + Bold("System:"))
		fmt.Println("    OS:       " + runtime.GOOS + "/" + runtime.GOARCH)
		fmt.Println("    Go:       " + runtime.Version())
		fmt.Println("    CPUs:     " + fmt.Sprintf("%d", runtime.NumCPU()))

		hasEbpf := runtime.GOOS == "linux"
		ebpfStatus := Red("✗ not available")
		if hasEbpf {
			ebpfStatus = Green("✓ available (Linux)")
		}
		fmt.Println("    eBPF:     " + ebpfStatus)
		fmt.Println()

		fmt.Println("  " + Bold("Project:"))
		countWorkersStr := "0"
		workerFiles := ""
		if files, err := os.ReadDir("./functions"); err == nil {
			count := 0
			var names []string
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".js") {
					count++
					names = append(names, f.Name())
				}
			}
			countWorkersStr = fmt.Sprintf("%d", count)
			workerFiles = strings.Join(names, ", ")
		}
		fmt.Println("    Workers:  " + countWorkersStr)
		if workerFiles != "" {
			fmt.Println("    Files:    " + workerFiles)
		}

		if _, err := os.Stat("./sonic.yaml"); err == nil {
			fmt.Println("    Config:   sonic.yaml " + Green("✓"))
		} else {
			fmt.Println("    Config:   " + Yellow("not found (run 'sonic init')"))
		}

		if _, err := os.Stat("./certs/ca.pem"); err == nil {
			fmt.Println("    CA cert:  certs/ca.pem " + Green("✓"))
		} else {
			fmt.Println("    CA cert:  " + Yellow("not generated (run 'sonic ca install')"))
		}
		fmt.Println()

		fmt.Println("  " + Bold("Runtime:"))
		fmt.Println("    Engine:   goja (Go JavaScript VM)")
		fmt.Println("    API:      Cloudflare Workers-compatible")
		fmt.Println("    Protocol: HTTP/1.1, TLS 1.2+")
		fmt.Println("    Proxy:    Transparent MITM (dynamic certs)")
		fmt.Println()

		fmt.Println("  " + Bold("CLI Commands:"))
		fmt.Println("    " + Cyan("sonic init") + "      Initialize a project")
		fmt.Println("    " + Cyan("sonic new <n>") + "   Create a worker")
		fmt.Println("    " + Cyan("sonic run <f>") + "   Execute a worker directly")
		fmt.Println("    " + Cyan("sonic list") + "      List workers")
		fmt.Println("    " + Cyan("sonic start") + "     Start production proxy")
		fmt.Println("    " + Cyan("sonic dev") + "       Start dev mode (hot-reload)")
		fmt.Println("    " + Cyan("sonic ca install") + " Generate Root CA")
		fmt.Println()
	},
}
