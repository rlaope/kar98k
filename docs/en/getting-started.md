# Getting Started

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/kar98k/kar98k.git
cd kar98k

# Build
make build

# Verify installation
./bin/kar98k -version
```

### Using Docker

```bash
# Build Docker image
docker build -t kar98k:latest .

# Or use docker-compose
docker-compose up -d
```

### Pre-built Binaries

Download pre-built binaries from the [Releases](https://github.com/kar98k/kar98k/releases) page.

## Quick Start

### 1. Create Configuration

Create a `kar98k.yaml` file:

```yaml
targets:
  - name: my-api
    url: http://localhost:8080/api/health
    protocol: http
    method: GET
    weight: 100
    timeout: 10s

controller:
  base_tps: 100
  max_tps: 500

pattern:
  poisson:
    enabled: true
    lambda: 0.1
    spike_factor: 2.0
  noise:
    enabled: true
    amplitude: 0.1

metrics:
  enabled: true
  address: ":9090"
```

### 2. Run kar98k

```bash
./bin/kar98k -config kar98k.yaml
```

### 3. Monitor Metrics

Open your browser to `http://localhost:9090/metrics` to see Prometheus metrics.

## Basic Configuration

### Targets

Define the endpoints to send traffic to:

```yaml
targets:
  - name: api-endpoint      # Unique identifier
    url: http://host:port/path
    protocol: http          # http, http2, or grpc
    method: GET             # HTTP method
    headers:                # Optional headers
      Authorization: "Bearer token"
    body: '{"key": "value"}'  # Optional request body
    weight: 100             # Relative weight for load distribution
    timeout: 10s            # Request timeout
```

### Traffic Pattern

Control how traffic is generated:

```yaml
controller:
  base_tps: 100           # Baseline TPS
  max_tps: 1000           # Maximum TPS cap
  ramp_up_duration: 30s   # Time to reach base TPS on startup

pattern:
  poisson:
    enabled: true
    lambda: 0.1           # Spike frequency (spikes per second)
    spike_factor: 3.0     # TPS multiplier during spikes
  noise:
    enabled: true
    amplitude: 0.15       # Random fluctuation range (±15%)
```

## Verification

### Check Health Endpoint

```bash
curl http://localhost:9090/healthz
# Output: ok
```

### Check Metrics

```bash
curl http://localhost:9090/metrics | grep kar98k
```

Key metrics to watch:
- `kar98k_requests_total` - Total requests sent
- `kar98k_current_tps` - Current actual TPS
- `kar98k_target_tps` - Target TPS setting
- `kar98k_spike_active` - Whether a spike is active

## Next Steps

- [Configuration Reference](configuration.md) - Full configuration options
- [Architecture](architecture.md) - Deep dive into how kar98k works
- [API Reference](api-reference.md) - Metrics and endpoints
