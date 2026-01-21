# Configuration Reference

kar98k uses YAML configuration files. This document describes all available options.

## Complete Configuration Example

```yaml
targets:
  - name: api-health
    url: http://localhost:8080/api/health
    protocol: http
    method: GET
    weight: 50
    timeout: 10s

  - name: api-users
    url: http://localhost:8080/api/users
    protocol: http
    method: GET
    headers:
      Authorization: "Bearer ${API_TOKEN}"
      Content-Type: "application/json"
    weight: 30
    timeout: 15s

  - name: api-data
    url: http://localhost:8080/api/data
    protocol: http
    method: POST
    headers:
      Content-Type: "application/json"
    body: '{"query": "test"}'
    weight: 20
    timeout: 20s

controller:
  base_tps: 100
  max_tps: 1000
  ramp_up_duration: 30s
  shutdown_timeout: 30s
  schedule:
    - hours: [9, 10, 11, 12, 13, 14, 15, 16, 17]
      tps_multiplier: 1.5
    - hours: [12, 13]
      tps_multiplier: 2.0
    - hours: [0, 1, 2, 3, 4, 5]
      tps_multiplier: 0.3

pattern:
  poisson:
    enabled: true
    lambda: 0.1
    spike_factor: 3.0
    min_interval: 30s
    max_interval: 5m
    ramp_up: 5s
    ramp_down: 10s
  noise:
    enabled: true
    amplitude: 0.15

worker:
  pool_size: 1000
  queue_size: 10000
  max_idle_conns: 100
  idle_conn_timeout: 90s

health:
  enabled: true
  interval: 10s
  timeout: 5s

metrics:
  enabled: true
  address: ":9090"
  path: "/metrics"
```

## Configuration Sections

### targets

List of target endpoints to send traffic to.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | string | Yes | - | Unique identifier for the target |
| `url` | string | Yes | - | Full URL including protocol and path |
| `protocol` | string | No | `http` | Protocol: `http`, `http2`, or `grpc` |
| `method` | string | No | `GET` | HTTP method |
| `headers` | map | No | - | Request headers |
| `body` | string | No | - | Request body |
| `weight` | int | No | `100` | Relative weight for load distribution |
| `timeout` | duration | No | `30s` | Request timeout |

### controller

Controls the main traffic generation behavior.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `base_tps` | float | No | `100` | Baseline transactions per second |
| `max_tps` | float | No | `1000` | Maximum TPS cap |
| `ramp_up_duration` | duration | No | `30s` | Time to reach base TPS on startup |
| `shutdown_timeout` | duration | No | `30s` | Max time to wait for graceful shutdown |
| `schedule` | list | No | - | Time-of-day TPS multipliers |

#### schedule

| Field | Type | Description |
|-------|------|-------------|
| `hours` | list[int] | Hours (0-23) when this multiplier applies |
| `tps_multiplier` | float | Multiplier to apply to base TPS |

### pattern

Controls traffic pattern generation.

#### pattern.poisson

Poisson distribution for random traffic spikes.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | `true` | Enable Poisson spikes |
| `lambda` | float | No | `0.1` | Average spikes per second |
| `spike_factor` | float | No | `3.0` | TPS multiplier during spikes |
| `min_interval` | duration | No | `30s` | Minimum time between spikes |
| `max_interval` | duration | No | `5m` | Maximum time between spikes |
| `ramp_up` | duration | No | `5s` | Time to reach peak spike |
| `ramp_down` | duration | No | `10s` | Time to return to baseline |

#### pattern.noise

Micro fluctuations for realistic traffic.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | `true` | Enable noise |
| `amplitude` | float | No | `0.15` | Fluctuation range (0.15 = ±15%) |

### worker

Worker pool configuration.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `pool_size` | int | No | `1000` | Maximum concurrent workers |
| `queue_size` | int | No | `10000` | Request queue size |
| `max_idle_conns` | int | No | `100` | HTTP keep-alive connections |
| `idle_conn_timeout` | duration | No | `90s` | Connection idle timeout |

### health

Health checker configuration.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | `true` | Enable health checking |
| `interval` | duration | No | `10s` | Health check interval |
| `timeout` | duration | No | `5s` | Health check timeout |

### metrics

Prometheus metrics configuration.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | `true` | Enable metrics endpoint |
| `address` | string | No | `:9090` | Listen address |
| `path` | string | No | `/metrics` | Metrics endpoint path |

## Environment Variables

You can use environment variables in the configuration:

```yaml
headers:
  Authorization: "Bearer ${API_TOKEN}"
```

Note: Environment variable substitution must be handled by your deployment system (e.g., Docker Compose, Kubernetes).

## Configuration Validation

kar98k validates the configuration on startup:

- At least one target is required
- `base_tps` must be positive
- `max_tps` must be >= `base_tps`
- `poisson.lambda` must be positive if enabled
- `poisson.spike_factor` must be >= 1
- `noise.amplitude` must be between 0 and 1
- `worker.pool_size` must be positive
