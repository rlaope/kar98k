package controller

import "context"

// Submitter strategises how (or whether) the controller pushes jobs into
// the local worker pool. Solo mode uses LocalSubmitter to run the
// per-millisecond submission loop; master mode uses NoopSubmitter so
// no local jobs are produced — the WorkerRegistry fans the rate out to
// remote workers instead.
type Submitter interface {
	Run(ctx context.Context)
}

// LocalSubmitter drives Controller.generateLoop on the owning controller.
type LocalSubmitter struct {
	c *Controller
}

// Run runs the local job-submission loop until ctx is cancelled.
func (s *LocalSubmitter) Run(ctx context.Context) {
	if s == nil || s.c == nil {
		return
	}
	s.c.generateLoop(ctx)
}

// NoopSubmitter is the master-mode strategy: it returns immediately and
// never submits jobs locally.
type NoopSubmitter struct{}

// Run is a no-op for master mode.
func (NoopSubmitter) Run(_ context.Context) {}
