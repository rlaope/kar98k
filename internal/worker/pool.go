package worker

import (
	"context"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/health"
	"github.com/kar98k/pkg/protocol"
	"golang.org/x/time/rate"
)

// dropWindow is the rolling-window length used for sustained-rate
// tracking and the heuristic warning. One slot per second.
const dropWindow = 60

// dropWarnThreshold is the sustained drop fraction (drops/submits over
// dropWindow) that triggers the rate-limited operator warning.
const dropWarnThreshold = 0.01

// dropWarnCooldown is the minimum gap between repeated warnings so we
// don't spam the log when the queue stays saturated.
const dropWarnCooldown = 5 * time.Minute

// Latency histogram bounds: 1µs..60s with 3 significant digits, matching
// internal/script/builtins.go and internal/discovery/analyzer.go so the
// three latency surfaces stay comparable.
const (
	latencyHistMin    = 1
	latencyHistMax    = 60_000_000
	latencyHistSigFig = 3
)

// Job represents a single request job.
type Job struct {
	Target config.Target
	Client protocol.Client
}

// Pool manages a pool of worker goroutines.
type Pool struct {
	cfg      config.Worker
	metrics  *health.Metrics
	clients  map[config.Protocol]protocol.Client
	limiter  *rate.Limiter
	jobs     chan Job
	wg       sync.WaitGroup
	active   int64
	cancel   context.CancelFunc
	mu       sync.RWMutex
	tpsCount int64
	lastTPS  time.Time

	// Drop tracking. submitCount/dropCount are bumped from the hot path
	// via atomics; the ring buffers are owned by measureTPS.
	submitCount  int64
	dropCount    int64
	totalDrops   int64
	dropHistory  [dropWindow]int64
	subHistory   [dropWindow]int64
	histIdx      int
	dropRate     uint64 // float64 bits, set/read atomically
	lastDropWarn time.Time

	// Request/error tracking — same per-second ring buffer pattern as
	// drops, used by the circuit breaker (#59) to compute sustained
	// error rate. requestSlot/errorSlot are bumped from the hot path
	// via atomics; the histories are owned by measureTPS.
	requestSlot   int64
	errorSlot     int64
	requestHist   [dropWindow]int64
	errorHist     [dropWindow]int64
	errorRateBits uint64 // float64 bits, set/read atomically

	// paused is set by Pause/Resume. While paused, processJob returns
	// without firing the request — workers stay alive, the rate limiter
	// keeps its setting, but no traffic flows.
	paused atomic.Bool

	// Latency tracking. hdrhistogram.Histogram is not goroutine-safe,
	// so all access is serialised through latMu.
	latMu        sync.Mutex
	latRaw       *hdrhistogram.Histogram // observed latency (t_done - t_sent)
	latCorrected *hdrhistogram.Histogram // CO-corrected via RecordCorrectedValue
}

// NewPool creates a new worker pool.
func NewPool(cfg config.Worker, metrics *health.Metrics) *Pool {
	// Initialize protocol clients
	clientCfg := protocol.ClientConfig{
		MaxIdleConns:    cfg.MaxIdleConns,
		IdleConnTimeout: cfg.IdleConnTimeout,
		TLSInsecure:     true,
	}

	clients := map[config.Protocol]protocol.Client{
		config.ProtocolHTTP:  protocol.NewHTTPClient(clientCfg),
		config.ProtocolHTTP2: protocol.NewHTTP2Client(clientCfg),
		config.ProtocolGRPC:  protocol.NewGRPCClient(clientCfg),
	}

	return &Pool{
		cfg:          cfg,
		metrics:      metrics,
		clients:      clients,
		limiter:      rate.NewLimiter(rate.Limit(100), 1), // Initial rate, will be updated
		jobs:         make(chan Job, cfg.QueueSize),
		lastTPS:      time.Now(),
		latRaw:       hdrhistogram.New(latencyHistMin, latencyHistMax, latencyHistSigFig),
		latCorrected: hdrhistogram.New(latencyHistMin, latencyHistMax, latencyHistSigFig),
	}
}

// Start launches the worker pool.
func (p *Pool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)

	// Start worker goroutines
	for i := 0; i < p.cfg.PoolSize; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}

	// Start TPS measurement goroutine
	go p.measureTPS(ctx)

	log.Printf("[worker] started %d workers with queue size %d", p.cfg.PoolSize, p.cfg.QueueSize)
}

