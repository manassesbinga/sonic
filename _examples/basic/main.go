// Example: Basic Sonic proxy with a custom JS worker.
//
// This example starts Sonic, runs a JavaScript worker that adds
// a custom header to every request, then waits for SIGINT.
package main

import (
	"fmt"
	"log"

	"channelworkers/sdk"
)

const workerCode = `
function onTraffic(request) {
    // Add a custom header to every intercepted request
    request.headers.set("X-Sonic-Proxy", "enabled");
    request.headers.set("X-Request-ID", Date.now().toString(36));
    return request;
}

function onResponse(response) {
    response.headers.set("X-Powered-By", "Sonic");
    return response;
}
`

func main() {
	// Create the Sonic engine with JS worker code
	s, err := sonic.New(sonic.Config{
		JSCode:    workerCode,
		ListenPort: 8443,
		Mode:      "intercept",
		PoolSize:  8,
		TimeoutMS: 100,
	})
	if err != nil {
		log.Fatalf("Failed to create Sonic: %v", err)
	}
	defer s.Stop()

	fmt.Println("Sonic starting on :8443...")
	fmt.Println("Press Ctrl+C to stop.")

	// Block until signal or Stop()
	if err := s.Start(); err != nil {
		log.Fatalf("Sonic error: %v", err)
	}

	fmt.Println("Sonic stopped.")
}
