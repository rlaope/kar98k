# API Reference

## Command Line Interface

### Usage

```bash
kar98k [flags]
```

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-config` | string | `configs/kar98k.yaml` | Path to configuration file |
| `-version` | bool | `false` | Show version and exit |

### Examples

```bash
# Run with default config
./kar98k

# Run with custom config
./kar98k -config /path/to/config.yaml

# Show version
./kar98k -version
```

## HTTP Endpoints

kar98k exposes several HTTP endpoints on the metrics address (default `:9090`).

### GET /metrics

Prometheus metrics endpoint.

**Response:** Prometheus text format

```
# HELP kar98k_requests_total Total number of requests by target and status
# TYPE kar98k_requests_total counter
kar98k_requests_total{protocol="http",status="success",target="api-health"} 12345

# HELP kar98k_request_duration_seconds Request latency histogram
# TYPE kar98k_request_duration_seconds histogram
kar98k_request_duration_seconds_bucket{protocol="http",target="api-health",le="0.001"} 100
...
```

### GET /healthz

Liveness probe endpoint.

**Response:**
- Status: `200 OK`
- Body: `ok`

### GET /readyz

Readiness probe endpoint.

**Response:**
- Status: `200 OK`
- Body: `ok`

## Prometheus Metrics

### Counters

#### kar98k_requests_total

Total number of requests sent.

**Labels:**
| Label | Description |
|-------|-------------|
| `target` | Target name |
| `status` | `success` or `error` |
| `protocol` | `http`, `http2`, or `grpc` |

**Example queries:**
```promql
# Request rate per target
rate(kar98k_requests_total[5m])

# Error rate
sum(rate(kar98k_requests_total{status="error"}[5m])) /
sum(rate(kar98k_requests_total[5m]))
```

### Histograms

#### kar98k_request_duration_seconds

Request latency distribution.

**Labels:**
| Label | Description |
|-------|-------------|
| `target` | Target name |
| `protocol` | Protocol used |

**Buckets:** Exponential from 1ms to ~16s

**Example queries:**
```promql
# 95th percentile latency
histogram_quantile(0.95, rate(kar98k_request_duration_seconds_bucket[5m]))

# Average latency
rate(kar98k_request_duration_seconds_sum[5m]) /
rate(kar98k_request_duration_seconds_count[5m])
```

### Gauges

#### kar98k_requests_in_flight

Current number of requests being processed.

**Example query:**
```promql
kar98k_requests_in_flight
```

#### kar98k_current_tps

Actual TPS being generated (measured over last second).

**Example query:**
```promql
kar98k_current_tps
```

#### kar98k_target_tps

Target TPS setting from pattern engine.

**Example query:**
```promql
# TPS accuracy
kar98k_current_tps / kar98k_target_tps
```

#### kar98k_active_workers

Number of active worker goroutines.

**Example query:**
```promql
kar98k_active_workers
```

#### kar98k_queued_requests

Number of requests waiting in queue.

**Example query:**
```promql
# Queue utilization (assuming 10000 queue size)
kar98k_queued_requests / 10000
```

#### kar98k_spike_active

Whether a traffic spike is currently active.

**Values:**
- `1` - Spike is active
- `0` - No spike

**Example query:**
```promql
# Spike duration
changes(kar98k_spike_active[1h])
```

#### kar98k_target_health

Health status of each target.

**Labels:**
| Label | Description |
|-------|-------------|
| `target` | Target name |

**Values:**
- `1` - Healthy
- `0` - Unhealthy

**Example query:**
```promql
# Unhealthy targets
kar98k_target_health == 0
```

## Grafana Dashboard

### Recommended Panels

#### Traffic Overview
```promql
# Current TPS
kar98k_current_tps

# Target vs Actual TPS
kar98k_target_tps
kar98k_current_tps
```

#### Latency
```promql
# P50, P95, P99 latency
histogram_quantile(0.50, rate(kar98k_request_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(kar98k_request_duration_seconds_bucket[5m]))
histogram_quantile(0.99, rate(kar98k_request_duration_seconds_bucket[5m]))
```

#### Error Rate
```promql
# Error percentage
100 * sum(rate(kar98k_requests_total{status="error"}[5m])) /
sum(rate(kar98k_requests_total[5m]))
```

#### System Health
```promql
# Active workers
kar98k_active_workers

# Queue depth
kar98k_queued_requests

# In-flight requests
kar98k_requests_in_flight
```

## Exit Codes

| Code | Description |
|------|-------------|
| 0 | Success |
| 1 | Configuration error |
| 1 | Runtime error |

## Signals

| Signal | Behavior |
|--------|----------|
| `SIGINT` | Graceful shutdown |
| `SIGTERM` | Graceful shutdown |
