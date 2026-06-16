#!/usr/bin/env python
# -*- coding: utf-8 -*-
# functions/examples/anonymizer.py — Sonic Native Edge Worker (Python)
#
# Runs as a persistent background process.
# Protocol: Reads JSON packet from stdin, processes it, and writes JSON result to stdout.
# Logs printed to stderr or printed as non-JSON string to stdout are caught by Sonic.

import sys
import json

def log(msg):
    # Print to stdout without { } so the Go runtime captures it as a worker log
    print(f"[Python Anonymizer] {msg}")
    sys.stdout.flush()

log("Python Edge Anonymizer initialized successfully.")

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        packet = json.loads(line)
        # Check if HTTP request packet
        if packet.get("protocol") in ["http", "https"] and packet.get("request"):
            req = packet["request"]
            headers = req.get("headers", {})
            
            # Anonymize: Strip headers that disclose client metadata
            to_remove = ["x-forwarded-for", "client-ip", "user-agent", "referer", "x-real-ip"]
            stripped = []
            for h in list(headers.keys()):
                if h.lower() in to_remove:
                    del headers[h]
                    stripped.append(h)
            
            # Add anonymized routing header
            headers["X-Anonymized-By"] = "Sonic Python Anonymizer Worker"
            
            if stripped:
                log(f"Stripped client metadata: {', '.join(stripped)}")
            
        # Write back packet result in single JSON line
        result = {
            "allow": True,
            "packet": packet
        }
        sys.stdout.write(json.dumps(result) + "\n")
        sys.stdout.flush()
    except Exception as e:
        log(f"Error processing packet: {str(e)}")
        # Failsafe fallback: allow packet to bypass unaltered
        sys.stdout.write(json.dumps({"allow": True}) + "\n")
        sys.stdout.flush()
