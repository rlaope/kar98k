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
	SpikeActive      prometheus.Gauge
	TargetHealth     *prometheus.GaugeVec
}

// NewMetrics creates and registers all Prometheus metrics.
func NewMetrics() *Metrics {
	return &Metrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "kar98k",
				Name:      "requests_total",
				Help:      "Total number of requests by target and status",
			},
			[]string{"target", "status", "protocol"},
		),
		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "kar98k",
				Name:      "request_duration_seconds",
				Help:      "Request latency histogram",
				Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
			},
			[]string{"target", "protocol"},
		),
		RequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "requests_in_flight",
				Help:      "Current number of requests being processed",
			},
		),
		CurrentTPS: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "current_tps",
				Help:      "Current actual TPS being generated",
			},
		),
		TargetTPS: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "target_tps",
				Help:      "Target TPS setting",
			},
		),
		ActiveWorkers: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "active_workers",
				Help:      "Number of active worker goroutines",
			},
		),
		QueuedRequests: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "queued_requests",
				Help:      "Number of requests waiting in queue",
			},
		),
		SpikeActive: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "spike_active",
				Help:      "Whether a traffic spike is currently active (1=yes, 0=no)",
			},
		),
		TargetHealth: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "kar98k",
				Name:      "target_health",
				Help:      "Health status of each target (1=healthy, 0=unhealthy)",
			},
			[]string{"target"},
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

// IncRequestsInFlight increments the in-flight requests counter.
func (m *Metrics) IncRequestsInFlight() {
	m.RequestsInFlight.Inc()
}

// DecRequestsInFlight decrements the in-flight requests counter.
func (m *Metrics) DecRequestsInFlight() {
	m.RequestsInFlight.Dec()
}