// worker is the main worker goroutine.
func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			p.processJob(ctx, job)
		}
	}
}

// processJob executes a single job.
func (p *Pool) processJob(ctx context.Context, job Job) {
	// Wait for rate limiter
	if err := p.limiter.Wait(ctx); err != nil {
		return // Context cancelled
	}

	// Circuit breaker pause: keep the worker goroutine alive (and the
	// connection pool warm) but don't actually send anything. The rate
	// limiter's setting is preserved so resume is instantaneous.
	if p.paused.Load() {
		return
	}

	atomic.AddInt64(&p.active, 1)
	p.metrics.IncRequestsInFlight()
	defer func() {
		atomic.AddInt64(&p.active, -1)
		p.metrics.DecRequestsInFlight()
	}()

	// Update active workers metric
	p.metrics.SetActiveWorkers(int(atomic.LoadInt64(&p.active)))

	// Build request
	req := &protocol.Request{
		URL:     job.Target.URL,
		Method:  job.Target.Method,
		Headers: job.Target.Headers,
		Body:    []byte(job.Target.Body),
		Timeout: job.Target.Timeout,
	}

	// Execute request
	resp := job.Client.Do(ctx, req)

	// Record metrics
	p.metrics.RecordRequest(
		job.Target.Name,
		string(job.Target.Protocol),
		resp.StatusCode,
		resp.Duration.Seconds(),
	)

	p.recordLatency(resp.Duration)

	// Increment TPS counter and feed the per-second request/error
	// slots so the breaker can compute a sustained error rate.
	atomic.AddInt64(&p.tpsCount, 1)
	atomic.AddInt64(&p.requestSlot, 1)
	if resp.StatusCode >= 500 || resp.StatusCode == 0 {
		atomic.AddInt64(&p.errorSlot, 1)
	}
}

// recordLatency feeds an observed request duration into both the raw
// and the coordinated-omission-corrected histograms. The expected
// inter-request interval is derived from the rate limiter's current
// limit. If the limit is unset (e.g. before SetRate has been called)
// the corrected histogram receives the raw value with no synthesis.
//
// Coordinated-omission correction: when the server stalls for many
// intervals, the *raw* histogram only sees the single slow call that
// eventually returned, while the *corrected* histogram synthesises
// samples for every slot that should have been issued during the
// stall. Surfacing both lets a reader tell whether a tail-latency
// regression is real or a measurement artifact.
func (p *Pool) recordLatency(observed time.Duration) {
	micros := observed.Microseconds()
	if micros < latencyHistMin {
		micros = latencyHistMin
	} else if micros > latencyHistMax {
		micros = latencyHistMax
	}

	expectedMicros := int64(0)
	if r := float64(p.limiter.Limit()); r > 0 {
		expectedMicros = int64(1_000_000 / r)
	}

	p.latMu.Lock()
	_ = p.latRaw.RecordValue(micros)
	if expectedMicros > 0 {
		_ = p.latCorrected.RecordCorrectedValue(micros, expectedMicros)
	} else {
		_ = p.latCorrected.RecordValue(micros)
	}
	p.latMu.Unlock()
}

// measureTPS periodically calculates and updates the actual TPS, and
// also rolls the drop ring buffer forward and re-computes the sustained
// drop rate.
func (p *Pool) measureTPS(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count := atomic.SwapInt64(&p.tpsCount, 0)
			p.metrics.SetCurrentTPS(float64(count))

			drops := atomic.SwapInt64(&p.dropCount, 0)
			submits := atomic.SwapInt64(&p.submitCount, 0)
			p.recordDropSlot(drops, submits)

			reqs := atomic.SwapInt64(&p.requestSlot, 0)
			errs := atomic.SwapInt64(&p.errorSlot, 0)
			p.recordErrorSlot(errs, reqs)
		}
	}
}

