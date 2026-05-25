# Sonic — Edge JavaScript Engine

**Sonic** is a transparent L7 proxy with edge JavaScript execution, powered by eBPF Sockmap acceleration and dynamic TLS MITM. Compatible with the **Cloudflare Workers API** — run your workers locally, at the network edge.

```
sonic — Self-hosted Cloudflare Workers alternative
✓ No vendor lock-in
✓ Deploy anywhere: VPS, Raspberry Pi, Docker, bare metal
✓ eBPF-accelerated kernel bypass (Linux)
✓ Drop-in: your CF Workers code runs unmodified
```

## Table of Contents

- [Quick Start](#quick-start)
- [CLI Usage](#cli-usage)
- [SDK / Library Usage](#sdk--library-usage)
- [Worker API](#worker-api)
- [Configuration](#configuration)
- [Docker](#docker)
- [Architecture](#architecture)
- [Deploy Anywhere](#deploy-anywhere)

## Quick Start

```bash
go install github.com/manassesbinga/sonic@latest

# Initialize a project
sonic init

# Start developing (hot-reload)
sonic dev

# Test a worker directly
sonic run hello --method POST --header "X-Test: value"

# Run in production
sonic start
```

## CLI Usage

| Command | Description |
|---------|-------------|
| `sonic init` | Initialize a new project |
| `sonic new <name>` | Create a new worker function |
| `sonic start` | Start proxy in production mode |
| `sonic dev` | Start with hot-reload |
| `sonic run <file>` | Execute a worker directly |
| `sonic list` | List available workers |
| `sonic info` | Show project & system info |
| `sonic ca install` | Generate Root CA |
| `sonic version` | Show version |

## SDK / Library Usage

Use Sonic as an embeddable Go library:

```go
import "github.com/manassesbinga/sonic/sdk"

// Start the proxy with a JS worker
s, err := sonic.New(sonic.Config{
    JSCode:    `function onTraffic(r) { return r; }`,
    ListenPort: 8443,
    PoolSize:   8,
})
defer s.Stop()
go s.StartBackground()

// Execute workers directly (no proxy)
result, err := s.RunWorker("GET", "https://example.com/",
    "Authorization: Bearer token123",
)
fmt.Printf("Modified headers: %v\n", result.Headers)
```

### Sonic struct

```go
func New(cfg Config) (*Sonic, error)
func LoadConfig(path string) (*Sonic, error)

func (s *Sonic) Start() error            // Blocking
func (s *Sonic) StartBackground() error  // Non-blocking
func (s *Sonic) Stop()                   // Graceful shutdown
func (s *Sonic) WaitForSignal()          // Wait for SIGINT/SIGTERM
func (s *Sonic) RunWorker(method, url string, headers ...string) (*runtime.TrafficResult, error)
func (s *Sonic) Config() *config.Config
func (s *Sonic) JSEngine() *runtime.JSEngine
```

### Config struct

```go
type Config struct {
    ListenPort int      // Proxy port (default: 8443)
    Mode       string   // "intercept" | "passthrough-all" | "observe"
    JSCode     string   // JavaScript worker source
    CADir      string   // CA certificate directory (default: "./certs")
    TimeoutMS  int      // JS execution timeout (default: 50ms)
    PoolSize   int      // VM pool size (default: 64)
    Failsafe   string   // "bypass" | "block" (default: "bypass")
}
```

### Sub-packages

Use sub-packages for finer control:

```go
import "github.com/manassesbinga/sonic/runtime"  // JS engine
import "github.com/manassesbinga/sonic/mitm"     // TLS MITM
import "github.com/manassesbinga/sonic/proxy"    // Transparent proxy
import "github.com/manassesbinga/sonic/config"   // Configuration
```

## Worker API

Workers use a **Cloudflare Workers-compatible** API:

```javascript
function onTraffic(request) {
    request.headers.set("X-Custom", "value");
    return request;
}

function onResponse(response) {
    response.headers.set("X-Processed", "yes");
    return response;
}
```

### Supported APIs

| API | Status |
|-----|--------|
| `Request` | Web Standard class |
| `Response` | Web Standard class |
| `Headers` | Proxy for direct property access |
| `fetch(url)` | Outbound HTTP via Go bridge |
| `log(msg)` | Go-native logging |
| `jwtVerify(token, secret)` | JWT verification |
| `require("module")` | Local modules from `./modules/` |
| `module.exports` | Node.js-style module exports |

### require() — Local Modules

Create reusable modules in `modules/`:

```javascript
// modules/uuid.js
module.exports = {
    v4: function() { /* ... */ }
};
```

```javascript
// functions/worker.js
var uuid = require("uuid");
request.headers.set("X-Request-ID", uuid.v4());
```

## Configuration

Config via `sonic.yaml` or environment variables (`SONIC_*`):

```yaml
listen_port: 8443
mode: "intercept"
runtime:
  timeout_ms: 50
  pool_size: 64
  failsafe: "bypass"
bypass_domains:
  - "*.stripe.com"
tls:
  ca_dir: "./certs"
  auto_generate: true
```

```bash
export SONIC_LISTEN_PORT=8443
export SONIC_MODE=intercept
sonic start
```

## Docker

```bash
docker compose up -d
docker compose logs -f
```

Or manually:

```bash
docker run -d --name sonic --network host \
  -v ./functions:/etc/sonic/functions \
  -v ./certs:/etc/sonic/certs \
  sonic:latest
```

## Architecture

```
Client ──► Transparent Proxy (:8443)
              ├─ SNI Extraction
              ├─ Bypass Check
              ├─ TLS MITM Termination
              ├─ JS onTraffic (edge worker)
              ├─ Re-encrypt to real server
              ├─ JS onResponse (edge worker)
              └─ Response to client
```

## Deploy Anywhere

| Platform | Instructions |
|----------|-------------|
| **Linux** | `make install` + `systemctl enable extras/sonic.service` |
| **Docker** | `docker compose up -d` |
| **Raspberry Pi** | `make release` → `dist/sonic-linux-arm64` |
| **macOS** | `make build` (dev only, no eBPF) |
| **Go app embed** | `import "github.com/manassesbinga/sonic/sdk"` |

## Makefile

```bash
make build       # Build binary
make test        # Run all tests
make run         # Start in production
make dev         # Start with hot-reload
make install     # Install to /usr/local/bin
make release     # Cross-compile all platforms
make docker      # Build Docker image
make docker-run  # Run via Docker Compose
```

## Why Sonic?

**vs Cloudflare Workers:**
- No vendor lock-in: run anywhere
- No cold starts: always-on Go runtime
- Custom TLS: full control over certificates
- eBPF acceleration: kernel-level performance
- Embeddable as Go library

**vs nginx + Lua:**
- JavaScript instead of Lua
- Cloudflare Workers API compatibility
- Dynamic TLS MITM built-in
- Hot-reload without restart

## License

MIT
