package rpc

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/hdrbounds"
	"github.com/kar98k/internal/health"
	pb "github.com/kar98k/internal/rpc/proto"
	"github.com/kar98k/internal/worker"
	"github.com/kar98k/pkg/protocol"
)

const (
	workerHeartbeatTimeout = 5 * time.Second
	sendChBuffer           = 4
)

// workerEntry holds live state for one registered worker.
//
// Invariant: sendCh is NEVER closed. When the worker is evicted or unregistered,
// done is closed to signal the RateUpdates stream goroutine to stop. This avoids
// the send-on-closed-channel panic that would occur if SetRate races with eviction.
type workerEntry struct {
	id       string
	addr     string
	lastBeat time.Time

	// Latest snapshot pushed by the worker via Stats stream.
	lastTPS   float64
	drops     int64 // cumulative drops as of last StatsPush
	errorRate float64

	// sendCh carries non-blocking rate updates to the worker's stream goroutine.
	sendCh chan *pb.RateUpdate
	// done is closed (once) when the worker is removed so the stream goroutine exits.
	done chan struct{}
}

// RegistryOption configures a WorkerRegistry.
type RegistryOption func(*WorkerRegistry)

// WithMetrics attaches a Metrics instance to the registry so per-worker
// Prometheus label series are updated on every StatsPush and deleted on eviction.
// Solo mode never calls NewWorkerRegistry, so passing nil is safe (no-op).
func WithMetrics(m *health.Metrics) RegistryOption {
	return func(r *WorkerRegistry) {
		r.metrics = m
	}
}

// WorkerRegistry is a thread-safe map of active workers. It implements
// the controller.PoolFacade interface so the master controller can call
// SetRate/Active/QueueSize/etc. without knowing it is talking to a
// distributed fleet instead of a local pool.
type WorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*workerEntry

	// Aggregate HDR histograms — merged on each Stats push.
	latMu      sync.Mutex
	globalRaw  *hdrhistogram.Histogram
	globalCorr *hdrhistogram.Histogram

	// totalDrops is the sum of w.drops across all live workers, recomputed on
	// each RecordStats call so it never grows unboundedly.
	totalDrops int64

	// liveCount is updated atomically on Register/Unregister/sweep so Active()
	// avoids allocating a slice on every controller tick.
	liveCount int32

	stopCh chan struct{}

	// metrics is optional; when set, per-worker Prometheus label series are
	// maintained. prevDrops tracks the last-reported cumulative drop count per
	// worker so we can compute deltas for the monotonic CounterVec.
	metrics   *health.Metrics
	prevDrops map[string]int64

	// currentPhase carries the scenario phase the master is in. SetPhase
	// (called by the controller's ScenarioRunner) updates it; SetRate
	// reads it on every broadcast so each pb.RateUpdate carries the
	// phase tag. atomic.Value because SetRate fires every 100ms while
	// SetPhase only fires on phase boundaries — keeping them lock-free
	// avoids contention with RecordStats. See #68.
	currentPhase atomic.Value // string

	// Per-phase HdrHistogram aggregates keyed by phase_name. Both maps
	// are guarded by latMu (alongside globalRaw/globalCorr). Empty key
	// "" is the default-phase bucket used when v1 workers send pushes
	// without a phase tag, or when scenarios are disabled.
	phaseRaw  map[string]*hdrhistogram.Histogram
	phaseCorr map[string]*hdrhistogram.Histogram
}

// NewWorkerRegistry constructs a registry and starts the heartbeat sweeper.
func NewWorkerRegistry(opts ...RegistryOption) *WorkerRegistry {
	r := &WorkerRegistry{
		workers:    make(map[string]*workerEntry),
		globalRaw:  hdrhistogram.New(hdrbounds.Min, hdrbounds.Max, int(hdrbounds.SigFigs)),
		globalCorr: hdrhistogram.New(hdrbounds.Min, hdrbounds.Max, int(hdrbounds.SigFigs)),
		stopCh:     make(chan struct{}),
		prevDrops:  make(map[string]int64),
		phaseRaw:   make(map[string]*hdrhistogram.Histogram),
		phaseCorr:  make(map[string]*hdrhistogram.Histogram),
	}
	r.currentPhase.Store("")
	for _, o := range opts {
		o(r)
	}
	go r.sweepLoop()
	return r
}

