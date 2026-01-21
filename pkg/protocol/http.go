package protocol

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// HTTPClient implements Client for HTTP/1.1 and HTTP/2.
type HTTPClient struct {
	client  *http.Client
	bufPool sync.Pool
}

// NewHTTPClient creates a new HTTP/1.1 client.
func NewHTTPClient(cfg ClientConfig) *HTTPClient {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		DisableCompression:  true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.TLSInsecure,
		},
	}

	return &HTTPClient{
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		bufPool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 32*1024)
				return &buf
			},
		},
	}
}

// NewHTTP2Client creates a new HTTP/2 client.
func NewHTTP2Client(cfg ClientConfig) *HTTPClient {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			d := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.TLSInsecure,
		},
	}

	return &HTTPClient{
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		bufPool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 32*1024)
				return &buf
			},
		},
	}
}

// Do executes an HTTP request.
func (c *HTTPClient) Do(ctx context.Context, req *Request) *Response {
	start := time.Now()
	resp := &Response{}

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
		resp.BytesWritten = int64(len(req.Body))
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		resp.Error = err
		resp.Duration = time.Since(start)
		return resp
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
		httpReq = httpReq.WithContext(ctx)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		resp.Error = err
		resp.Duration = time.Since(start)
		return resp
	}
	defer httpResp.Body.Close()

	resp.StatusCode = httpResp.StatusCode

	// Drain and discard response body
	bufPtr := c.bufPool.Get().(*[]byte)
	defer c.bufPool.Put(bufPtr)

	n, _ := io.CopyBuffer(io.Discard, httpResp.Body, *bufPtr)
	resp.BytesRead = n
	resp.Duration = time.Since(start)

	return resp
}

// Close releases resources.
func (c *HTTPClient) Close() error {
	c.client.CloseIdleConnections()
	return nil
}
