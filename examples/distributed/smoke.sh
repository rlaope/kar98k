#!/usr/bin/env bash
# Smoke test: start distributed stack, trigger a run, assert global TPS
# within 5% of target and 3 worker rows visible.
set -euo pipefail

COMPOSE="docker-compose -f $(dirname "$0")/docker-compose.yml"
MASTER_API="http://localhost:7000"
TARGET_TPS=30   # must match base_tps in configs/kar98k.yaml for the smoke

echo "==> Bringing up distributed stack..."
$COMPOSE up -d --build

echo "==> Waiting for master dashboard..."
for i in $(seq 1 30); do
  if curl -sf "$MASTER_API/api/stats" > /dev/null 2>&1; then
    break
  fi
  sleep 2
done

echo "==> Triggering run..."
curl -sf "$MASTER_API/api/start" > /dev/null || true

echo "==> Letting traffic flow for 30s..."
sleep 30

echo "==> Sampling /api/stats..."
STATS=$(curl -sf "$MASTER_API/api/stats")
echo "$STATS" | python3 -c "
import sys, json, math
d = json.load(sys.stdin)
tps = d.get('rps', 0)
target = $TARGET_TPS
pct = abs(tps - target) / target * 100
print(f'  global TPS: {tps:.1f} (target {target}, diff {pct:.1f}%)')
if pct > 5:
    sys.exit(f'FAIL: TPS diff {pct:.1f}% > 5%')
print('  PASS: TPS within 5%')
"

echo "==> Sampling /api/workers..."
WORKERS=$(curl -sf "$MASTER_API/api/workers")
COUNT=$(echo "$WORKERS" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))")
echo "  worker rows: $COUNT"
if [ "$COUNT" -ne 3 ]; then
  echo "FAIL: expected 3 worker rows, got $COUNT"
  $COMPOSE down
  exit 1
fi
echo "  PASS: 3 workers visible"

echo "==> Tearing down..."
$COMPOSE down
echo "==> Smoke test PASSED"
