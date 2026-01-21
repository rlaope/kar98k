# kar98k

24/7 High-Intensity Irregular Traffic Simulation Service in Go.

kar98k generates realistic, irregular traffic patterns for load testing and performance validation of HTTP/1.1, HTTP/2, and gRPC services.

## Features

- **Multi-Protocol Support**: HTTP/1.1, HTTP/2, and gRPC
- **Irregular Traffic Patterns**: Poisson-distributed spikes with micro-fluctuations
- **Time-of-Day Scheduling**: Configure different TPS profiles for different hours
- **Rate Limiting**: Precise TPS control with `golang.org/x/time/rate`
- **Health Monitoring**: Built-in health checks and Prometheus metrics
- **Graceful Shutdown**: Clean request draining on termination

## Quick Start

```bash
# Build
make build

# Run with default config
./bin/kar98k -config configs/kar98k.yaml

# Run with Docker
docker-compose up
```

## Configuration

See `configs/kar98k.yaml` for the full configuration reference.

```yaml
targets:
  - name: api-service
    url: http://localhost:8080/api/health
    protocol: http
    method: GET
    weight: 100

controller:
  base_tps: 100
  max_tps: 1000
  schedule:
    - hours: [9, 10, 11, 12, 13, 14, 15, 16, 17]
      tps_multiplier: 1.5

pattern:
  poisson:
    enabled: true
    lambda: 0.1
    spike_factor: 3.0
  noise:
    enabled: true
    amplitude: 0.15
```

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Pulse-Controller│────▶│  Pattern-Engine │────▶│   Pulse-Worker  │
│   (Scheduler)    │     │ (Poisson/Noise) │     │  (Goroutine Pool)│
└─────────────────┘     └─────────────────┘     └─────────────────┘
         │                                               │
         ▼                                               ▼
┌─────────────────┐                            ┌─────────────────┐
│  Health-Checker │                            │    Targets      │
│   (Metrics)     │                            │ (HTTP/gRPC)     │
└─────────────────┘                            └─────────────────┘
```

## Metrics

Prometheus metrics are exposed at `:9090/metrics`:

- `kar98k_requests_total` - Total requests by target and status
- `kar98k_request_duration_seconds` - Request latency histogram
- `kar98k_current_tps` - Current TPS setting
- `kar98k_active_workers` - Number of active worker goroutines

## Development

```bash
# Run tests
make test

# Run with race detector
make test-race

# Build for all platforms
make build-all

# Lint
make lint
```

## License

MIT
