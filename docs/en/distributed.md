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

- **Single point of failure** (default): master crash ends the run unless HA is enabled. The lean default is k8s/systemd `restartPolicy: Always` + worker reconnect (#69) — that handles same-host restarts in seconds. Cross-host failover requires opt-in HA; see "High Availability (Master HA)" below.
- **No mTLS**: plaintext gRPC only. Deploy inside a trusted VPC. Follow-up issue: mTLS + auth tokens.
- **Inject curves not propagated**: scenario *phase names* now flow master → worker (see "Scenarios in distributed mode" below), but the inject-curve sampler still runs only on the master. Follow-up issue: full inject-curve propagation.
- **No Prometheus per-worker labels**: all worker metrics share a single label set. Follow-up issue: per-worker Prometheus labels.
- **Hot-add**: works opportunistically (master redistributes on the next tick after registration). See the Hot-add benchmark section below for acceptance criteria and how to run the bench.

## Scenarios in distributed mode

When the master config declares `scenarios:`, each phase boundary is propagated to every worker via the `RateUpdate` stream. The flow is:

1. Master's `ScenarioRunner.applyPhase(idx)` records the new phase name on `WorkerRegistry.SetPhase(name)`.
2. The next `SetRate` broadcast (≤100 ms later) tags `pb.RateUpdate{phase_name = name}` to every live worker.
3. Each worker compares the tag to its local `pool.CurrentPhase()`. On a mismatch it calls `pool.SnapshotAndAdvancePhase(newPhase)` — atomic snapshot+reset+flip under `latMu` — and emits an *out-of-band* `StatsPush{phase_name = prevPhase}` so the master attributes those samples to the phase that produced them.
4. Master's `WorkerRegistry.RecordStats` keys per-phase HdrHistogram aggregates by `phase_name`. Re-entering a previously seen phase **merges into the existing histogram** (does NOT reset) — matching solo `internal/script/phase.go:46-50` name-only re-entry semantics. The dashboard's `LatencyPercentileByPhase` and `PhaseSnapshot` surfaces read these aggregates.

Workers do **not** execute scenario logic — they have no schedule, no inject curve, no phase timer. They only follow target TPS and the phase tag the master attaches to each `RateUpdate`. v1 workers (no `phase_name` field) merge into the default `""` bucket, which keeps backwards compatibility.

## High Availability (Master HA)

The Phase-1 HA design (#72) lets you run a primary + standby master against a coordination store so a primary crash leaves the standby acquiring the lease in **≤3 seconds** without operator intervention. Phase 2 (#74) adds histogram tail-streaming so the standby preserves percentile continuity across failover.

### The "floor" — what HA is NOT for

For most deployments you do NOT need this: a `restartPolicy: Always` (k8s) or `Restart=always` (systemd) **plus** the #69 worker reconnect already gives you same-host failure recovery in a few seconds. Use HA only when you need cross-host failover — i.e. the failure mode is "the entire box is gone", not just "the master process crashed".

### HAStore backends

`HAStore` is a pluggable interface implementing the Kleppmann fencing-token pattern. A holder that loses the lease cannot accidentally write under the old fence — fence values are strictly monotonic across the store's lifetime, including across primary restarts.

| Backend | Build | Use for |
|---|---|---|
| `none` (default) | always | HA disabled — restart-policy + reconnect floor only |
| `memory` | always | tests, demos — same SPOF as a single master |
| `etcd` | `-tags ha_etcd` | production HA across hosts |

```bash
# Default build excludes etcd:
go build ./...

# Production build with etcd backend:
go build -tags ha_etcd ./...
```

There is **no file-based backend** by design — files cannot provide fencing across a network partition.

### Running primary + standby

```bash
# Primary
kar master --config kar.yaml \
  --listen :7777 \
  --ha-store etcd --ha-id primary-1 \
  --ha-endpoints http://etcd-1:2379,http://etcd-2:2379,http://etcd-3:2379 \
  --ha-ttl 5s

# Standby (same flags except --ha-id)
kar master --config kar.yaml \
  --listen :7777 \
  --ha-store etcd --ha-id standby-1 \
  --ha-endpoints http://etcd-1:2379,http://etcd-2:2379,http://etcd-3:2379 \
  --ha-ttl 5s
```

Workers dial both via `--master-standby`:

```bash
kar worker --master primary.host:7777 --master-standby standby.host:7777 ...
```

The reconnect loop (#69) cycles between the two addresses on each attempt so failover is automatic.

### Operator failover

```bash
# Graceful release — any standby acquires within ~LeaseTTL.
kar master failover --ha-store etcd --ha-endpoints ... --target ""

# Forced transfer to a specific holder.
kar master failover --ha-store etcd --ha-endpoints ... --target standby-1
```

Both paths write `last_transferred_by` + `last_transferred_at` audit metadata to the lease value.

### Self-fence guarantee

When a primary's `RenewLease` fails, the master self-fences via `grpcServer.GracefulStop()` within **1 s**, and the listener stops accepting connections within **1.5 s**. This is enforced by `TestGRPCServer_SelfFenceOnLeaseLoss`.

### Demo stack (dev only)

`examples/distributed/compose.ha.yml` brings up a single-node etcd + 1 primary + 1 standby + 2 workers + an echoserver. **Single-node etcd is for dev/CI only.** Production deployments need a 3- or 5-member etcd cluster with fsync-safe storage; otherwise you have replaced the master SPOF with an etcd SPOF.

`examples/distributed/smoke-ha.sh` validates the failover path: starts the stack, kills the primary, asserts the standby acquires within ~5 s.

### Honest about the percentile gap

Phase 1's standby starts with an empty HdrHistogram, so the **first few seconds of post-failover P95/P99** reflect samples from only the new master. The `kar98k_ha_failover_total` counter records every failover event; the `kar98k_ha_failover_percentile_gap_ms` gauge is reserved for **Phase 2 (#74)**, which streams histogram aggregates to the standby and updates this gauge with the bounded staleness it observed. In Phase 1 the gauge stays at 0 because there is no replica to measure against.

## Hot-add benchmark

Adding or removing a worker while a run is in progress causes `WorkerRegistry.SetRate` to redistribute `total_tps / N` across the new worker count within one controller tick (100 ms).

### Acceptance criteria

The formal acceptance criterion is the Go integration test `TestHotAddRebalance` in `internal/rpc/integration_test.go`. It:

- Spins up 2 in-process workers, drives `SetRate(900)`, and asserts each worker receives `450 ± 5%` averaged over 5 consecutive ticks.
- Hot-adds a 3rd worker and asserts each receives `300 ± 5%` after one settle interval.
- Unregisters the 3rd worker and asserts each of the remaining two receives `450 ± 5%` again.
- Completes in ≤ 2 s wall time using a 100 ms stats interval injected via `WithStatsIntervalMs(100)`.

Run it as part of the normal test suite:

```bash
go test -race ./internal/rpc/... -run TestHotAddRebalance -count=3 -timeout 30s
```

### Supplementary end-to-end bench (manual, not a CI gate)

`examples/distributed/bench.sh` runs the same add/remove scenario over real docker-compose networking. It is **not** a CI gate — docker-compose timing makes it impractical to run automatically, and the Go integration test covers the same logic deterministically.

```bash
make bench-distributed
# equivalent to:
./examples/distributed/bench.sh
```

Expected output (successful run):

```
==> Starting distributed stack (3 workers)...
==> Waiting for master dashboard...
==> Triggering run...
==> Letting traffic flow for 30s (3 workers, target TPS split ~333 each)...
==> Phase 1: 3-worker baseline
  worker count: 3
  PASS: 3-worker worker TPS=331.2 (expected ~333 ±10%)
  PASS: 3-worker worker TPS=334.7 (expected ~333 ±10%)
  PASS: 3-worker worker TPS=332.1 (expected ~333 ±10%)
==> Stopping worker3 (hot-remove)...
==> Settling for 30s (2 workers, expect ~500 each)...
==> Phase 2: 2-worker after hot-remove
  worker count: 2
  PASS: 2-worker worker TPS=498.8 (expected ~500 ±10%)
  PASS: 2-worker worker TPS=501.3 (expected ~500 ±10%)
==> Restarting worker3 (hot-add)...
==> Settling for 30s (3 workers again, expect ~333 each)...
==> Phase 3: 3-worker after hot-add
  worker count: 3
  PASS: 3-worker-readd worker TPS=330.5 (expected ~333 ±10%)
  PASS: 3-worker-readd worker TPS=335.1 (expected ~333 ±10%)
  PASS: 3-worker-readd worker TPS=332.9 (expected ~333 ±10%)
==> Tearing down...
==> bench-distributed PASSED
```

### Interpreting failures

A TPS divergence > 5% at steady-state (after one full stats interval) indicates the rate distributor regressed. Check `WorkerRegistry.SetRate` in `internal/rpc/registry.go` — specifically the `total / float64(n)` division and the non-blocking channel send. A blocked `sendCh` (buffer full) will cause one worker to miss a tick and appear low; the next tick self-corrects, so sustained divergence points to a logic error rather than a timing fluke.

---

## TLS + auth tokens

> **Warning:** The `compose.tls.yml` example uses self-signed certs in `examples/distributed/tls-local/`. For production, replace with real certs from your CA. **Never use `internal/rpc/testdata/` certs in any deployed environment** — those keys are publicly available in the repository and provide no security.

### TLS quickstart (self-signed, dev only)

```bash
# Generate a fresh self-signed cert pair in examples/distributed/tls-local/
make tls-quickstart

# Second run refuses to overwrite (protects an existing working cert):
# "tls-local/ already populated — refusing to overwrite. Run 'make tls-quickstart-force' to regenerate."

# Force regeneration:
make tls-quickstart-force
```

The generated files are gitignored (`examples/distributed/tls-local/` contains only `.gitignore` and `.gitkeep` in version control). The `README.md` written alongside the certs repeats the "DEV ONLY" warning.

### Running with TLS + auth

```bash
# Master — enable TLS and require a bearer token
kar master \
  --config configs/kar98k.yaml \
  --tls-cert examples/distributed/tls-local/insecure.crt \
  --tls-key  examples/distributed/tls-local/insecure.key \
  --auth-token my-secret-token

# Worker — verify master cert and present the same token
kar worker \
  --master master-host:7777 \
  --tls-ca examples/distributed/tls-local/insecure.crt \
  --tls-server-name kar-master \
  --auth-token my-secret-token
```

Flags can also be set via config YAML under `master:`:

```yaml
master:
  listen: ":7777"
  tls:
    cert: /etc/kar/tls/server.crt
    key:  /etc/kar/tls/server.key
    client_ca: /etc/kar/tls/ca.crt   # optional — enables mTLS
  auth_token: "${KAR_AUTH_TOKEN}"     # env-expanded at load time
```

### Docker Compose with TLS

```bash
make tls-quickstart
KAR_AUTH_TOKEN=change-me docker compose -f examples/distributed/compose.tls.yml up --build
```

Run the automated TLS smoke test:

```bash
bash examples/distributed/smoke-tls.sh
```

### Flag reference

| Command | Flag | Description |
|---|---|---|
| `kar master` | `--tls-cert` | Path to server certificate PEM |
| `kar master` | `--tls-key` | Path to server private key PEM |
| `kar master` | `--tls-client-ca` | Path to CA PEM for mTLS client verification |
| `kar master` | `--auth-token` | Bearer token workers must present |
| `kar worker` | `--tls-ca` | Path to CA PEM to verify master certificate |
| `kar worker` | `--tls-cert` | Path to client cert PEM (mTLS) |
| `kar worker` | `--tls-server-name` | TLS server name override |
| `kar worker` | `--auth-token` | Bearer token to present to master |

### Cert isolation rules

- Test certs live exclusively in `internal/rpc/testdata/insecure.{crt,key}`.
- `compose.tls.yml` mounts `./tls-local/` (generated by `make tls-quickstart`) — it does **not** reference `internal/rpc/testdata/`.
- A CI lint test (`TestTestdataIsolation` in `internal/rpc/auth_test.go`) fails the build if any non-testdata file references `insecure.crt` or `insecure.key` by string.
- Master logs the SHA-256 fingerprint of the loaded cert at boot for operator verification.
