// Package runtime provides a JavaScript execution engine compatible with
// the Cloudflare Workers API.
//
// It uses goja (Go JavaScript VM) with a pre-compiled script pool for
// concurrent request handling. Supports onTraffic and onResponse hooks,
// CPU watchdog timeouts, and Web Standard APIs (Request, Response, Headers, fetch).
//
// Usage:
//
//	engine, err := runtime.NewJSEngine(jsCode, 50, 64)
//	req := &runtime.Request{Method: "GET", URL: "https://example.com/"}
//	result, err := engine.RunOnTraffic(req)
package runtime

// Request represents an intercepted HTTP request exposed to the JavaScript VM.
// Fields are directly accessible from JS: req.method, req.url, req.headers, req.body.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Response represents an intercepted HTTP response exposed to the JavaScript VM.
// Fields are directly accessible from JS: resp.status, resp.headers, resp.body.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// TrafficResult holds the result of onTraffic execution.
// It can represent either a modified Request (IsResponse=false) or
// a direct Response for WAF-style blocking (IsResponse=true).
type TrafficResult struct {
	IsResponse bool              `json:"_isResponse"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Path       string            `json:"path"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}
