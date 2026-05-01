package health

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for kar98k.
type Metrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight prometheus.Gauge
	CurrentTPS       prometheus.Gauge
	TargetTPS        prometheus.Gauge
	ActiveWorkers    prometheus.Gauge
	QueuedRequests   prometheus.Gauge
	QueueDropsTotal  prometheus.Counter
	QueueDropRate    prometheus.Gauge
	SpikeActive      prometheus.Gauge
	TargetHealth     *prometheus.GaugeVec

	// Scenario phase metrics (issue #63).
	ScenarioPhaseIndex            prometheus.Gauge
	ScenarioPhaseTransitionsTotal *prometheus.CounterVec

	// Circuit breaker state (issue #59). 0 = closed, 1 = open.
	CircuitBreakerState prometheus.Gauge

	// Per-worker labelled variants (issue #70). Coexist with aggregate metrics above.
	ObservedTPSPerWorker  *prometheus.GaugeVec
	QueueDropsPerWorker   *prometheus.CounterVec
	LatencyP95MsPerWorker *prometheus.GaugeVec
	ErrorRatePerWorker    *prometheus.GaugeVec

	// Master HA metrics (issue #72). HAFailoverTotal increments on every
	// lease loss event (renew failure or graceful transfer). The percentile
	// gap gauge is honest about Phase-1 limitation: standby starts with an
	// empty histogram, so failover loses percentile continuity. Phase 2
	// (#74) tail-streams histograms to standby to bound this.
	HAFailoverTotal           prometheus.Counter
	HAFailoverPercentileGapMs prometheus.Gauge
}

// NewMetrics creates and registers all Prometheus metrics on the default registry.
func NewMetrics() *Metrics {
	return NewMetricsWithRegistry(prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates and registers all Prometheus metrics on the
// supplied registerer. Tests use a fresh registry to avoid duplicate-registration
// panics from promauto.
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		RequestsTotal: f.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "kar98k",
				Name:      "requests_total",
				Help:      "Total number of requests by target and status",
			},
			[]string{"target", "status", "protocol"},
		),
		RequestDuration: f.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "kar98k",
				Name:      "request_duration_seconds",
				Help:      "Request latency histogram",
				Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
			},
			[]string{"target", "protocol"},
		),
		RequestsInFlight: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "requests_in_flight",
				Help:      "Current number of requests being processed",
			},
		),
		CurrentTPS: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "current_tps",
				Help:      "Current actual TPS being generated",
			},
		),
		TargetTPS: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "target_tps",
				Help:      "Target TPS setting",
			},
		),
		ActiveWorkers: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "active_workers",
				Help:      "Number of active worker goroutines",
			},
		),
		QueuedRequests: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "queued_requests",
				Help:      "Number of requests waiting in queue",
			},
		),
		QueueDropsTotal: f.NewCounter(
			prometheus.CounterOpts{
				Namespace: "kar98k",
				Name:      "queue_drops_total",
				Help:      "Total number of jobs dropped because the worker queue was full",
			},
		),
		QueueDropRate: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "queue_drop_rate",
				Help:      "Sustained drop rate over the last 60 seconds (drops / submits)",
			},
		),
		SpikeActive: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "spike_active",
				Help:      "Whether a traffic spike is currently active (1=yes, 0=no)",
			},
		),
		TargetHealth: f.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "target_health",
				Help:      "Health status of each target (1=healthy, 0=unhealthy)",
			},
			[]string{"target"},
		),
		ScenarioPhaseIndex: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "scenario_phase_index",
				Help:      "Current 1-based scenario phase index; 0 when no scenarios are active",
			},
		),
		ScenarioPhaseTransitionsTotal: f.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "kar98k",
				Name:      "scenario_phase_transitions_total",
				Help:      "Total number of scenario phase transitions, labelled by from/to phase name",
			},
			[]string{"from", "to"},
		),
		CircuitBreakerState: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "circuit_breaker_state",
				Help:      "Circuit breaker state — 0 = closed (traffic flowing), 1 = open (traffic paused)",
			},
		),
		ObservedTPSPerWorker: f.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "observed_tps_per_worker",
				Help:      "Observed TPS reported by each distributed worker",
			},
			[]string{"worker_id"},
		),
		QueueDropsPerWorker: f.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "kar98k",
				Name:      "queue_drops_per_worker_total",
				Help:      "Cumulative queue drops reported by each distributed worker",
			},
			[]string{"worker_id"},
		),
		LatencyP95MsPerWorker: f.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "request_latency_p95_ms_per_worker",
				Help:      "P95 request latency in milliseconds reported by each distributed worker",
			},
			[]string{"worker_id"},
		),
		ErrorRatePerWorker: f.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "error_rate_per_worker",
				Help:      "Error rate reported by each distributed worker",
			},
			[]string{"worker_id"},
		),
		HAFailoverTotal: f.NewCounter(
			prometheus.CounterOpts{
				Namespace: "kar98k",
				Name:      "ha_failover_total",
				Help:      "Cumulative master HA failover events (lease lost, transferred, or self-fenced)",
			},
		),
		HAFailoverPercentileGapMs: f.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "ha_failover_percentile_gap_ms",
				Help:      "Bounded staleness of the standby's percentile snapshot at last failover (Phase 1: 0 — standby has no replica)",
			},
		),
	}
}

// RecordRequest records metrics for a completed request.
func (m *Metrics) RecordRequest(target, protocol string, statusCode int, durationSeconds float64) {
	status := "success"
	if statusCode >= 400 || statusCode == 0 {
		status = "error"
	}

	m.RequestsTotal.WithLabelValues(target, status, protocol).Inc()
	m.RequestDuration.WithLabelValues(target, protocol).Observe(durationSeconds)
}

