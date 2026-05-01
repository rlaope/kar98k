package daemon

import (
	"net"
	"sync"
	"sync/atomic"
)

// acceptDropListener is a tiny test helper: it accepts TCP connections
// and immediately closes them, bumping acceptCount on each accept.
// Useful for asserting that a dial loop actually reaches a given addr
// without spinning up a full gRPC server.
type acceptDropListener struct {
	net.Listener
	acceptCount int32
	closeOnce   sync.Once
	stop        chan struct{}
}

func newAcceptDropListener() (*acceptDropListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	a := &acceptDropListener{Listener: ln, stop: make(chan struct{})}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&a.acceptCount, 1)
			c.Close()
		}
	}()
	return a, nil
}

func (a *acceptDropListener) Close() error {
	a.closeOnce.Do(func() { close(a.stop) })
	return a.Listener.Close()
}
