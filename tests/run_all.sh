#!/usr/bin/env bash
# Sonic — Comprehensive Test Suite
# Runs all tests, benchmarks, and saves results to tests/results/
set -euo pipefail

RESULTS="tests/results/$(date +%Y%m%d_%H%M%S)"
mkdir -p "$RESULTS"

echo "============================================" | tee -a "$RESULTS/summary.txt"
echo " Sonic Comprehensive Test Suite"             | tee -a "$RESULTS/summary.txt"
echo " Started: $(date)"                           | tee -a "$RESULTS/summary.txt"
echo "============================================" | tee -a "$RESULTS/summary.txt"

# ── 1. Unit Tests ──────────────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "[1/6] Running Unit Tests..." | tee -a "$RESULTS/summary.txt"

go test ./... -count=1 -timeout 120s -v 2>&1 | tee "$RESULTS/unit_tests.txt"
UNIT_EXIT=${PIPESTATUS[0]}

UNIT_PASS=$(grep -c "PASS:" "$RESULTS/unit_tests.txt" || true)
UNIT_FAIL=$(grep -c "FAIL:" "$RESULTS/unit_tests.txt" || true)
UNIT_TOTAL=$((UNIT_PASS + UNIT_FAIL))
echo "Unit tests: $UNIT_PASS passed / $UNIT_TOTAL total" | tee -a "$RESULTS/summary.txt"

# ── 2. Race Detection ──────────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "[2/6] Running Race Detection..." | tee -a "$RESULTS/summary.txt"

go test -race ./runtime/... ./config/... ./mitm/... ./proxy/... -count=1 -timeout 120s 2>&1 | tee "$RESULTS/race_tests.txt"
RACE_EXIT=${PIPESTATUS[0]}

if grep -q "WARNING: DATA RACE" "$RESULTS/race_tests.txt"; then
    echo "⚠ DATA RACE DETECTED!" | tee -a "$RESULTS/summary.txt"
    RACE_COUNT=$(grep -c "WARNING: DATA RACE" "$RESULTS/race_tests.txt" || true)
    echo "Races: $RACE_COUNT" | tee -a "$RESULTS/summary.txt"
else
    echo "No data races detected" | tee -a "$RESULTS/summary.txt"
fi

# ── 3. Benchmarks ──────────────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "[3/6] Running Benchmarks..." | tee -a "$RESULTS/summary.txt"

go test ./runtime/... -bench=. -benchmem -count=3 -timeout 120s 2>&1 | tee "$RESULTS/benchmarks.txt"
BENCH_EXIT=${PIPESTATUS[0]}

# Extract benchmark results
grep "Benchmark" "$RESULTS/benchmarks.txt" | grep -v "^go:" | tee "$RESULTS/benchmarks_summary.txt" || true
BENCH_COUNT=$(grep -c "Benchmark" "$RESULTS/benchmarks_summary.txt" || true)
echo "Benchmarks: $BENCH_COUNT results" | tee -a "$RESULTS/summary.txt"

# ── 4. Integration Tests ────────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "[4/6] Running Integration Tests..." | tee -a "$RESULTS/summary.txt"

go test -run "TestIntegration" ./tests/... -count=1 -timeout 120s -v 2>&1 | tee "$RESULTS/integration_tests.txt"
INT_EXIT=${PIPESTATUS[0]}

INT_PASS=$(grep -c "PASS:" "$RESULTS/integration_tests.txt" || true)
INT_FAIL=$(grep -c "FAIL:" "$RESULTS/integration_tests.txt" || true)
echo "Integration tests: $INT_PASS passed / $((INT_PASS + INT_FAIL)) total" | tee -a "$RESULTS/summary.txt"

# ── 5. Compatibility Tests ──────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "[5/6] Running Cloudflare Workers Compatibility Tests..." | tee -a "$RESULTS/summary.txt"

go test -run "TestCompatibility" ./tests/... -count=1 -timeout 120s -v 2>&1 | tee "$RESULTS/compatibility_tests.txt"
COMP_EXIT=${PIPESTATUS[0]}

COMP_PASS=$(grep -c "PASS:" "$RESULTS/compatibility_tests.txt" || true)
COMP_FAIL=$(grep -c "FAIL:" "$RESULTS/compatibility_tests.txt" || true)
echo "Compatibility: $COMP_PASS passed / $((COMP_PASS + COMP_FAIL)) total" | tee -a "$RESULTS/summary.txt"

# ── 6. Stress Tests ─────────────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "[6/6] Running Stress Tests..." | tee -a "$RESULTS/summary.txt"

go test -run "TestStress" ./tests/... -count=1 -timeout 120s -v 2>&1 | tee "$RESULTS/stress_tests.txt"
STRESS_EXIT=${PIPESTATUS[0]}

STRESS_PASS=$(grep -c "PASS:" "$RESULTS/stress_tests.txt" || true)
STRESS_FAIL=$(grep -c "FAIL:" "$RESULTS/stress_tests.txt" || true)
echo "Stress: $STRESS_PASS passed / $((STRESS_PASS + STRESS_FAIL)) total" | tee -a "$RESULTS/summary.txt"

# ── Summary ──────────────────────────────────────────────
echo "" | tee -a "$RESULTS/summary.txt"
echo "============================================" | tee -a "$RESULTS/summary.txt"
echo " TEST SUITE COMPLETE"                       | tee -a "$RESULTS/summary.txt"
echo " Finished: $(date)"                         | tee -a "$RESULTS/summary.txt"
echo "============================================" | tee -a "$RESULTS/summary.txt"
echo "" | tee -a "$RESULTS/summary.txt"
echo "Results saved to: $RESULTS" | tee -a "$RESULTS/summary.txt"

# Check exit codes
ALL_OK=true
for name in UNIT RACE BENCH INT COMP STRESS; do
    exit_var="${name}_EXIT"
    if [ "${!exit_var}" -ne 0 ]; then
        echo "⚠ $name tests FAILED" | tee -a "$RESULTS/summary.txt"
        ALL_OK=false
    fi
done

if [ "$ALL_OK" = true ]; then
    echo "" | tee -a "$RESULTS/summary.txt"
    echo " ALL TESTS PASSED ✓" | tee -a "$RESULTS/summary.txt"
    exit 0
else
    echo "" | tee -a "$RESULTS/summary.txt"
    echo " SOME TESTS FAILED ✗" | tee -a "$RESULTS/summary.txt"
    exit 1
fi
