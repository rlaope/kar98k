// Package hdrbounds is the single source of truth for HdrHistogram
// configuration used across kar98k. Latencies are recorded in
// microseconds with 3 significant digits; the 1µs..60s range covers
// everything from sub-ms in-memory targets to long timeouts without
// requiring histogram resizing.
//
// Before this package existed the constants were duplicated in
// internal/worker/pool.go (latencyHistMin/Max/SigFig), internal/discovery/
// analyzer.go (hdrMin/Max/SigFig), internal/rpc/bounds.go
// (BoundsMin/Max/SigFigs), and inlined as literals in internal/script/
// builtins.go and phase.go. Distributed mode requires bounds to match
// across all binaries (rpc.ValidateBounds rejects mismatches), so
// keeping them in one place avoids a class of failure where a renamed
// edit in one file silently diverges.
package hdrbounds

// Canonical HdrHistogram parameters (microseconds, 3 sigfigs, 1µs..60s).
const (
	// Min is the lowest discretised value (1 µs).
	Min int64 = 1
	// Max is the highest discretised value (60_000_000 µs = 60 s).
	Max int64 = 60_000_000
	// SigFigs is the number of significant digits in the histogram.
	SigFigs int32 = 3
)
