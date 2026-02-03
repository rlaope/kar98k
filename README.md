# kar98k

24/7 High-Intensity Irregular Traffic Simulation Service in Go.

kar98k generates realistic, irregular traffic patterns for load testing and performance validation of HTTP/1.1, HTTP/2, and gRPC services.

![kar98k banner](./assets/kar98start.png)

## Features

- **Interactive CLI**: Beautiful sky-blue themed TUI for easy configuration
- **Multi-Protocol Support**: HTTP/1.1, HTTP/2, and gRPC
- **Irregular Traffic Patterns**: Poisson-distributed spikes with micro-fluctuations
- **Time-of-Day Scheduling**: Configure different TPS profiles for different hours
- **Real-time Monitoring**: Live stats dashboard while traffic is flowing
- **Test Report**: Detailed statistics with latency distribution (P50/P95/P99)
- **Real-time Logs**: Event logging with spike detection and peak TPS tracking

## Installation

### Download Binary (Recommended)

Download the latest release for your platform:

```bash
# Linux (amd64)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-linux-amd64
chmod +x kar98k-linux-amd64
sudo mv kar98k-linux-amd64 /usr/local/bin/kar

# Linux (arm64)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-linux-arm64
chmod +x kar98k-linux-arm64
sudo mv kar98k-linux-arm64 /usr/local/bin/kar

# macOS (Apple Silicon)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-darwin-arm64
chmod +x kar98k-darwin-arm64
sudo mv kar98k-darwin-arm64 /usr/local/bin/kar

# macOS (Intel)
curl -LO https://github.com/rlaope/kar98k/releases/latest/download/kar98k-darwin-amd64
chmod +x kar98k-darwin-amd64
sudo mv kar98k-darwin-amd64 /usr/local/bin/kar

# Windows (PowerShell)
Invoke-WebRequest -Uri https://github.com/rlaope/kar98k/releases/latest/download/kar98k-windows-amd64.exe -OutFile kar.exe
```

### Docker

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/rlaope/kar98k:latest

# Run
docker run --rm -it ghcr.io/rlaope/kar98k:latest version
```

### Build from Source

```bash
# Clone and build
git clone https://github.com/rlaope/kar98k.git
cd kar98k
make build

# Binary is at ./bin/kar
./bin/kar version
```

### Verify Installation

```bash
kar version
```

## Quick Start

### Interactive Mode (Recommended)

```bash
kar start
```

This launches the interactive TUI where you can configure everything step by step.

#### Step 1: Target Configuration

![Step 1](./assets/config1.png)

Configure your target endpoint:
- **Target URL**: The endpoint to send traffic to
- **HTTP Method**: GET, POST, PUT, DELETE, etc.
- **Protocol**: http, http2, or grpc

#### Step 2: Traffic Configuration

![Step 2](./assets/config2.png)

Set your traffic parameters:
- **Base TPS**: Baseline transactions per second
- **Max TPS**: Maximum TPS cap during spikes

#### Step 3: Pattern Configuration

![Step 3](./assets/config3.png)

Fine-tune the traffic pattern:
- **Poisson Lambda**: Average spikes per second (e.g., 0.1)
- **Spike Factor**: TPS multiplier during spikes (e.g., 3.0x)
- **Noise Amplitude**: Random fluctuation range (e.g., 0.15 = ±15%)
- **Schedule**: Time-based TPS multipliers (e.g., `9-17:1.5, 0-5:0.3`)

#### Step 4: Review & Fire

![Step 4](./assets/config4.png)

Review your configuration and pull the trigger!

#### Firing

![Firing](./assets/trigger.png)

Watch real-time stats as traffic flows:
- Current TPS with progress bar
- Requests sent / Errors / Avg Latency
- Elapsed time

#### Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Tab` / `↓` | Next field |
| `Shift+Tab` / `↑` | Previous field |
| `Enter` | Next screen / Select |
| `Esc` | Previous screen |
| `Q` or `Ctrl+C` | Stop and show report (on Running screen) |

#### Test Report

When you stop the test (`Q` or `Ctrl+C`), a detailed report is displayed:

- **Overview**: Duration, total requests, success rate, avg/peak TPS
- **Latency Distribution**: Min, Avg, Max, P50, P95, P99
- **Latency Histogram**: Visual distribution of response times
- **Status Codes**: Count by HTTP status code
- **Timeline Summary**: 5-second interval breakdown with spike detection

### Real-time Logs

Monitor events while test is running (in another terminal):

```bash
kar logs -f
```

Log events include:
- `EVENT: SPIKE START/END` - Spike detection
- `EVENT: New peak TPS` - New peak TPS reached
- `STATUS:` - Periodic status (every 10s)
- `WARNING:` - Error spikes
- `SUMMARY:` - Final summary on stop

### Stop Running Test

```bash
kar stop
```

This sends a stop signal to the running kar instance and displays the test report.

### Headless Mode

Run with a config file for automation:

```bash
kar run --config configs/kar98k.yaml
```

### Demo Server

A demo HTTP server is included for testing:

```bash
# Build and run demo server
make run-server

# Server runs at http://localhost:8080
# Endpoints: /health, /api/users, /api/stats, /api/echo
```

## Commands

| Command | Description |
|---------|-------------|
| `kar start` | Launch interactive TUI |
| `kar run --config <file>` | Run headless with config file |
| `kar discover` | Auto-discover maximum sustainable TPS |
| `kar stop` | Stop running kar instance |
| `kar logs` | View recent logs |
| `kar logs -f` | Follow logs in real-time |
| `kar logs -n 50` | Show last 50 lines |
| `kar version` | Show version info |

## Configuration File

For headless mode, use a YAML config file:

```yaml
targets:
  - name: my-api
    url: http://localhost:8080/api/health
    protocol: http
    method: GET
    weight: 100
    timeout: 10s

controller:
  base_tps: 100      # 100 requests/sec baseline
  max_tps: 500       # Cap at 500 during spikes

pattern:
  poisson:
    enabled: true
    lambda: 0.1       # ~10 seconds between spikes
    spike_factor: 2.0 # 2x TPS during spikes
  noise:
    enabled: true
    amplitude: 0.1    # ±10% random fluctuation

metrics:
  enabled: true
  address: ":9090"
```

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ Pulse-Controller│────▶│  Pattern-Engine │────▶│   Pulse-Worker  │
│   (Scheduler)   │     │ (Poisson/Noise) │     │ (Goroutine Pool)│
└─────────────────┘     └─────────────────┘     └─────────────────┘
         │                                               │
         ▼                                               ▼
┌─────────────────┐                            ┌─────────────────┐
│  Health-Checker │                            │     Targets     │
│    (Metrics)    │                            │  (HTTP/gRPC)    │
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

# Run sample echo server for testing
make run-server

# Run full demo
make demo
```

## Docker

```bash
# Build image
make docker

# Run with docker-compose
docker-compose up
```

## License

MIT
