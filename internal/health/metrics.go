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
	ScenarioPhaseIndex           prometheus.Gauge
	ScenarioPhaseTransitionsTotal *prometheus.CounterVec
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

// IncRequestsInFlight increments the in-flight requests counter.
func (m *Metrics) IncRequestsInFlight() {
	m.RequestsInFlight.Inc()
}

// DecRequestsInFlight decrements the in-flight requests counter.
func (m *Metrics) DecRequestsInFlight() {
	m.RequestsInFlight.Dec()
}