// recordErrorSlot writes one second's request/error counts into the
// ring buffer and recomputes the sustained error rate over the window.
// Mirrors recordDropSlot's pattern so the breaker has a metric shape
// it can poll without holding the pool's mutex on the hot path.
func (p *Pool) recordErrorSlot(errs, reqs int64) {
	p.mu.Lock()
	// histIdx is shared with the drop ring buffer; it advances in
	// recordDropSlot which is called immediately before us, so we read
	// the *just-incremented* index minus one.
	idx := (p.histIdx + dropWindow - 1) % dropWindow
	p.requestHist[idx] = reqs
	p.errorHist[idx] = errs

	var winReqs, winErrs int64
	for i := 0; i < dropWindow; i++ {
		winReqs += p.requestHist[i]
		winErrs += p.errorHist[i]
	}
	p.mu.Unlock()

	rate := 0.0
	if winReqs > 0 {
		rate = float64(winErrs) / float64(winReqs)
	}
	atomic.StoreUint64(&p.errorRateBits, math.Float64bits(rate))
}

// ErrorRate returns the sustained error rate (errors / total requests)
// over the last dropWindow seconds. Returns 0 before the first
// sampling tick fires. The breaker uses this to detect prolonged
// failure conditions without flapping on single-sample noise.
func (p *Pool) ErrorRate() float64 {
	return math.Float64frombits(atomic.LoadUint64(&p.errorRateBits))
}

// Pause halts request execution without tearing down workers or the
// connection pool. Workers continue dequeuing jobs from the channel
// but processJob returns early, leaving the rate limiter and clients
// untouched. Resume restores normal flow instantly.
func (p *Pool) Pause() {
	p.paused.Store(true)
}

// Resume re-enables request execution after Pause.
func (p *Pool) Resume() {
	p.paused.Store(false)
}

// IsPaused reports whether the pool is currently in the paused state.
func (p *Pool) IsPaused() bool {
	return p.paused.Load()
}

// recordDropSlot writes one second's worth of drop counters into the
// ring buffer, recomputes the sustained rate over the window, and
// emits a heuristic warning when drops stay >dropWarnThreshold for the
// whole window.
func (p *Pool) recordDropSlot(drops, submits int64) {
	p.mu.Lock()
	idx := p.histIdx
	p.dropHistory[idx] = drops
	p.subHistory[idx] = submits
	p.histIdx = (idx + 1) % dropWindow

	var winDrops, winSubmits int64
	for i := 0; i < dropWindow; i++ {
		winDrops += p.dropHistory[i]
		winSubmits += p.subHistory[i]
	}

	var rate float64
	totalAttempts := winDrops + winSubmits
	if totalAttempts > 0 {
		rate = float64(winDrops) / float64(totalAttempts)
	}
	atomic.StoreUint64(&p.dropRate, math.Float64bits(rate))

	// Warn when *every* slot in the window has been written (full minute
	// of data) and the sustained drop rate is over threshold. Cooldown
	// avoids log spam during prolonged saturation.
	full := true
	for i := 0; i < dropWindow; i++ {
		if p.dropHistory[i] == 0 && p.subHistory[i] == 0 {
			full = false
			break
		}
	}
	shouldWarn := full && rate > dropWarnThreshold &&
		time.Since(p.lastDropWarn) >= dropWarnCooldown
	if shouldWarn {
		p.lastDropWarn = time.Now()
	}
	currentTPS := float64(winSubmits) / float64(dropWindow)
	p.mu.Unlock()

	p.metrics.SetQueueDropRate(rate)

	if shouldWarn {
		suggested := suggestQueueSize(currentTPS)
		log.Printf("[worker] WARNING: sustained queue-drop rate %.2f%% over last %ds "+
			"(drops=%d, submits=%d). Current queue_size=%d; consider raising to %d.",
			rate*100, dropWindow, winDrops, winSubmits, p.cfg.QueueSize, suggested)
	}
}

// suggestQueueSize rounds up to the next power of two of (tps * 10),
// matching the in-tree heuristic of "10s of headroom at the current
// observed TPS".
func suggestQueueSize(tps float64) int {
	target := int(tps * 10)
	if target < 1 {
		target = 1
	}
	pow := 1
	for pow < target {
		pow <<= 1
	}
	return pow
}

