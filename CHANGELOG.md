# Changelog

All notable changes to the Sonic project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.1.3] - 2026-06-15

### Added
- Dynamic WebUI authorization token display in the startup console banner when configured token is empty.
- Multi-stage Docker build targeting Go 1.25.
- Persistence data directories (`/etc/sonic/data`) to Docker volume.

### Changed
- Standardized environment variables file template `.env.example`.
- Updated container orchestration `docker-compose.yml` to remove legacy properties and support full port binding for telemetry and WebUI.

### Fixed
- Replaced runtime panics during Web Standard API bootstrap within the VM pool with graceful Go errors returned to the application controller.
- Handled VM resource replenishment via non-panicking `recreateAndQueueVM` routine.
- Cleared unwanted certificates CA and outdated binaries from local workspace.

---

## [1.1.2] - 2026-06-15

### Added
- **Neural Cache Compression**: Zero-dependency implementation of LZ77 sliding window (4KB dictionary) and variable-length Huffman entropy encoding.
- Smart compression heuristics: Payloads smaller than 512 bytes are bypassed; only standard web formats (`text/*`, `application/json`, etc.) are compressed.
- Internal metrics tracking compression savings, ratio, and original size in Cache Stats.

---

## [1.1.1] - 2026-06-15

### Added
- **Web Security Hardening**: Integrated Strict HTTP Response Headers including Strict-Transport-Security (HSTS) and Content-Security-Policy (CSP) inside dynamic middleware.
- Cleaned query parameters containing secrets from URL logs.

---

## [1.1.0] - 2026-06-14

### Added
- **Security Hardening (Phase 2)**:
  - Input validation on JS dynamic engine routing (SSRF blocker).
  - WebUI transparent proxy connection limits (maximum 1020 concurrent connections).
  - Sanitization of CRLF injections in headers.
  - SQLite auditing buffer batching with `busy_timeout` configuration.
  - Native subprocess workers pool bounding.
  - API Keys encriptação local com cifras AES-GCM.

---

## [1.0.0] - 2026-06-14

### Added
- Initial Release of Sonic Engine.
- Multi-protocol, multi-language intercepting engine (JS Goja, WASM Wazero, Native subprocesses).
- Embedded SQLite storage with persistent Rate Limiting rules.
- Concurrent Sandbox Interceptor for dynamic payload breakpoints.
- AI Proxy Gateway with semantic local TF-IDF cache and OpenAI integration.
- Minimalist monocromatic WebUI dashboard.
- Windows Service support via SCM handler.
