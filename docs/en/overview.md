# Overview

## What is kar98k?

kar98k is a high-performance traffic simulation tool designed to generate realistic, irregular traffic patterns for load testing and performance validation. It supports HTTP/1.1, HTTP/2, and gRPC protocols.

## Key Features

### Multi-Protocol Support
- **HTTP/1.1**: Standard HTTP with connection pooling
- **HTTP/2**: Stream multiplexing for improved performance
- **gRPC**: Native gRPC health check protocol support

### Irregular Traffic Patterns
Unlike traditional load testing tools that generate constant or linearly increasing traffic, kar98k creates realistic traffic patterns using:

- **Poisson Distribution Spikes**: Random traffic bursts that mimic real-world usage patterns
- **Micro Fluctuations**: Continuous small variations in traffic rate

### Time-of-Day Scheduling
Configure different TPS (Transactions Per Second) profiles for different hours:
- Higher traffic during business hours
- Lower traffic during night hours
- Custom multipliers for any hour

### Rate Limiting
Precise TPS control using `golang.org/x/time/rate` token bucket algorithm ensures accurate traffic generation without overwhelming targets.

### Health Monitoring
- Automatic health checks for all targets
- Unhealthy targets are temporarily excluded from traffic
- Prometheus metrics for observability

### Graceful Shutdown
- Clean request draining on termination
- Configurable shutdown timeout
- No dropped requests during shutdown

### Adaptive Load Discovery
Automatically find the maximum sustainable TPS for your system:
- Binary search algorithm for efficient discovery
- Configurable P95 latency and error rate thresholds
- Generates recommended BaseTPS and MaxTPS settings

## Use Cases

1. **Load Testing**: Validate system performance under realistic traffic patterns
2. **Chaos Engineering**: Test system resilience to traffic spikes
3. **Capacity Planning**: Understand system limits with sustained traffic
4. **Performance Regression**: Continuous traffic generation for performance monitoring

## Architecture Overview

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

## Requirements

- Go 1.22 or later
- Docker (optional, for containerized deployment)