// Submit adds a job to the queue. Returns false when the queue is full
// (the job is dropped, the queue-drop counter is incremented, and the
// caller is expected to back off).
func (p *Pool) Submit(job Job) bool {
	select {
	case p.jobs <- job:
		atomic.AddInt64(&p.submitCount, 1)
		p.metrics.SetQueuedRequests(len(p.jobs))
		return true
	default:
		atomic.AddInt64(&p.dropCount, 1)
		atomic.AddInt64(&p.totalDrops, 1)
		p.metrics.IncQueueDrops()
		return false
	}
}

// SetRate updates the rate limiter.
func (p *Pool) SetRate(tps float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.limiter.SetLimit(rate.Limit(tps))
	p.limiter.SetBurst(int(tps / 10)) // Burst of 10% of TPS
	if p.limiter.Burst() < 1 {
		p.limiter.SetBurst(1)
	}

	p.metrics.SetTargetTPS(tps)
}

// GetClient returns the client for a given protocol.
func (p *Pool) GetClient(proto config.Protocol) protocol.Client {
	client, ok := p.clients[proto]
	if !ok {
		return p.clients[config.ProtocolHTTP]
	}
	return client
}

// Active returns the number of currently active workers.
func (p *Pool) Active() int {
	return int(atomic.LoadInt64(&p.active))
}

// QueueSize returns the current queue length.
func (p *Pool) QueueSize() int {
	return len(p.jobs)
}

// TotalDrops returns the lifetime count of dropped jobs.
func (p *Pool) TotalDrops() int64 {
	return atomic.LoadInt64(&p.totalDrops)
}

// DropRate returns the sustained drop rate over the last dropWindow
// seconds, as drops/(drops+submits). Returns 0 before the first tick.
func (p *Pool) DropRate() float64 {
	return math.Float64frombits(atomic.LoadUint64(&p.dropRate))
}

// LatencyPercentile returns the requested percentile (0..100) in
// milliseconds. When corrected is true, the coordinated-omission
// corrected histogram is read; otherwise the raw observed histogram is
// returned. Returns 0 when no samples are recorded.
func (p *Pool) LatencyPercentile(percentile float64, corrected bool) float64 {
	p.latMu.Lock()
	defer p.latMu.Unlock()

	hist := p.latRaw
	if corrected {
		hist = p.latCorrected
	}
	if hist.TotalCount() == 0 {
		return 0
	}
	return float64(hist.ValueAtQuantile(percentile)) / 1000.0
}

// LatencySamples returns (raw, corrected) sample counts. The corrected
// count is typically larger because RecordCorrectedValue synthesises
// samples for each missed slot during a stall.
func (p *Pool) LatencySamples() (rawN, correctedN int64) {
	p.latMu.Lock()
	defer p.latMu.Unlock()
	return p.latRaw.TotalCount(), p.latCorrected.TotalCount()
}

// SnapshotAndResetHistograms encodes both latency histograms to their
// wire format (HdrHistogram V2 compressed) and resets them to zero.
// The snapshot is atomic with respect to recordLatency — no samples are
// lost or double-counted across concurrent calls.
func (p *Pool) SnapshotAndResetHistograms() (rawBytes, corrBytes []byte, err error) {
	p.latMu.Lock()
	defer p.latMu.Unlock()

	rawBytes, err = p.latRaw.Encode(hdrhistogram.V2CompressedEncodingCookieBase)
	if err != nil {
		return nil, nil, err
	}
	p.latRaw.Reset()

	corrBytes, err = p.latCorrected.Encode(hdrhistogram.V2CompressedEncodingCookieBase)
	if err != nil {
		return nil, nil, err
	}
	p.latCorrected.Reset()

	return rawBytes, corrBytes, nil
}

// Stop gracefully stops the worker pool.
func (p *Pool) Stop() {
	if p.cancel != nil {
		p.cancel()
	}

	close(p.jobs)
	p.wg.Wait()

	// Close all clients
	for _, client := range p.clients {
		client.Close()
	}

	log.Printf("[worker] all workers stopped")
}

// Drain waits for all in-flight requests to complete with a timeout.
func (p *Pool) Drain(timeout time.Duration) {
	deadline := time.Now().Add(timeout)

	for atomic.LoadInt64(&p.active) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}

	remaining := atomic.LoadInt64(&p.active)
	if remaining > 0 {
		log.Printf("[worker] drain timeout with %d requests still in-flight", remaining)
	}
}
