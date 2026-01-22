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

### Headless Mode

Run with a config file for automation:

```bash
kar run --config kar98k.yaml
```

### Daemon Mode

Run as a background service:

```bash
# Start daemon
kar start --daemon

# Check status
kar status

# View logs
kar logs -f

# Trigger traffic
kar trigger

# Pause traffic
kar pause

# Stop daemon
kar stop
```

## Commands

| Command | Description |
|---------|-------------|
| `kar start` | Launch interactive TUI |
| `kar start --daemon` | Start as background daemon |
| `kar run --config <file>` | Run headless with config file |
| `kar status` | Show daemon status |
| `kar logs [-f]` | View logs (with optional follow) |
| `kar trigger` | Start traffic generation |
| `kar pause` | Pause traffic generation |
| `kar stop` | Stop the daemon |
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
