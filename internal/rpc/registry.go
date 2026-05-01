package rpc

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/kar98k/internal/config"
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

// WorkerRegistry is a thread-safe map of active workers. It implements
// the controller.PoolFacade interface so the master controller can call
// SetRate/Active/QueueSize/etc. without knowing it is talking to a
// distributed fleet instead of a local pool.
type WorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]*workerEntry

	// Aggregate HDR histograms — merged on each Stats push.
	latMu        sync.Mutex
	globalRaw    *hdrhistogram.Histogram
	globalCorr   *hdrhistogram.Histogram

	// totalDrops is the sum of w.drops across all live workers, recomputed on
	// each RecordStats call so it never grows unboundedly.
	totalDrops int64

	// liveCount is updated atomically on Register/Unregister/sweep so Active()
	// avoids allocating a slice on every controller tick.
	liveCount int32

	stopCh chan struct{}
}

// NewWorkerRegistry constructs a registry and starts the heartbeat sweeper.
func NewWorkerRegistry() *WorkerRegistry {
	r := &WorkerRegistry{
		workers:    make(map[string]*workerEntry),
		globalRaw:  hdrhistogram.New(BoundsMin, BoundsMax, int(BoundsSigFigs)),
		globalCorr: hdrhistogram.New(BoundsMin, BoundsMax, int(BoundsSigFigs)),
		stopCh:     make(chan struct{}),
	}
	go r.sweepLoop()
	return r
}

// Register adds or replaces a worker entry. Returns the assigned worker ID.
func (r *WorkerRegistry) Register(id, addr string) chan *pb.RateUpdate {
	ch := make(chan *pb.RateUpdate, sendChBuffer)
	done := make(chan struct{})
	r.mu.Lock()
	r.workers[id] = &workerEntry{
		id:       id,
		addr:     addr,
		lastBeat: time.Now(),
		sendCh:   ch,
		done:     done,
	}
	atomic.AddInt32(&r.liveCount, 1)
	r.mu.Unlock()
	log.Printf("[registry] worker registered: id=%s addr=%s", id, addr)
	return ch
}

// Unregister removes a worker and signals its stream goroutine to exit.
func (r *WorkerRegistry) Unregister(id string) {
	r.mu.Lock()
	if w, ok := r.workers[id]; ok {
		close(w.done)
		delete(r.workers, id)
		atomic.AddInt32(&r.liveCount, -1)
	}
	r.mu.Unlock()
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

	// Merge histogram snapshots under latMu. Workers send snapshot+reset
	// deltas so we can safely merge without double-counting.
	if len(push.HdrRaw) > 0 {
		if snap, err := hdrhistogram.Decode(push.HdrRaw); err == nil {
			r.latMu.Lock()
			r.globalRaw.Merge(snap)
			r.latMu.Unlock()
		}
	}
	if len(push.HdrCorrected) > 0 {
		if snap, err := hdrhistogram.Decode(push.HdrCorrected); err == nil {
			r.latMu.Lock()
			r.globalCorr.Merge(snap)
			r.latMu.Unlock()
		}
	}
}

// SetRate distributes tps evenly across live workers (controller.PoolFacade).
func (r *WorkerRegistry) SetRate(tps float64) {
	r.mu.RLock()
	live := r.liveWorkers()
	n := len(live)
	r.mu.RUnlock()

	if n == 0 {
		return
	}
	perWorker := tps / float64(n)
	update := &pb.RateUpdate{TargetTps: perWorker, Command: pb.Command_NONE}
	for _, w := range live {
		select {
		case w.sendCh <- update:
		default:
			// channel full — worker will catch the next tick
		}
	}
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
	r.mu.Lock()
	for id, w := range r.workers {
		if !w.lastBeat.After(cutoff) {
			close(w.done)
			delete(r.workers, id)
			atomic.AddInt32(&r.liveCount, -1)
			log.Printf("[registry] evicted stale worker id=%s (last beat %.1fs ago)",
				id, time.Since(w.lastBeat).Seconds())
		}
	}
	r.mu.Unlock()
}
