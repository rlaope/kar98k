package rpc

import (
	"fmt"

	"github.com/kar98k/internal/hdrbounds"
	pb "github.com/kar98k/internal/rpc/proto"
)

// DefaultHistogramBounds returns the canonical bounds proto message.
// All workers send these in RegisterReq; the master rejects mismatches.
func DefaultHistogramBounds() *pb.HistogramBounds {
	return &pb.HistogramBounds{
		MinValue: hdrbounds.Min,
		MaxValue: hdrbounds.Max,
		SigFigs:  hdrbounds.SigFigs,
	}
}

// ValidateBounds checks that the incoming bounds match master's constants.
func ValidateBounds(b *pb.HistogramBounds) error {
	if b == nil {
		return fmt.Errorf("histogram bounds missing from RegisterReq")
	}
	if b.MinValue != hdrbounds.Min || b.MaxValue != hdrbounds.Max || b.SigFigs != hdrbounds.SigFigs {
		return fmt.Errorf("histogram bounds mismatch: got min=%d max=%d sig=%d, want min=%d max=%d sig=%d",
			b.MinValue, b.MaxValue, b.SigFigs,
			hdrbounds.Min, hdrbounds.Max, hdrbounds.SigFigs)
	}
	return nil
}
