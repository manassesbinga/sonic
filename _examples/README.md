# Sonic SDK & Multi-Language Examples

## Go SDK Examples (embedded library)

| Example | Description |
|---------|-------------|
| `basic/main.go` | Start Sonic, run a worker, stop |
| `worker-only/main.go` | Use Sonic as JS engine (no proxy) |
| `custom-server/main.go` | Integrate Sonic with custom HTTP server (TODO) |

Run:
```bash
cd basic && go run main.go
```

## Multi-Language Examples (via `sonic run` CLI)

Sonic's `sonic run` command runs a worker and outputs JSON. Any language
that can execute a subprocess can use Sonic.

| Language | File | How it works |
|----------|------|-------------|
| **Python** | `python/run_worker.py` | `subprocess.run(["sonic", "run", ...])` |
| **Node.js** | `node/run_worker.js` | `child_process.execSync("sonic run ...")` |
| **Shell** | `shell/run_worker.sh` | Direct `sonic run` in bash |

Run:
```bash
python3 _examples/python/run_worker.py
node _examples/node/run_worker.js
bash _examples/shell/run_worker.sh   # requires jq for pretty-printing
```

### The `sonic run` API

```
sonic run <worker.js> \
  --method GET|POST|PUT|DELETE \
  --url <url> \
  --header "Key: Value" \
  --body '<json>' \
  --func onTraffic         # optional: function to run (default onTraffic)
```

Output: JSON with `method`, `url`, `path`, `headers`, `body`, `status`
(or raw JSON if the function returns `{_isResponse: true, ...}`).

### Using the proxy from other languages

Run Sonic as a background proxy, then point your app's HTTP client to it:

```bash
# Start proxy (background)
sonic start --port 8443 --functions ./functions/

# Or with Docker
docker run -d --network host \
  -v $(pwd)/functions:/functions \
  sonic:latest
```

Then configure your app to use `http://127.0.0.1:8443` as an HTTPS proxy.
