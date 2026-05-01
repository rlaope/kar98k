package controller

import (
	"context"
	"testing"
	"time"
)

func TestNoopSubmitter_ReturnsImmediately(t *testing.T) {
	done := make(chan struct{})
	go func() {
		NoopSubmitter{}.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("NoopSubmitter.Run did not return immediately")
	}
}

func TestLocalSubmitter_HonorsContextCancel(t *testing.T) {
	// Why: this exercises the *generateLoop* path. We don't need a real
	// pool — a no-op stub PoolFacade is enough because Pick returns nil
	// for an empty target slice and submitJobs short-circuits.
	c := &Controller{
		picker: nil, // Pick on a nil *Picker returns nil safely
	}
	s := &LocalSubmitter{c: c}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Let the loop spin a few ticks then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("LocalSubmitter.Run did not exit on context cancel")
	}
}

func TestLocalSubmitter_NilSafeRun(t *testing.T) {
	// Calling Run on a nil receiver or empty wrapper must not panic;
	// the controller wires up *LocalSubmitter{} markers in NewController
	// and a guard makes early bring-up safe.
	var s *LocalSubmitter
	s.Run(context.Background()) // nil receiver
	(&LocalSubmitter{}).Run(context.Background()) // nil c
}
