// Example: Integrate Sonic with a custom Go HTTP server.
//
// This shows how to use the Sonic JS engine alongside a standard
// net/http server. Requests pass through the worker before being handled.
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/manassesbinga/sonic/runtime"
)

const workerCode = `
function onTraffic(request) {
    log("HTTP " + request.method + " " + request.url);
    request.headers.set("X-Sonic", "custom-server");
    return request;
}
`

func main() {
	engine, err := runtime.NewJSEngine(workerCode, 100, 4)
	if err != nil {
		log.Fatalf("JS engine: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		workerReq := &runtime.Request{
			Method:  r.Method,
			URL:     r.URL.String(),
			Path:    r.URL.Path,
			Headers: map[string]string{},
		}
		for k := range r.Header {
			workerReq.Headers[k] = r.Header.Get(k)
		}

		result, err := engine.RunOnTraffic(workerReq)
		if err != nil {
			http.Error(w, "Worker error", 502)
			return
		}

		if result.IsResponse {
			w.WriteHeader(result.Status)
			fmt.Fprint(w, result.Body)
			return
		}

		for k, v := range result.Headers {
			w.Header().Set(k, v)
		}
		fmt.Fprintf(w, "Hello from Sonic + Go!\nPath: %s\n", r.URL.Path)
	})

	fmt.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
