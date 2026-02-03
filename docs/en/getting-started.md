# Getting Started

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

# macOS (Apple Silicon / M1, M2, M3)
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
# Clone the repository
git clone https://github.com/rlaope/kar98k.git
cd kar98k

# Build
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

This launches the interactive TUI with a 4-step configuration flow:

1. **Target Configuration** - URL, HTTP method, protocol selection
2. **Traffic Configuration** - Base TPS, Max TPS settings
3. **Pattern Configuration** - Poisson Lambda, Spike Factor, Noise Amplitude, Schedule
4. **Review & Fire** - Review settings and pull the trigger!

#### TUI Keyboard Shortcuts

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

Monitor events in real-time while test is running:

```bash
# In another terminal
kar logs -f

# Or directly
tail -f /tmp/kar98k/kar98k.log
```

Log events include:
- `EVENT: SPIKE START/END` - Spike detection
- `EVENT: New peak TPS` - New peak TPS reached
- `STATUS:` - Periodic status (every 10s)
- `WARNING:` - Error spikes
- `SUMMARY:` - Final summary on stop

### Stop Running Test

```bash
# From another terminal
kar stop
```

This will:
1. Send stop signal to running kar instance
2. Display test report in the running terminal
3. Show summary in the stop terminal

### Headless Mode

Run with a config file for automation:

```bash
kar run --config kar.yaml
```

### Adaptive Load Discovery

Automatically find the maximum sustainable TPS for your system:

```bash
# Interactive mode
kar discover

# With URL directly
kar discover --url http://localhost:8080/health

# Headless mode with custom thresholds
kar discover --url http://localhost:8080/health --headless \
  --latency-limit 200 \
  --error-limit 3 \
  --min-tps 50 \
  --max-tps 1000
```

Discovery uses binary search to efficiently find the optimal TPS:
1. Starts at minimum TPS and verifies stability
2. Uses binary search to find the breaking point
3. Reports maximum sustainable TPS with recommendations

Output example:
```
✓ DISCOVERY COMPLETE

Your system can handle:
  Sustained TPS:  486
  Breaking Point: 583 TPS

Recommendation:
  Set BaseTPS to 389 (80% of sustained)
  Set MaxTPS to 778 (safe spike limit)
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

For headless/daemon mode, use a YAML config file:

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
