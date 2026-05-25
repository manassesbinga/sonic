// Example: Use Sonic purely as a JavaScript execution engine (no proxy).
//
// This example loads a worker and runs it directly, useful for
// testing workers or processing HTTP requests in batch.
package main

import (
	"fmt"
	"log"

	"github.com/manassesbinga/sonic/sdk"
)

const workerCode = `
function onTraffic(request) {
    log("Processing: " + request.method + " " + request.url);

    if (request.headers.get("x-block") === "true") {
        return new Response("Blocked by WAF", { status: 403 });
    }

    request.headers.set("X-Processed-By", "Sonic-Worker");
    return request;
}
`

func main() {
	s, err := sonic.New(sonic.Config{
		JSCode:    workerCode,
		PoolSize:  2,
		TimeoutMS: 100,
	})
	if err != nil {
		log.Fatalf("Failed to create Sonic: %v", err)
	}

	// Run onTraffic with a synthetic request
	result, err := s.RunWorker("POST", "https://api.example.com/data",
		"Content-Type: application/json",
		"Authorization: Bearer token123",
	)
	if err != nil {
		log.Fatalf("Worker error: %v", err)
	}

	fmt.Printf("Method:  %s\n", result.Method)
	fmt.Printf("URL:     %s\n", result.URL)
	fmt.Printf("Headers: %v\n", result.Headers)

	// Test WAF blocking
	result2, err := s.RunWorker("GET", "https://evil.com/",
		"x-block: true",
	)
	if err != nil {
		log.Fatalf("Worker error: %v", err)
	}
	fmt.Printf("\nWAF Blocked? %v (status=%d)\n", result2.IsResponse, result2.Status)
}
