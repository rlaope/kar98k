# CLAUDE.md — kar98k working context

This file is read on every session start. Keep it dense and current.

## Project at a glance

- **Go** load testing tool. Generates **realistic, irregular traffic** (Poisson spikes + noise + hour-of-day schedule) for 24/7 simulation.
- Single binary `kar` (entry: `cmd/kar98k/main.go`). Cobra CLI.
- `internal/` is the work area; `pkg/protocol` exposes HTTP/2/gRPC clients.

### Key dirs
| Path | Role |
|---|---|
| `internal/pattern/` | TPS pattern engine — Poisson spikes, noise (spring + perlin) |
| `internal/controller/` | Scheduler (hour-of-day) + main control loop |
| `internal/worker/` | Goroutine pool + rate limiter (`golang.org/x/time/rate`) |
| `internal/discovery/` | Adaptive max-TPS binary search |
| `internal/script/` | k6-style script runner (Starlark/JS/Py/Ruby; goja for JS) |
| `internal/dashboard/` | Web UI (HTML embedded) |
| `internal/daemon/` | Background daemon for long-running mode |
| `internal/health/` | Prometheus metrics + health checker |

### Build / test
```
make build           # binary at bin/kar
go build ./...
go vet ./...
go test ./... -count=1 -short
```

## Architectural invariants (do not break)