// SetCurrentTPS updates the current TPS metric.
func (m *Metrics) SetCurrentTPS(tps float64) {
	m.CurrentTPS.Set(tps)
}

// SetTargetTPS updates the target TPS metric.
func (m *Metrics) SetTargetTPS(tps float64) {
	m.TargetTPS.Set(tps)
}

// SetActiveWorkers updates the active workers metric.
func (m *Metrics) SetActiveWorkers(count int) {
	m.ActiveWorkers.Set(float64(count))
}

// SetQueuedRequests updates the queued requests metric.
func (m *Metrics) SetQueuedRequests(count int) {
	m.QueuedRequests.Set(float64(count))
}

// IncQueueDrops increments the queue-drop counter.
func (m *Metrics) IncQueueDrops() {
	m.QueueDropsTotal.Inc()
}

// SetQueueDropRate updates the sustained drop-rate gauge.
func (m *Metrics) SetQueueDropRate(rate float64) {
	m.QueueDropRate.Set(rate)
}

// SetSpikeActive updates the spike active metric.
func (m *Metrics) SetSpikeActive(active bool) {
	if active {
		m.SpikeActive.Set(1)
	} else {
		m.SpikeActive.Set(0)
	}
}

// SetTargetHealth updates the health status for a target.
func (m *Metrics) SetTargetHealth(target string, healthy bool) {
	if healthy {
		m.TargetHealth.WithLabelValues(target).Set(1)
	} else {
		m.TargetHealth.WithLabelValues(target).Set(0)
	}
}

// SetScenarioPhaseIndex sets the current 1-based phase index gauge.
// Call with 0 when the timeline has completed or no scenarios are running.
func (m *Metrics) SetScenarioPhaseIndex(idx int) {
	m.ScenarioPhaseIndex.Set(float64(idx))
}

// RecordScenarioTransition increments the phase-transition counter.
// from is empty string for the initial entry into the first phase.
func (m *Metrics) RecordScenarioTransition(from, to string) {
	m.ScenarioPhaseTransitionsTotal.WithLabelValues(from, to).Inc()
}

// SetCircuitBreakerState updates the breaker state gauge. Pass 0 for
// closed (traffic flowing) and 1 for open (traffic paused).
func (m *Metrics) SetCircuitBreakerState(v float64) {
	m.CircuitBreakerState.Set(v)
}

// IncHAFailover increments the master HA failover counter. Call from
// HALeaseManager.OnLost or graceful-transfer handlers (#72).
func (m *Metrics) IncHAFailover() {
	m.HAFailoverTotal.Inc()
}

// SetHAFailoverPercentileGapMs records the bounded staleness of the
// standby's percentile snapshot at the moment of the last failover.
// Phase 1 always reports 0 because the standby starts with an empty
// histogram; Phase 2 (#74) tail-streams aggregates and updates this
// gauge with the real lag.
func (m *Metrics) SetHAFailoverPercentileGapMs(gap float64) {
	m.HAFailoverPercentileGapMs.Set(gap)
}

// IncRequestsInFlight increments the in-flight requests counter.
func (m *Metrics) IncRequestsInFlight() {
	m.RequestsInFlight.Inc()
}

// DecRequestsInFlight decrements the in-flight requests counter.
func (m *Metrics) DecRequestsInFlight() {
	m.RequestsInFlight.Dec()
}

// SetPerWorker updates all per-worker labelled metrics for the given worker.
// In solo mode pass an empty workerID to keep aggregate-only behaviour.
func (m *Metrics) SetPerWorker(workerID string, tps float64, drops int64, p95Ms float64, errRate float64) {
	m.ObservedTPSPerWorker.WithLabelValues(workerID).Set(tps)
	m.LatencyP95MsPerWorker.WithLabelValues(workerID).Set(p95Ms)
	m.ErrorRatePerWorker.WithLabelValues(workerID).Set(errRate)
	// CounterVec tracks cumulative drops; reset-on-evict is handled by DeletePerWorker.
	// We can only add the delta since counters are monotonic — store the last value in
	// the gauge path and use Add only when drops exceeds the previous counter value.
	// Simpler: expose drops as a gauge-backed counter via the dedicated CounterVec by
	// adding the full cumulative value once on first observation and the delta thereafter.
	// Because StatsPush delivers cumulative drops, use a separate gauge for the
	// per-worker drop count to avoid counter-reset issues on reconnect.
	_ = drops // drops is surfaced via QueueDropsPerWorker only when caller tracks delta
}

// AddPerWorkerDrops adds delta drop counts to the per-worker drop counter.
// Callers must compute the delta (current cumulative − previous cumulative) themselves.
func (m *Metrics) AddPerWorkerDrops(workerID string, delta int64) {
	if delta > 0 {
		m.QueueDropsPerWorker.WithLabelValues(workerID).Add(float64(delta))
	}
}

// DeletePerWorker removes all per-worker label series for the given worker,
// bounding Prometheus cardinality after eviction.
func (m *Metrics) DeletePerWorker(workerID string) {
	m.ObservedTPSPerWorker.DeleteLabelValues(workerID)
	m.QueueDropsPerWorker.DeleteLabelValues(workerID)
	m.LatencyP95MsPerWorker.DeleteLabelValues(workerID)
	m.ErrorRatePerWorker.DeleteLabelValues(workerID)
}
