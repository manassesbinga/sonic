package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/manassesbinga/sonic/config"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Sonic project",
	Long: `Initialize a new Sonic project in the current directory.

Creates the standard Sonic project structure:
  functions/   - Directory for JavaScript worker functions
  certs/       - Directory for TLS certificates
  sonic.yaml   - Default configuration file

To start developing, run:  sonic dev
To run in production:     sonic start`,
	Run: func(cmd *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err != nil {
			fail(fmt.Sprintf("error getting current directory: %v", err))
			return
		}

		functionsDir := filepath.Join(cwd, "functions")
		if err := os.MkdirAll(functionsDir, 0755); err != nil {
			fail(fmt.Sprintf("error creating functions/: %v", err))
			return
		}

		certsDir := filepath.Join(cwd, "certs")
		if err := os.MkdirAll(certsDir, 0755); err != nil {
			fail(fmt.Sprintf("error creating certs/: %v", err))
			return
		}

		if err := config.CreateDefaultConfigFile(cwd); err != nil {
			fail(fmt.Sprintf("error creating config: %v", err))
			return
		}

		helloJS := `// functions/hello.js — Sonic Edge Worker
//
// Compatible with Cloudflare Workers API:
//   - Request, Response, Headers, fetch
//
// onTraffic:  intercept/modify requests before they reach the server
// onResponse: intercept/modify responses before they reach the client

function onTraffic(request) {
    log("Intercepted: " + request.method + " " + request.url);
    request.headers.set("X-Sonic-Worker", "active");
    request.headers.set("X-Request-Time", Date.now().toString());
    return request;
}

function onResponse(response) {
    response.headers.set("X-Sonic-Proxy", "enabled");
    return response;
}
`
		if err := os.WriteFile(filepath.Join(functionsDir, "hello.js"), []byte(helloJS), 0644); err != nil {
			fail(fmt.Sprintf("error creating hello.js: %v", err))
			return
		}

		fmt.Println()
		success("Sonic project initialized successfully")
		fmt.Println()
		fmt.Println("  " + Bold("Structure:"))
		fmt.Println("    " + Cyan("├──") + " sonic.yaml" + Yellow("          # Configuration"))
		fmt.Println("    " + Cyan("├──") + " functions/" + Yellow("         # Your edge workers"))
		fmt.Println("    " + Cyan("│   └──") + " hello.js" + Yellow("     # Example worker"))
		fmt.Println("    " + Cyan("└──") + " certs/" + Yellow("            # TLS certificates"))
		fmt.Println()
		info("Next steps:")
		fmt.Println("    " + Cyan("$") + " sonic dev        " + Yellow("Start development mode"))
		fmt.Println("    " + Cyan("$") + " sonic run hello  " + Yellow("Test a worker directly"))
		fmt.Println("    " + Cyan("$") + " sonic start      " + Yellow("Start production proxy"))
		fmt.Println()
	},
}

var newCmd = &cobra.Command{
	Use:     "new [name]",
	Aliases: []string{"create", "generate"},
	Short:   "Create a new JavaScript worker function",
	Long: `Create a new worker function in the functions/ directory.

The name can be with or without .js extension.
Example:  sonic new auth     →  functions/auth.js
          sonic new waf.js   →  functions/waf.js`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		fileName := name
		if filepath.Ext(name) != ".js" {
			fileName = name + ".js"
		}

		cwd, _ := os.Getwd()
		functionsDir := filepath.Join(cwd, "functions")
		filePath := filepath.Join(functionsDir, fileName)

		_ = os.MkdirAll(functionsDir, 0755)

		if _, err := os.Stat(filePath); err == nil {
			fail(fmt.Sprintf("worker %s already exists", fileName))
			return
		}

		template := fmt.Sprintf(`// functions/%s — Sonic Edge Worker
//
// onTraffic(request):  modify or block requests
// onResponse(response): modify responses

function onTraffic(request) {
    log("Processing: " + request.url);
    request.headers.set("X-Worker", %q);
    return request;
}

function onResponse(response) {
    return response;
}
`, fileName, name)

		if err := os.WriteFile(filePath, []byte(template), 0644); err != nil {
			fail(fmt.Sprintf("error creating worker: %v", err))
			return
		}

		success("worker created: functions/" + fileName)
	},
}