1. **HdrHistogram is the standard** for latency in the metrics path (PR #42, commit `804ecc8`). Use it for any new percentile work. `internal/discovery/analyzer.go` is the last sort-based holdout — see issue #49.
2. **Worker pool decouples TPS control from job submission.** `controller.generateLoop` over-feeds the queue (1ms tick × 10 jobs); `worker.Pool.limiter` (`rate.Limiter`) decides actual send rate. Queue full → silent drop. Issue #47 makes drops visible.
3. **Pattern engine is pure** — `engine.CalculateTPS()` takes a schedule multiplier and returns a target TPS. No I/O. Keep it that way; helps `kar simulate --dry-run` (#57).
4. **Daemon is single-process today.** Distributed mode (#52) plans to add master/worker; design must keep single-process flow as default.
5. **Configs are layered**: structural YAML parse → semantic validation (planned in #61) → runtime defaults from `config.DefaultConfig()`.

## Issue tracker — current iteration

Internal# → GitHub#. All open as of this writing.

### Gaps (cleanup) — small / medium
| In | GH | Title | Status |
|---|---|---|---|
| #1 | **#46** | feat: expose Perlin noise as alternative noise type | ✅ **DONE** (this session) |
| #2 | **#47** | feat: surface queue drop rate as metric & dashboard widget | ⏭ **NEXT** (after compact) |
| #3 | #48 | feat: explicit priority for schedule entries | open |
| #4 | #49 | perf: migrate discovery analyzer to HdrHistogram | open |
| #5 | #50 | feat: show next-spike countdown in TUI/dashboard | open |

### Capability uplift (medium / epic)
| In | GH | Title | Notes |
|---|---|---|---|
| #6 | #51 | feat: coordinated omission correction (wrk2-style) | Use HdrHistogram `RecordValueWithExpectedInterval` |
| #7 | #52 | feat: distributed mode (master/worker) [epic] | Build on `internal/daemon/` |
| #8 | #53 | feat: scenarios / phases for multi-stage runs [epic] | YAML `scenarios:` array, sequential phases |
| #9 | #54 | feat: injection profile DSL (Gatling-style) | Compose with #53 |
| #10 | #55 | feat: latency CDF & heatmap in HTML report | Extends #38, uses HdrHistogram percentile iter |

### 24/7 accessibility
| In | GH | Title |
|---|---|---|
| #11 | #56 | feat: kar demo — zero-config |
| #12 | #57 | feat: kar simulate --dry-run for 24h forecast |
| #13 | #58 | feat: forecast view in dashboard |
| #14 | #59 | feat: circuit breaker / auto-pause |
| #15 | #60 | feat: kar doctor |
| #16 | #61 | feat: kar validate <config> |

## ✅ #46 changeset (just landed, uncommitted)

Goal: let users pick `spring` (default) or `perlin` noise via config.

Files changed:
- `internal/config/config.go` — added `NoiseType` constants + `Type` field on `Noise` struct
- `internal/pattern/noise.go` — added `NoiseGenerator` interface, `NewNoiseGenerator` factory, `Enabled()` method on both `Noise` and `PerlinNoise`
- `internal/pattern/engine.go` — `Engine.noise` is now `NoiseGenerator` interface; constructor uses factory; `GetStatus` calls `Enabled()` instead of `cfg.Enabled`
- `internal/pattern/noise_test.go` — **new**, 10 tests covering factory routing, enabled state, amplitude bounds for both generators
- `docs/en/configuration.md` — documented `type` field
- `configs/kar98k.yaml` — commented example showing the option

Verified: `go build`, `go vet`, `go test ./...` all pass. PR not opened yet.

## ⏭ #47 handoff — Queue drop visibility

**Read this section first when resuming after compact.**

### What & why
At high TPS, `worker.Pool.Submit()` drops jobs when the queue is full and returns `false` — the controller silently backs off. Users see "actual TPS < target TPS" with no signal explaining it. We must surface this.

### Where the silence happens
- `internal/worker/pool.go` `Submit()` (around line 152-162) — `default:` branch returns `false` with no metric increment
- `internal/controller/controller.go` `submitJobs()` (around line 198-228) — calls `Submit`, returns on `false` with no log

### Required changes (acceptance criteria from #47)

1. **Metric:** new Prometheus counter `kar98k_queue_drops_total` (and probably a gauge `kar98k_queue_size` if not already there).
   - Wire in `internal/health/metrics.go` (mirror style of existing counters).
2. **Submit() must increment** the counter on the drop branch.
3. **Status struct + dashboard** — drop count + sustained drop rate (over a rolling window, e.g. last 60s).
   - `pool.go` already has `tpsCount` + `measureTPS` 1-second tick → mirror that for drops.
   - Expose via `Pool.DropRate()` (drops / submits in last window).
   - Add to `controller.Status` (`internal/controller/controller.go` ~line 243).
   - Surface in dashboard widget (`internal/dashboard/html.go`) and `kar status` (`internal/cli/status.go`).
4. **Heuristic warning:** when sustained drop rate > 1% for 60s, log once (rate-limited) with a recommended `queue_size` (e.g. ceil to next power of 2 of the current TPS × 10).
5. **Tests:** unit test in `internal/worker/pool_test.go` (new file) — fill queue, verify Submit returns false AND counter increments.

### Design notes & gotchas
- Keep counter atomic (`int64` + `atomic.AddInt64`); don't take a mutex on the hot path. Existing `tpsCount` is the pattern to follow.
- Don't change `Submit()` return value contract — controllers already handle `false`. Just add the side-effect of metric increment.
- The "sustained for 60s" heuristic should reuse the existing 1s tick in `measureTPS` — accumulate drop counts in a 60-element ring buffer, compute rate.
- Beware double-counting: only count drops in `Submit()`, not at the controller layer.

### Files you'll likely touch
```
internal/worker/pool.go              # core
internal/worker/pool_test.go         # NEW
internal/health/metrics.go           # add counter
internal/controller/controller.go    # Status struct
internal/cli/status.go               # CLI display
internal/dashboard/html.go           # widget
internal/dashboard/server.go         # JSON endpoint feeding the widget
docs/en/configuration.md             # mention the new metric
```

### Verify
```
go build ./... && go vet ./... && go test ./... -count=1 -short
./bin/kar run --config configs/kar98k.yaml --trigger    # smoke
curl -s localhost:9090/metrics | grep queue_drops
```

Open a PR titled `feat: surface queue drop rate as metric & dashboard widget` linking #47.

## Workflow conventions (project-specific)

- Conventional commit prefix: `feat:`, `fix:`, `perf:`, `refactor:`. PR titles match.
- Recent merged PRs (for style reference): #38, #39, #40, #41, #42.
- Don't commit unless explicitly asked — current `#46` work is intentionally uncommitted, awaiting user direction.
- `make build` produces `bin/kar`; the binary is gitignored.

## Current uncommitted state (handoff snapshot)

```
# changes pending for #46 (Perlin noise option)
internal/config/config.go
internal/pattern/engine.go
internal/pattern/noise.go
internal/pattern/noise_test.go         (new)
docs/en/configuration.md
configs/kar98k.yaml
```

If user wants commit/PR: title `feat: expose Perlin noise as alternative noise type`, link `#46`.