// Register adds or replaces a worker entry. Returns the send channel.
// If a prior entry exists for the same addr (reconnect case), it is evicted
// before the new entry is inserted so liveCount never double-counts.
func (r *WorkerRegistry) Register(id, addr string) chan *pb.RateUpdate {
	ch := make(chan *pb.RateUpdate, sendChBuffer)
	done := make(chan struct{})

	var staleID string
	r.mu.Lock()
	// Scan for a stale entry sharing the same addr but a different id.
	// This happens when a worker reconnects and receives a new ID from nextWorkerID.
	for eid, e := range r.workers {
		if e.addr == addr && eid != id {
			close(e.done)
			delete(r.workers, eid)
			delete(r.prevDrops, eid)
			atomic.AddInt32(&r.liveCount, -1)
			staleID = eid
			log.Printf("[registry] evicted stale entry id=%s on reconnect from addr=%s", eid, addr)
			break
		}
	}
	r.workers[id] = &workerEntry{
		id:       id,
		addr:     addr,
		lastBeat: time.Now(),
		sendCh:   ch,
		done:     done,
	}
	atomic.AddInt32(&r.liveCount, 1)
	r.mu.Unlock()

	if staleID != "" && r.metrics != nil {
		r.metrics.DeletePerWorker(staleID)
	}
	log.Printf("[registry] worker registered: id=%s addr=%s", id, addr)
	return ch
}

// Unregister removes a worker and signals its stream goroutine to exit.
func (r *WorkerRegistry) Unregister(id string) {
	r.mu.Lock()
	if w, ok := r.workers[id]; ok {
		close(w.done)
		delete(r.workers, id)
		delete(r.prevDrops, id)
		atomic.AddInt32(&r.liveCount, -1)
	}
	r.mu.Unlock()
	if r.metrics != nil {
		r.metrics.DeletePerWorker(id)
	}
	log.Printf("[registry] worker unregistered: id=%s", id)
}

// RecordStats merges a stats push into the registry aggregate.
func (r *WorkerRegistry) RecordStats(push *pb.StatsPush) {
	r.mu.Lock()
	w, ok := r.workers[push.WorkerId]
	if ok {
		w.lastBeat = time.Now()
		w.lastTPS = push.ObservedTps
		w.errorRate = push.ErrorRate
		// QueueDrops from workers is cumulative, not a delta — store as-is.
		// Recompute the aggregate by summing so it never grows unboundedly.
		w.drops = push.QueueDrops
		var total int64
		for _, e := range r.workers {
			total += e.drops
		}
		atomic.StoreInt64(&r.totalDrops, total)
	}
	r.mu.Unlock()

	if !ok {
		return
	}

	// Decode the raw histogram snapshot to compute per-worker p95 before merging.
	// Both global and per-phase aggregates are updated under latMu so they stay
	// consistent — a reader that grabs latMu sees both at the same merge point.
	var p95Ms float64
	if len(push.HdrRaw) > 0 {
		if snap, err := hdrhistogram.Decode(push.HdrRaw); err == nil {
			if snap.TotalCount() > 0 {
				// Histogram stores values in microseconds; convert to ms.
				p95Ms = float64(snap.ValueAtQuantile(95)) / 1000.0
			}
			r.latMu.Lock()
			r.globalRaw.Merge(snap)
			r.mergePhaseLocked(r.phaseRaw, push.PhaseName, snap)
			r.latMu.Unlock()
		}
	}
	if len(push.HdrCorrected) > 0 {
		if snap, err := hdrhistogram.Decode(push.HdrCorrected); err == nil {
			r.latMu.Lock()
			r.globalCorr.Merge(snap)
			r.mergePhaseLocked(r.phaseCorr, push.PhaseName, snap)
			r.latMu.Unlock()
		}
	}

	// Update per-worker Prometheus labels when metrics are wired in.
	if r.metrics != nil {
		id := push.WorkerId
		r.metrics.SetPerWorker(id, push.ObservedTps, push.QueueDrops, p95Ms, push.ErrorRate)

		r.mu.Lock()
		prev := r.prevDrops[id]
		var delta int64
		if push.QueueDrops < prev {
			// Worker counter reset (restart/reconnect). Treat current value as
			// new baseline; emit no drops for this interval to avoid negative delta.
			r.prevDrops[id] = push.QueueDrops
			delta = 0
		} else {
			delta = push.QueueDrops - prev
			r.prevDrops[id] = push.QueueDrops
		}
		r.mu.Unlock()

		r.metrics.AddPerWorkerDrops(id, delta)
	}
}

