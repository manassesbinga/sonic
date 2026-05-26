package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manassesbinga/sonic/runtime"
	"github.com/olekukonko/tablewriter"

	"github.com/spf13/cobra"
)

type runFlags struct {
	method     string
	url        string
	headers    []string
	body       string
	funcType   string
	jsonOutput bool
}

var runFlagsData runFlags

func init() {
	runCmd.Flags().StringVarP(&runFlagsData.method, "method", "X", "GET", "HTTP method for the test request")
	runCmd.Flags().StringVarP(&runFlagsData.url, "url", "u", "https://example.com/", "Request URL")
	runCmd.Flags().StringArrayVarP(&runFlagsData.headers, "header", "H", nil, "Request headers (can specify multiple: -H 'Key: Val')")
	runCmd.Flags().StringVarP(&runFlagsData.body, "body", "d", "", "Request body")
	runCmd.Flags().StringVarP(&runFlagsData.funcType, "func", "f", "onTraffic", "Function to run: onTraffic or onResponse")
	runCmd.Flags().BoolVarP(&runFlagsData.jsonOutput, "json", "j", false, "Output machine-readable JSON")
}

var runCmd = &cobra.Command{
	Use:   "run [file.js]",
	Short: "Execute a JavaScript worker function directly",
	Long: `Execute a Sonic worker function directly from the CLI.

Loads a .js file, runs the specified function (onTraffic or onResponse)
with a test request/response, and displays the modified result.

Useful for testing and debugging workers without starting the proxy.

Examples:
  sonic run hello.js
  sonic run hello.js --method POST --url https://api.example.com/data -H "Auth: token123"
  sonic run hello.js --func onResponse --body '{"ok":true}'
  sonic run functions/waf.js --header "X-WAF-Trigger: active"`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		jsFile := args[0]

		if !strings.HasSuffix(jsFile, ".js") {
			jsFile = jsFile + ".js"
		}

		if !filepath.IsAbs(jsFile) && !strings.Contains(jsFile, string(filepath.Separator)) {
			jsFile = filepath.Join("functions", jsFile)
		}

		code, err := os.ReadFile(jsFile)
		if err != nil {
			fail(fmt.Sprintf("error reading %s: %v", jsFile, err))
			os.Exit(1)
		}

		if runFlagsData.jsonOutput {
			info("loading worker: " + Bold(jsFile))
		}

		engine, err := runtime.NewJSEngine(string(code), 5000, 1)
		if err != nil {
			if runFlagsData.jsonOutput {
				j, _ := json.Marshal(map[string]string{"error": err.Error()})
				fmt.Println(string(j))
			} else {
				fail(fmt.Sprintf("JS compilation error: %v", err))
			}
			os.Exit(1)
		}
		if !runFlagsData.jsonOutput {
			success("worker compiled successfully")
		}

		headers := make(map[string]string)
		for _, h := range runFlagsData.headers {
			if parts := splitHeader(h); parts != nil {
				headers[parts[0]] = parts[1]
			}
		}

		if !runFlagsData.jsonOutput {
			fmt.Println()
			fmt.Println("  " + Bold("Input:"))
			fmt.Printf("    method:  %s\n", runFlagsData.method)
			fmt.Printf("    url:     %s\n", runFlagsData.url)
			fmt.Printf("    headers: %v\n", headers)
			if runFlagsData.body != "" {
				fmt.Printf("    body:    %s\n", truncate(runFlagsData.body, 80))
			}
			fmt.Println()
		}

		if runFlagsData.funcType == "onResponse" {
			resp := &runtime.Response{
				Status:  200,
				Headers: headers,
				Body:    runFlagsData.body,
			}

			modResp, err := engine.RunOnResponse(resp)
			if err != nil {
				if runFlagsData.jsonOutput {
					j, _ := json.Marshal(map[string]string{"error": err.Error()})
					fmt.Println(string(j))
				} else {
					warn(fmt.Sprintf("onResponse error: %v", err))
				}
				os.Exit(1)
			}

			if runFlagsData.jsonOutput {
				j, _ := json.Marshal(modResp)
				fmt.Println(string(j))
			} else {
				fmt.Println("  " + Bold(Green("Output (onResponse):")))
				printResponse(modResp)
			}
		} else {
			req := &runtime.Request{
				Method:  runFlagsData.method,
				URL:     runFlagsData.url,
				Path:    extractPath(runFlagsData.url),
				Headers: headers,
				Body:    runFlagsData.body,
			}

			result, err := engine.RunOnTraffic(req)
			if err != nil {
				if runFlagsData.jsonOutput {
					j, _ := json.Marshal(map[string]string{"error": err.Error()})
					fmt.Println(string(j))
				} else {
					warn(fmt.Sprintf("onTraffic error: %v", err))
				}
				os.Exit(1)
			}

			if runFlagsData.jsonOutput {
				j, _ := json.Marshal(result)
				fmt.Println(string(j))
			} else if result.IsResponse {
				fmt.Println("  " + Bold(Magenta("Output (direct response — WAF blocked):")))
				fmt.Printf("    status:  %d %s\n", result.Status, httpStatusText(result.Status))
				fmt.Printf("    headers: %v\n", result.Headers)
				fmt.Printf("    body:    %s\n", truncate(result.Body, 200))
			} else {
				fmt.Println("  " + Bold(Green("Output (modified request):")))
				fmt.Printf("    method:  %s\n", result.Method)
				fmt.Printf("    url:     %s\n", result.URL)
				fmt.Printf("    path:    %s\n", result.Path)
				fmt.Printf("    headers: %v\n", result.Headers)
				if result.Body != "" {
					fmt.Printf("    body:    %s\n", truncate(result.Body, 200))
				}
			}
		}
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "workers"},
	Short:   "List all available worker functions",
	Long: `List all JavaScript worker functions in the functions/ directory.

Shows file name, size, and exported function names (onTraffic, onResponse).`,
	Run: func(cmd *cobra.Command, args []string) {
		files, err := os.ReadDir("./functions")
		if err != nil {
			ErrorBox("No functions/ directory found")
			PrintArrow("Run 'sonic init' first to create a project")
			return
		}

		var jsFiles []string
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".js") {
				jsFiles = append(jsFiles, f.Name())
			}
		}

		if len(jsFiles) == 0 {
			Section("Workers")
			PrintBullet(Yellow("No .js worker files found in functions/"))
			PrintArrow("Create one: " + Cyan("sonic new myworker"))
			return
		}

		HRWithLabel("WORKERS")
		fmt.Println()

		table := NewPrettyTable([]string{"#", "Worker", "Size", "Handlers"})
		table.SetColumnAlignment([]int{
			tablewriter.ALIGN_CENTER,
			tablewriter.ALIGN_LEFT,
			tablewriter.ALIGN_RIGHT,
			tablewriter.ALIGN_LEFT,
		})

		for i, name := range jsFiles {
			info, _ := os.Stat(filepath.Join("./functions", name))
			sizeStr := "-"
			if info != nil {
				sizeStr = formatSize(info.Size())
			}
			hasOnTraffic := detectFunction(filepath.Join("./functions", name), "onTraffic")
			hasOnResponse := detectFunction(filepath.Join("./functions", name), "onResponse")

			funcs := ""
			if hasOnTraffic {
				funcs += "✓ onTraffic  "
			}
			if hasOnResponse {
				funcs += "✓ onResponse"
			}
			if funcs == "" {
				funcs = Yellow("(no handlers)")
			}

			table.Append([]string{fmt.Sprintf("%d", i+1), Bold(name), sizeStr, funcs})
		}

		table.Render()
		fmt.Println()
		HR()
		fmt.Println()
		PrintArrow("Run a worker: " + Cyan("sonic run <name>"))
		fmt.Println()
	},
}

func detectFunction(filePath, funcName string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "function "+funcName)
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

func splitHeader(h string) []string {
	idx := strings.Index(h, ":")
	if idx < 0 {
		return nil
	}
	key := strings.TrimSpace(h[:idx])
	val := strings.TrimSpace(h[idx+1:])
	if key == "" {
		return nil
	}
	return []string{key, val}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func httpStatusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 301:
		return "Moved Permanently"
	case 302:
		return "Found"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	default:
		return "Unknown"
	}
}

func printResponse(r *runtime.Response) {
	if r == nil {
		return
	}
	j, _ := json.MarshalIndent(r, "    ", "  ")
	fmt.Fprintf(os.Stderr, "    %s\n", string(j))
}
