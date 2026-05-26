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
		PrintBanner(Version)
		fmt.Println()

		HRWithLabel("SYSTEM")
		fmt.Println()

		hasEbpf := runtime.GOOS == "linux"
		ebpfStatus := Red("✗ Not available")
		ebpfNote := Yellow("(eBPF requires Linux)")
		if hasEbpf {
			ebpfStatus = Green("✓ Available")
			ebpfNote = Green("(Kernel bypass acceleration)")
		}

		sysPairs := [][2]string{
			{"OS", runtime.GOOS + "/" + runtime.GOARCH},
			{"Go", runtime.Version()},
			{"CPUs", fmt.Sprintf("%d cores", runtime.NumCPU())},
			{"eBPF", ebpfStatus + " " + ebpfNote},
		}

		for _, p := range sysPairs {
			KeyValue(p[0], p[1])
		}
		fmt.Println()

		HRWithLabel("PROJECT")
		fmt.Println()

		hasConfig := false
		hasCA := false
		workerCount := 0
		var workerNames []string

		if _, err := os.Stat("./sonic.yaml"); err == nil {
			hasConfig = true
		}
		if _, err := os.Stat("./certs/ca.pem"); err == nil {
			hasCA = true
		}

		if files, err := os.ReadDir("./functions"); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".js") {
					workerCount++
					workerNames = append(workerNames, f.Name())
				}
			}
		}

		KeyValueStatus("Config", "sonic.yaml", hasConfig)
		KeyValueStatus("CA Cert", "certs/ca.pem", hasCA)

		fmt.Println()
		KeyValue("Workers", fmt.Sprintf("%d", workerCount))
		if workerCount > 0 {
			fmt.Printf("              %s\n", Cyan(strings.Join(workerNames, ", ")))
		}
		fmt.Println()

		HRWithLabel("RUNTIME")
		fmt.Println()

		runtimePairs := [][2]string{
			{"Engine", "goja (Pure Go JavaScript VM)"},
			{"API", "Cloudflare Workers-compatible"},
			{"Protocol", "HTTP/1.1, TLS 1.2+"},
			{"Proxy", "Transparent MITM (dynamic certificates)"},
		}

		for _, p := range runtimePairs {
			KeyValue(p[0], p[1])
		}
		fmt.Println()

		HRWithLabel("QUICK COMMANDS")
		fmt.Println()

		commands := []struct {
			cmd  string
			desc string
		}{
			{"sonic init", "Initialize a new project"},
			{"sonic new <name>", "Create a worker"},
			{"sonic list", "List all workers"},
			{"sonic run <name>", "Test a worker directly"},
			{"sonic dev", "Development mode (hot-reload)"},
			{"sonic start", "Production mode"},
			{"sonic ca install", "Generate Root CA for TLS MITM"},
		}

		table := NewTable()
		for _, c := range commands {
			table.Append([]string{"", Cyan(fmt.Sprintf("%-22s", c.cmd)), Yellow(c.desc)})
		}
		table.Render()
		fmt.Println()
		HR()
		fmt.Println()
		PrintArrow("Docs: " + Blue("https://github.com/manassesbinga/sonic"))
		fmt.Println()
	},
}