// SetRate distributes tps evenly across live workers (controller.PoolFacade).
// The broadcast also carries the current scenario phase set by SetPhase so
// workers can flip their per-phase histograms in lock-step with master.
func (r *WorkerRegistry) SetRate(tps float64) {
	r.mu.RLock()
	live := r.liveWorkers()
	n := len(live)
	r.mu.RUnlock()

	if n == 0 {
		return
	}
	perWorker := tps / float64(n)
	phase, _ := r.currentPhase.Load().(string)
	update := &pb.RateUpdate{TargetTps: perWorker, Command: pb.Command_NONE, PhaseName: phase}
	for _, w := range live {
		select {
		case w.sendCh <- update:
		default:
			// channel full — worker will catch the next tick
		}
	}
}

// SetPhase records the active scenario phase. Subsequent SetRate broadcasts
// tag the pb.RateUpdate with this name so workers know when to flip their
// per-phase histograms. Empty string means "default phase" / "no scenarios".
// Safe to call concurrently with SetRate. See #68.
func (r *WorkerRegistry) SetPhase(phase string) {
	r.currentPhase.Store(phase)
}

// mergePhaseLocked merges snap into the per-phase histogram for phaseName,
// creating one on first sight. Re-entering a previously seen phase MERGES
// (does NOT reset) so phase aggregates accumulate across re-entries —
// matches solo internal/script/phase.go:46-50 name-only re-entry semantics.
// Caller must hold r.latMu.
func (r *WorkerRegistry) mergePhaseLocked(phaseMap map[string]*hdrhistogram.Histogram, phaseName string, snap *hdrhistogram.Histogram) {
	h, ok := phaseMap[phaseName]
	if !ok {
		h = hdrhistogram.New(hdrbounds.Min, hdrbounds.Max, int(hdrbounds.SigFigs))
		phaseMap[phaseName] = h
	}
	h.Merge(snap)
}

// LatencyPercentileByPhase returns a percentile for the per-phase aggregate
// (raw or coordinated-omission corrected). Returns 0 when the phase is
// unknown or no samples have been recorded yet. Phase "" is the default
// bucket used by v1 workers / non-scenarios runs. See #68.
func (r *WorkerRegistry) LatencyPercentileByPhase(phaseName string, percentile float64, corrected bool) float64 {
	r.latMu.Lock()
	defer r.latMu.Unlock()

	m := r.phaseRaw
	if corrected {
		m = r.phaseCorr
	}
	h, ok := m[phaseName]
	if !ok || h.TotalCount() == 0 {
		return 0
	}
	return float64(h.ValueAtQuantile(percentile)) / 1000.0
}

