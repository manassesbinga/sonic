#!/usr/bin/env bash
# Example: Calling Sonic from shell scripts
#
# Patterns:
#   1. sonic run — pipe stdin, capture output
#   2. sonic list — check active workers
#   3. sonic start — run proxy in background

set -euo pipefail

FUNCTIONS_DIR="$(dirname "$0")/../../functions"

echo "=== Pattern 1: sonic list ==="
sonic list

echo ""
echo "=== Pattern 2: sonic run with JSON output (pipeable) ==="
sonic run "$FUNCTIONS_DIR/hello.js" \
  --json \
  --method GET \
  --url "https://example.com/" \
  --header "X-Test: shell-example" 2>/dev/null

echo ""
echo "=== Pattern 3: sonic run piped to jq ==="
sonic run "$FUNCTIONS_DIR/hello.js" \
  --json \
  --method GET \
  --url "https://httpbin.org/get" 2>/dev/null | jq . 2>/dev/null || \
  echo "(install jq for JSON pretty-printing)"

echo ""
echo "=== Pattern 4: Loop over URLs ==="
for url in "https://api.example.com/users" "https://api.example.com/orders"; do
  echo "--- $url ---"
  sonic run "$FUNCTIONS_DIR/hello.js" --json --method GET --url "$url" 2>/dev/null
done
