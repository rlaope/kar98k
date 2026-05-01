# Distributed mode

kar supports a master/worker mode that fans a single test plan across multiple machines, breaking through the ~50K TPS ceiling of a single process.

## Architecture

```
kar master                  kar worker  (3-10 instances)
┌───────────────┐           ┌──────────────────┐
│  controller   │──SetRate──▶  pool (local)     │
│  (pattern +   │◀──Stats───│  health checker   │
│   scheduler)  │           │  job loop         │
│  gRPC server  │           └──────────────────┘
│  dashboard    │
└───────────────┘
     :7000 (HTTP)
     :7777 (gRPC)
```

- **Master** runs the full controller (pattern engine, scheduler, scenarios) but performs no local request execution. It broadcasts a per-worker TPS slice via a server-streaming gRPC call every 100 ms.
- **Workers** run a local `worker.Pool` + health checker + job submission loop. They receive TPS targets from master and push HdrHistogram snapshots back every 2 s.
- Master merges all worker histograms to compute global P95/P99.

## Quick start

```bash
# Terminal 1 — master
kar master --listen :7777 --config configs/kar98k.yaml

# Terminal 2+ — workers (repeat on each machine)
kar worker --master <master-host>:7777 --worker-addr <this-host>:9000
```

The master dashboard at `http://localhost:7000` shows global TPS, latency percentiles, and a per-worker table updated every 2 s.

## Docker Compose example (1 master + 3 workers + echoserver)

```bash
cd examples/distributed
docker-compose up --build
# In another terminal:
curl http://localhost:7000/api/workers   # 3 rows
curl http://localhost:7000/api/stats     # global TPS ~= base_tps
```

Run the automated smoke test:

```bash
examples/distributed/smoke.sh
```

## Configuration

No new top-level keys are required for single-process mode. Distributed mode uses the same `configs/kar98k.yaml`. Targets are propagated from the master to all workers via `RegisterResp`.

Flags:

| Command | Flag | Default | Description |
|---|---|---|---|
| `kar master` | `--listen` | `:7777` | gRPC listen address |
| `kar master` | `--config` | `kar98k.yaml` | Config file path |
| `kar worker` | `--master` | — | Master gRPC address (required) |
| `kar worker` | `--worker-addr` | `hostname:9000` | Self-address sent to master at registration |

## Rate distribution

Master divides `target_tps / N` (N = live workers) and pushes every 100 ms (matching the controller tick). Workers that miss a tick use the last known rate; the next tick corrects.

## Worker disconnect / drain

When a worker receives a `DRAIN` command (e.g. `Ctrl-C`), it stops accepting new jobs, drains in-flight requests within 10 s, pushes a final stats snapshot, and exits. Master evicts stale workers (last heartbeat > 5 s) and redistributes TPS to remaining workers within the next tick.

## Known limitations (v1 Lean MVP)

- **Single point of failure**: master crash ends the run. Restart master and re-start workers. Follow-up issue: master HA via active/passive standby.
- **No mTLS**: plaintext gRPC only. Deploy inside a trusted VPC. Follow-up issue: mTLS + auth tokens.
- **Scenario propagation**: workers receive raw TPS only; phase names and inject curves are not forwarded. Follow-up issue: scenario/phase propagation.
- **No Prometheus per-worker labels**: all worker metrics share a single label set. Follow-up issue: per-worker Prometheus labels.
- **Hot-add**: works opportunistically (master redistributes on the next tick after registration) but is not benchmarked as a formal acceptance criterion.