// PhaseLatency is a snapshot of one phase's percentiles for dashboard rendering.
type PhaseLatency struct {
	Phase   string  `json:"phase"`
	Samples int64   `json:"samples"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
}

// PhaseSnapshot returns one PhaseLatency per known phase from the raw
// (non-CO-corrected) per-phase aggregate. Useful for the dashboard's
// per-phase panel. Order is undefined; sort caller-side if needed.
func (r *WorkerRegistry) PhaseSnapshot() []PhaseLatency {
	r.latMu.Lock()
	defer r.latMu.Unlock()

	out := make([]PhaseLatency, 0, len(r.phaseRaw))
	for name, h := range r.phaseRaw {
		if h.TotalCount() == 0 {
			continue
		}
		out = append(out, PhaseLatency{
			Phase:   name,
			Samples: h.TotalCount(),
			P95Ms:   float64(h.ValueAtQuantile(95)) / 1000.0,
			P99Ms:   float64(h.ValueAtQuantile(99)) / 1000.0,
		})
	}
	return out
}

// Active returns the number of live workers (controller.PoolFacade).
func (r *WorkerRegistry) Active() int {
	return int(atomic.LoadInt32(&r.liveCount))
}

// QueueSize returns a placeholder; individual queue sizes are not aggregated (controller.PoolFacade).
func (r *WorkerRegistry) QueueSize() int { return 0 }

// TotalDrops returns the lifetime aggregate drop count (controller.PoolFacade).
func (r *WorkerRegistry) TotalDrops() int64 {
	return atomic.LoadInt64(&r.totalDrops)
}

// DropRate returns the aggregate sustained drop rate (controller.PoolFacade).
// TODO(#47): drop rate computed master-side requires submits-per-tick from workers; not on wire yet.
func (r *WorkerRegistry) DropRate() float64 {
	return 0
}

// LatencyPercentile returns a percentile from the merged global histogram (controller.PoolFacade).
func (r *WorkerRegistry) LatencyPercentile(percentile float64, corrected bool) float64 {
	r.latMu.Lock()
	defer r.latMu.Unlock()

	h := r.globalRaw
	if corrected {
		h = r.globalCorr
	}
	if h.TotalCount() == 0 {
		return 0
	}
	return float64(h.ValueAtQuantile(percentile)) / 1000.0
}

// ErrorRate returns the average error rate across live workers (controller.PoolFacade).
func (r *WorkerRegistry) ErrorRate() float64 {
	r.mu.RLock()
	live := r.liveWorkers()
	r.mu.RUnlock()

	if len(live) == 0 {
		return 0
	}
	var sum float64
	for _, w := range live {
		sum += w.errorRate
	}
	return sum / float64(len(live))
}

// WorkerRow is a snapshot of one worker's state for dashboard rendering.
type WorkerRow struct {
	ID             string  `json:"id"`
	Addr           string  `json:"addr"`
	LastBeatAgoSec float64 `json:"last_beat_ago_sec"`
	CurrentTPS     float64 `json:"current_tps"`
	Drops          int64   `json:"drops"`
	ErrorRate      float64 `json:"error_rate"`
}

// Snapshot returns a stable slice of WorkerRow for the dashboard.
func (r *WorkerRegistry) Snapshot() []WorkerRow {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rows := make([]WorkerRow, 0, len(r.workers))
	for _, w := range r.workers {
		rows = append(rows, WorkerRow{
			ID:             w.id,
			Addr:           w.addr,
			LastBeatAgoSec: time.Since(w.lastBeat).Seconds(),
			CurrentTPS:     w.lastTPS,
			Drops:          w.drops,
			ErrorRate:      w.errorRate,
		})
	}
	return rows
}

// Submit satisfies PoolFacade. The master never submits jobs locally;
// generateLoop is gated off via controller.SetDistributedMode(true).
func (r *WorkerRegistry) Submit(_ worker.Job) bool { return true }

// GetClient satisfies PoolFacade. Returns nil — the master holds no
// local protocol clients; workers maintain their own connections.
func (r *WorkerRegistry) GetClient(_ config.Protocol) protocol.Client { return nil }

// GetSendCh returns the send channel and done channel for a registered worker.
// Returns (nil, nil, false) if the worker is not found.
func (r *WorkerRegistry) GetSendCh(id string) (<-chan *pb.RateUpdate, <-chan struct{}, bool) {
	r.mu.RLock()
	w, ok := r.workers[id]
	r.mu.RUnlock()
	if !ok {
		return nil, nil, false
	}
	return w.sendCh, w.done, true
}

// Stop shuts down the background sweeper.
func (r *WorkerRegistry) Stop() {
	close(r.stopCh)
}

// liveWorkers returns entries whose last heartbeat is within the timeout.
// Caller must hold r.mu (at least RLock).
func (r *WorkerRegistry) liveWorkers() []*workerEntry {
	cutoff := time.Now().Add(-workerHeartbeatTimeout)
	out := make([]*workerEntry, 0, len(r.workers))
	for _, w := range r.workers {
		if w.lastBeat.After(cutoff) {
			out = append(out, w)
		}
	}
	return out
}

// sweepLoop evicts workers that have not sent a heartbeat within the timeout.
func (r *WorkerRegistry) sweepLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *WorkerRegistry) sweep() {
	cutoff := time.Now().Add(-workerHeartbeatTimeout)
	var evicted []string
	r.mu.Lock()
	for id, w := range r.workers {
		if !w.lastBeat.After(cutoff) {
			close(w.done)
			delete(r.workers, id)
			delete(r.prevDrops, id)
			atomic.AddInt32(&r.liveCount, -1)
			log.Printf("[registry] evicted stale worker id=%s (last beat %.1fs ago)",
				id, time.Since(w.lastBeat).Seconds())
			evicted = append(evicted, id)
		}
	}
	r.mu.Unlock()
	if r.metrics != nil {
		for _, id := range evicted {
			r.metrics.DeletePerWorker(id)
		}
	}
}
