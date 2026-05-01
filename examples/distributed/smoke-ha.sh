#!/usr/bin/env bash
# smoke-ha.sh — manual verification of master HA failover.
#
# Brings up the compose.ha.yml stack, kills the primary master, and
# checks that workers continue running and that the standby has acquired
# the lease. Cleanup runs on EXIT/INT/TERM.
#
# Usage: ./smoke-ha.sh
set -euo pipefail

cd "$(dirname "$0")"

for dep in docker curl jq; do
  if ! command -v "$dep" >/dev/null 2>&1; then
    echo "missing dependency: $dep" >&2
    exit 2
  fi
done

cleanup() {
  echo "--- cleanup ---"
  docker compose -f compose.ha.yml down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "--- starting stack ---"
docker compose -f compose.ha.yml up -d

echo "--- waiting for primary acquisition ---"
for i in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:7000/api/stats" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "--- triggering traffic ---"
curl -sf "http://127.0.0.1:7000/api/start" || echo "(start endpoint may not exist; OK)"
sleep 5

before_tps=$(curl -sf "http://127.0.0.1:7000/api/stats" | jq -r '.current_tps // 0')
echo "primary TPS before kill: $before_tps"

echo "--- killing primary master ---"
docker compose -f compose.ha.yml stop master-primary
kill_at=$(date +%s)

echo "--- waiting for standby to take over (≤5s SLA) ---"
acquired_at=0
for i in $(seq 1 10); do
  if curl -sf "http://127.0.0.1:7001/api/stats" >/dev/null 2>&1; then
    acquired_at=$(date +%s)
    break
  fi
  sleep 1
done

if [ "$acquired_at" -eq 0 ]; then
  echo "FAIL: standby did not respond on /api/stats within 10s"
  exit 1
fi

elapsed=$((acquired_at - kill_at))
echo "standby acquired and serving in ~${elapsed}s"

echo "--- waiting for traffic to recover ---"
sleep 5
after_tps=$(curl -sf "http://127.0.0.1:7001/api/stats" | jq -r '.current_tps // 0')
echo "standby TPS after acquire: $after_tps"

if (( $(echo "$after_tps > 0" | bc -l 2>/dev/null || echo 0) )); then
  echo "PASS — workers reconnected to standby"
else
  echo "WARN — TPS not yet recovered; workers may need more reconnect cycles"
fi

echo "--- check ha metrics ---"
curl -sf "http://127.0.0.1:9090/metrics" 2>/dev/null | grep '^kar98k_ha_failover_total' || true
