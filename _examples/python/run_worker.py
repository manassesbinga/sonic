#!/usr/bin/env python3
"""Example: Calling Sonic from Python via subprocess.

Two patterns:
  1. `sonic run` — execute a worker ad-hoc and get the result
  2. `sonic start` — run proxy in background, use it from Python

Prerequisites:
  pip install requests    (for pattern 2)
  sonic must be in $PATH  (go install ./cmd/sonic or make install)
"""

import json
import subprocess
import sys
import time

WORKER_PATH = "../../functions/hello.js"

def pattern1_sonic_run():
    """Execute a worker ad-hoc with sonic run and capture output."""
    print("=== Pattern 1: sonic run ===")

    result = subprocess.run(
        [
            "sonic", "run", WORKER_PATH,
            "--json",
            "--method", "POST",
            "--url", "https://api.example.com/data",
            "--header", "Content-Type: application/json",
            "--header", "X-Block-Me: true",
            "--body", json.dumps({"user": "test"}),
        ],
        capture_output=True, text=True, timeout=10,
    )

    print("stdout:", result.stdout)
    print("stderr:", result.stderr.strip())
    print("return code:", result.returncode)

    # Parse the JSON output from stdout
    if result.stdout.strip():
        try:
            output = json.loads(result.stdout)
            print("Parsed response status:", output.get("status"))
            if output.get("headers"):
                print("Headers:", output["headers"])
        except json.JSONDecodeError:
            print("Output is not JSON")


def pattern2_docker_proxy():
    """Run Sonic as a Docker proxy and send traffic through it."""
    print("\n=== Pattern 2: Docker proxy ===")

    import requests  # noqa: F811

    # Start Sonic in Docker (assuming sonic:latest image)
    subprocess.run(
        [
            "docker", "run", "-d", "--name", "sonic-proxy",
            "--network", "host",
            "-v", f"{WORKER_PATH}:/functions/worker.js",
            "sonic:latest",
        ],
        check=False,  # may fail if already running
    )

    time.sleep(1)

    try:
        # Send a request THROUGH the proxy (not to it directly)
        # The proxy intercepts outbound TLS connections.
        # For testing, tell requests to trust the Sonic CA.
        resp = requests.get(
            "https://httpbin.org/get",
            proxies={"https": "http://127.0.0.1:8443"},
            verify=False,  # Sonic's self-signed CA
            timeout=5,
        )
        print("Proxy response:", resp.status_code)
        print("X-Sonic headers:", {
            k: v for k, v in resp.headers.items()
            if "sonic" in k.lower()
        })
    finally:
        subprocess.run(["docker", "rm", "-f", "sonic-proxy"], check=False)


if __name__ == "__main__":
    pattern1_sonic_run()

    if "--docker" in sys.argv:
        pattern2_docker_proxy()
