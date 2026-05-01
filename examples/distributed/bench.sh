#!/usr/bin/env bash
# bench.sh — hot-add benchmark (manual, supplementary).
# Primary AC for #73 is the Go integration test TestHotAddRebalance.
# This script runs the same scenario end-to-end over real docker-compose
# networking and prints a human-readable pass/fail report.
#
# Usage: ./examples/distributed/bench.sh
# Requirements: docker, docker compose, jq, curl, awk
set -euo pipefail

for cmd in docker jq curl awk; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: '$cmd' not found in PATH" >&2; exit 1; }
done

COMPOSE_FILE="$(dirname "$0")/docker-compose.yml"
COMPOSE="docker compose -f $COMPOSE_FILE"
MASTER_API="http://localhost:7000"
TOL=0.10  # 10% tolerance for real-network variance

FAILED=0
pass() { echo "  PASS: $*"; }
fail() { echo "  FAIL: $*"; FAILED=1; }

cleanup() { $COMPOSE down -v 2>/dev/null || true; }
trap cleanup EXIT INT TERM

wait_api() {
  echo "==> Waiting for master dashboard..."
  for i in $(seq 1 30); do
    if curl -sf "$MASTER_API/api/stats" > /dev/null 2>&1; then return; fi
    sleep 2
  done
  echo "ERROR: master API not reachable after 60s" >&2
  exit 1
}

assert_per_worker() {
  local label="$1"
  local expected="$2"
  local workers_json
  workers_json=$(curl -sf "$MASTER_API/api/workers")
  local count
  count=$(echo "$workers_json" | jq 'length')
  echo "  worker count: $count"
  while IFS= read -r tps; do
    local lo hi
    lo=$(echo "$expected $TOL" | awk '{printf "%.2f", $1*(1-$2)}')
    hi=$(echo "$expected $TOL" | awk '{printf "%.2f", $1*(1+$2)}')
    if awk "BEGIN{exit !($tps >= $lo && $tps <= $hi)}"; then
      pass "$label worker TPS=$tps (expected ~$expected ±$(echo "$TOL*100" | awk '{printf "%.0f", $1}')%)"
    else
      fail "$label worker TPS=$tps outside [$lo, $hi]"
    fi
  done < <(echo "$workers_json" | jq '.[].current_tps')
}

echo "==> Starting distributed stack (3 workers)..."
$COMPOSE up -d --build
wait_api

echo "==> Triggering run..."
curl -sf "$MASTER_API/api/start" > /dev/null || true
echo "==> Letting traffic flow for 30s (3 workers, target TPS split ~333 each)..."
sleep 30

echo "==> Phase 1: 3-worker baseline"
assert_per_worker "3-worker" 333

echo "==> Stopping worker3 (hot-remove)..."
$COMPOSE stop worker3
echo "==> Settling for 30s (2 workers, expect ~500 each)..."
sleep 30

echo "==> Phase 2: 2-worker after hot-remove"
assert_per_worker "2-worker" 500

echo "==> Restarting worker3 (hot-add)..."
$COMPOSE start worker3
echo "==> Settling for 30s (3 workers again, expect ~333 each)..."
sleep 30

echo "==> Phase 3: 3-worker after hot-add"
assert_per_worker "3-worker-readd" 333

if [ "$FAILED" -eq 0 ]; then
  echo "==> bench-distributed PASSED"
else
  echo "==> bench-distributed FAILED (see above)" >&2
  exit 1
fi
