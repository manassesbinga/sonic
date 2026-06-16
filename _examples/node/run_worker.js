#!/usr/bin/env node
/**
 * Example: Calling Sonic from Node.js via child_process.
 *
 * Two patterns:
 *   1. `sonic run` — execute a worker ad-hoc
 *   2. Use Sonic proxy from Node.js (via HTTP agent)
 *
 * Prerequisites:
 *   sonic must be in $PATH
 */

const { execSync, spawn } = require("child_process");
const path = require("path");

const WORKER = path.resolve(__dirname, "../../functions/hello.js");

function pattern1_sonicRun() {
  console.log("=== Pattern 1: sonic run ===");

  try {
    const stdout = execSync(
      `sonic run "${WORKER}" ` +
      `--json ` +
      `--method POST ` +
      `--url https://api.example.com/data ` +
      `--header "Content-Type: application/json" ` +
      `--header "X-Block-Me: true" ` +
      `--body '{"user":"test"}'`,
      { encoding: "utf-8", timeout: 10000 }
    );
    console.log("stdout:", stdout);
  } catch (err) {
    console.error("sonic run failed:", err.stderr || err.message);
  }
}

function pattern2_sonicProxy() {
  console.log("\n=== Pattern 2: Start Sonic proxy from Node ===");

  const proc = spawn("sonic", ["start", "--port", "8443"], {
    stdio: "pipe",
    detached: false,
  });

  proc.stdout.on("data", (d) => process.stdout.write(`[sonic] ${d}`));
  proc.stderr.on("data", (d) => process.stderr.write(`[sonic] ${d}`));

  // Wait for proxy to be ready
  setTimeout(() => {
    // Send request through the proxy
    const https = require("https");
    const options = {
      hostname: "httpbin.org",
      port: 443,
      path: "/get",
      method: "GET",
      rejectUnauthorized: false, // Sonic self-signed CA
      headers: { "Host": "httpbin.org" },
      // Tunnel through Sonic proxy
      createConnection: () => {
        const net = require("net");
        const socket = net.connect(8443, "127.0.0.1");
        return socket;
      },
    };

    const req = https.request(options, (res) => {
      let data = "";
      res.on("data", (chunk) => (data += chunk));
      res.on("end", () => {
        console.log("Response status:", res.statusCode);
        console.log("X-Sonic headers:", res.headers["x-sonic-proxy"]);
        proc.kill();
      });
    });
    req.end();
  }, 2000);
}

pattern1_sonicRun();
// pattern2_sonicProxy(); // uncomment to test proxy (needs root/capabilities)
