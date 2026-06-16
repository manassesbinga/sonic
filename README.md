# Sonic — Multi-Language, Multi-Protocol Edge Engine
#### Video Demo:  https://youtu.be/PzQM_MJUqRk
#### Description:

Sonic is a self-hosted alternative to Cloudflare Workers designed to process logic over any network traffic at the edge. Written entirely in Go, the engine combines high performance, multi-language support, and protocol-agnostic intercepting. It transforms the concept of an edge router into a programmable gateway, running lightweight functions locally without vendor lock-in.

At its core, Sonic terminates TLS connections, executes user-defined middleware logic in various engines (JavaScript via Goja VM, WebAssembly via Wazero, and native subprocesses), and proxies the modified payload to upstream servers. It achieves ultra-low latencies and high throughput by leveraging a hot VM pooling architecture, avoiding the standard cold starts associated with traditional container-based or serverless runtimes.

---

### Project Architecture & File Contents

To understand the complexity and modular design of Sonic, here is a detailed description of the components and the files written for the project:

#### 1. Command Line Interface (`cli/`)
* **`cli/root.go`**: Contains the command-line setup using standard flags. It initializes the logging system, loads the core configuration YAML file, handles OS signal catching (like SIGINT or SIGTERM for graceful shutdowns), and spins up the proxy engine.
* **`cli/banner.go`**: Responsible for rendering the stylized monocromatic console banner upon startup, printing helpful information like the loaded port, the active mode, the path to the SQLite database, and the WebUI authorization token.

#### 2. Configuration System (`config/`)
* **`config/config.go`**: Defines the configurations structures mapping options from `sonic.yaml` or environment variables (using Viper). Options include network ports, operational modes (`intercept`, `passthrough-all`, `observe`), rate limiting rules, and custom runtime timeouts.

#### 3. Transparent L7 Proxy (`proxy/`)
* **`proxy/transparent.go`**: The core TCP listener that intercepts incoming packets. It reads the initial bytes of a connection to parse the Server Name Indication (SNI) from TLS ClientHellos without consuming the stream, deciding whether a domain should be decrypted (MITM) or bypassed (Passthrough).
* **`proxy/connection.go`**: Implements custom connection buffering structures (`BufferedConn`) that allow peeking into raw bytes of active sockets.

#### 4. TLS Man-in-the-Middle (`mitm/`)
* **`mitm/engine.go`**: Coordinates the TLS interception logic. It dynamically terminates client TLS sessions using on-the-fly generated Leaf certificates signed by a local Root Certificate Authority (CA) and opens a secure upstream connection to the real server.
* **`mitm/ca.go`**: Handles local filesystem certificate generation, managing the secure storage and loading of the master Root CA.
* **`mitm/cert_cache.go`**: A concurrent-safe memory cache utilizing Read-Write Mutexes to reuse previously generated Leaf certificates, drastically reducing TLS handshake times.

#### 5. Extensible Runtime Engines (`runtime/`)
* **`runtime/js_runtime.go`**: Manages the JavaScript Goja VM pool. It pre-warms a sync.Pool of Goja runtimes to achieve zero cold starts. It also implements the CPU execution watchdog that interrupts runaway infinite loops in JS scripts.
* **`runtime/wasm_engine.go`**: An engine wrapper around Wazero to load compiled WebAssembly binaries (Rust, Go, C) at the edge, utilizing a lease mechanism to wait for available modules under concurrent loads.
* **`runtime/bootstrap.go`**: A virtual polyfill bootstrap that injects web standards like `Request`, `Response`, `Headers`, `fetch()`, and `kv` (BBolt wrapper) into the JavaScript environment.

#### 6. Database and Storage (`neuralcache/` and `security/`)
* **`neuralcache/`**: Implements a local semantic TF-IDF caching layer for LLM (Large Language Model) gateways. It converts text queries into similarity representations to match cached answers without token costs.
* **`security/aes.go`**: Encrypts API keys and sensitive SQLite audits using local AES-GCM encryption.

#### 7. Administrative Console (`webui/`)
* **`webui/server.go`**: An embedded Go HTTP server exposing REST APIs to manage workers, view telemetry graphs in real-time via Server-Sent Events (SSE), configure visual execution pipelines, and tampered frozen payloads in the Interactive Sandbox.
* **`webui/frontend/`**: The modern AMOLED Pure Black HTML/JS frontend application communicating with the REST API.

---

### Design Decisions & Engineering Trade-offs

During the development of Sonic, several complex design choices were evaluated to maximize performance and security:

1. **Goja VM vs. Node/V8 Subprocess**:
   Instead of launching external Node.js engines or linking heavy C++ V8 runtimes, we chose **Goja**, a pure Go JavaScript engine. Goja allows us to eliminate the overhead of CGO cross-language boundaries, maintaining all execution state inside Go’s memory space. To overcome Goja's lack of native multi-threading, we implemented a concurrent **VM Pool (sync.Pool)** of 64 pre-warmed instances, allowing concurrent requests to run Javascript scripts in parallel without CPU starvation.

2. **Watchdog Interruption using Contexts**:
   Goja's standard `Interrupt()` method stops script execution but cannot stop native Go bridge calls (like outbound HTTP `fetch()` requests) if they block or suffer high network latency. To fix this vulnerability, we implemented context timeout tracking (`__current_ctx`). Before any execution, a context with a timeout is bound to the running routine. When the timeout expires, the context is cancelled, automatically terminating blocked native HTTP connections and releasing resources.

3. **Embedded BBolt KV Store**:
   For sharing state between different language runtimes, we integrated an embedded **BBolt database**. Since BBolt is a single-file transactional key-value store in pure Go, it requires zero external dependencies, making Sonic entirely self-contained and easy to deploy on any server or Docker environment.

4. **WASM VM Lease Pool**:
   Compiling and loading WebAssembly modules is highly CPU and RAM intensive. Under heavy load, compiling on-demand WASM modules could crash the host. We designed a lease queue where incoming requests wait for a free module from the pool before compiling raw code, capping on-demand instances to protect system stability.
