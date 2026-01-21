# Architecture

This document describes the internal architecture of kar98k.

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                              main.go                                 │
│                         (Application Entry)                          │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┼───────────────┐
                    ▼               ▼               ▼
        ┌───────────────┐  ┌───────────────┐  ┌───────────────┐
        │   Controller  │  │  Worker Pool  │  │Health Checker │
        │               │  │               │  │               │
        │ ┌───────────┐ │  │ ┌───────────┐ │  │ ┌───────────┐ │
        │ │ Scheduler │ │  │ │  Limiter  │ │  │ │  Metrics  │ │
        │ └───────────┘ │  │ └───────────┘ │  │ └───────────┘ │
        └───────┬───────┘  └───────┬───────┘  └───────────────┘
                │                  │
                ▼                  ▼
        ┌───────────────┐  ┌───────────────┐
        │Pattern Engine │  │   Protocols   │
        │               │  │               │
        │ ┌───────────┐ │  │ ┌───────────┐ │
        │ │  Poisson  │ │  │ │   HTTP    │ │
        │ ├───────────┤ │  │ ├───────────┤ │
        │ │   Noise   │ │  │ │   gRPC    │ │
        │ └───────────┘ │  │ └───────────┘ │
        └───────────────┘  └───────────────┘
```

## Core Components

### 1. Controller (`internal/controller/`)

The Controller is the brain of kar98k, orchestrating all traffic generation.

**Responsibilities:**
- Coordinate between Pattern Engine and Worker Pool
- Apply time-of-day scheduling
- Manage ramp-up on startup
- Handle graceful shutdown

**Key Methods:**
```go
func (c *Controller) Start(ctx context.Context)  // Start traffic generation
func (c *Controller) Stop()                       // Graceful shutdown
func (c *Controller) GetStatus() Status           // Current status
```

**Control Loops:**
1. **Ramp-up Loop**: Gradually increases TPS from 0 to base_tps
2. **Control Loop**: Updates target TPS every 100ms based on patterns
3. **Generate Loop**: Continuously submits jobs to worker pool

### 2. Scheduler (`internal/controller/scheduler.go`)

Manages time-of-day based TPS multipliers.

**How it works:**
```go
// Get multiplier for current hour
multiplier := scheduler.GetMultiplier()

// Apply to base TPS
effectiveTPS := baseTPS * multiplier
```

**Schedule Resolution:**
- Later entries in the schedule take precedence
- Hours not in any schedule entry use multiplier 1.0

### 3. Pattern Engine (`internal/pattern/`)

Generates realistic traffic patterns using mathematical models.

#### Poisson Spike Generator

Uses Poisson distribution for random spike timing:

```go
// Inverse transform sampling
// t = -ln(U) / λ where U ~ Uniform(0,1)
interval := -math.Log(rand.Float64()) / lambda
```

**Spike Lifecycle:**
```
      TPS
       ▲
       │    ╭──╮
spike  │   ╱    ╲
factor │  ╱      ╲
       │ ╱        ╲
base ──┼╱──────────╲────────
       │           │
       └─────┴─────┴─────▶ Time
         ramp  peak  ramp
          up         down
```

#### Noise Generator

Adds continuous micro-fluctuations using a spring-damper system:

```go
force := springConstant * (target - current)
velocity = velocity * damping + force
current += velocity
```

### 4. Worker Pool (`internal/worker/`)

Manages goroutines for executing requests.

**Components:**
- **Job Queue**: Buffered channel for pending requests
- **Rate Limiter**: Token bucket for TPS control
- **Goroutine Pool**: Fixed number of worker goroutines

**Flow:**
```
Submit(job) → Queue → Worker → RateLimiter.Wait() → Client.Do() → Metrics
```

**Rate Limiting:**
Uses `golang.org/x/time/rate` token bucket algorithm:
- Tokens added at target TPS rate
- Each request consumes one token
- Requests wait if no tokens available

### 5. Protocol Clients (`pkg/protocol/`)

Protocol-specific client implementations.

**Interface:**
```go
type Client interface {
    Do(ctx context.Context, req *Request) *Response
    Close() error
}
```

**HTTP Client Features:**
- Connection pooling
- Keep-alive support
- `sync.Pool` for buffer reuse
- HTTP/2 stream multiplexing

**gRPC Client Features:**
- Connection caching per target
- Keepalive configuration
- Standard health check protocol

### 6. Health Checker (`internal/health/`)

Monitors target health and collects metrics.

**Health Check Flow:**
1. Periodic health check (configurable interval)
2. Send GET request to each target
3. Mark unhealthy if error or status >= 400
4. Unhealthy targets excluded from traffic

**Prometheus Metrics:**
| Metric | Type | Description |
|--------|------|-------------|
| `kar98k_requests_total` | Counter | Total requests by target/status |
| `kar98k_request_duration_seconds` | Histogram | Request latency |
| `kar98k_current_tps` | Gauge | Actual TPS |
| `kar98k_target_tps` | Gauge | Target TPS |
| `kar98k_active_workers` | Gauge | Active worker count |
| `kar98k_spike_active` | Gauge | Spike active (1/0) |
| `kar98k_target_health` | Gauge | Target health (1/0) |

## Data Flow

### Request Generation Flow

```
1. Controller.generateLoop()
   │
   ├─▶ Select target (weighted random)
   │
   ├─▶ Check target health
   │
   ├─▶ Create Job{Target, Client}
   │
   └─▶ Pool.Submit(job)
        │
        ├─▶ Queue (buffered channel)
        │
        └─▶ Worker goroutine
             │
             ├─▶ RateLimiter.Wait()
             │
             ├─▶ Client.Do(request)
             │
             └─▶ Metrics.RecordRequest()
```

### TPS Calculation Flow

```
1. Controller.updateTPS() [every 100ms]
   │
   ├─▶ Scheduler.GetMultiplier()
   │    └─▶ Check current hour against schedule
   │
   ├─▶ Engine.CalculateTPS(scheduleMultiplier)
   │    │
   │    ├─▶ baseTPS * scheduleMultiplier
   │    │
   │    ├─▶ * Poisson.Multiplier()
   │    │    └─▶ Check spike state, calculate ramp
   │    │
   │    └─▶ * Noise.Multiplier()
   │         └─▶ Spring-damper calculation
   │
   └─▶ Pool.SetRate(tps)
        └─▶ Update rate limiter
```

## Concurrency Model

### Goroutines

1. **Main goroutine**: Signal handling, lifecycle management
2. **Control loop**: TPS updates (1 goroutine)
3. **Generate loop**: Job submission (1 goroutine)
4. **Worker pool**: Request execution (N goroutines, configurable)
5. **Health checker**: Periodic checks (1 goroutine)
6. **Metrics server**: HTTP server (managed by net/http)

### Synchronization

- **Rate Limiter**: Thread-safe token bucket
- **Metrics**: Thread-safe Prometheus collectors
- **Pattern Engine**: `sync.RWMutex` for state access
- **Job Queue**: Go channel (inherently thread-safe)

## Graceful Shutdown

```
1. Receive SIGINT/SIGTERM
   │
   ├─▶ Cancel context
   │
   ├─▶ Controller.Stop()
   │    └─▶ Wait for control/generate loops
   │
   ├─▶ HealthChecker.Stop()
   │
   ├─▶ Pool.Drain(timeout)
   │    └─▶ Wait for in-flight requests
   │
   ├─▶ Pool.Stop()
   │    └─▶ Close job channel, wait for workers
   │
   └─▶ MetricsServer.Shutdown()
```
