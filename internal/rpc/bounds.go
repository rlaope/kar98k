package rpc

import (
	"fmt"

	pb "github.com/kar98k/internal/rpc/proto"
)

// DefaultBounds are the HDR histogram bounds used cluster-wide.
// Workers send these in RegisterReq; master rejects any mismatch.
// Values match the constants in internal/worker/pool.go.
const (
	BoundsMin     = int64(1)
	BoundsMax     = int64(60_000_000)
	BoundsSigFigs = int32(3)
)

// DefaultHistogramBounds returns the canonical bounds proto message.
func DefaultHistogramBounds() *pb.HistogramBounds {
	return &pb.HistogramBounds{
		MinValue: BoundsMin,
		MaxValue: BoundsMax,
		SigFigs:  BoundsSigFigs,
	}
}

// ValidateBounds checks that the incoming bounds match master's constants.
func ValidateBounds(b *pb.HistogramBounds) error {
	if b == nil {
		return fmt.Errorf("histogram bounds missing from RegisterReq")
	}
	if b.MinValue != BoundsMin || b.MaxValue != BoundsMax || b.SigFigs != BoundsSigFigs {
		return fmt.Errorf("histogram bounds mismatch: got min=%d max=%d sig=%d, want min=%d max=%d sig=%d",
			b.MinValue, b.MaxValue, b.SigFigs,
			BoundsMin, BoundsMax, BoundsSigFigs)
	}
	return nil
}
