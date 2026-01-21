package protocol

import (
	"context"
	"time"
)

// Request represents a generic request to be sent.
type Request struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    []byte
	Timeout time.Duration
}

// Response represents the result of a request.
type Response struct {
	StatusCode   int
	Duration     time.Duration
	BytesRead    int64
	BytesWritten int64
	Error        error
}

// Client is the interface for protocol implementations.
type Client interface {
	// Do executes a request and returns the response.
	Do(ctx context.Context, req *Request) *Response

	// Close releases any resources held by the client.
	Close() error
}

// ClientConfig contains common configuration for all clients.
type ClientConfig struct {
	MaxIdleConns    int
	IdleConnTimeout time.Duration
	TLSInsecure     bool
}
