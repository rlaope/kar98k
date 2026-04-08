package script

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExternalRunner implements Runner for any language via JSON-line protocol.
//
// Protocol:
//   kar98k sends commands to stdin as JSON lines:
//     {"cmd":"setup"}
//     {"cmd":"iterate","vu_id":1,"data":{...}}
//     {"cmd":"teardown","data":{...}}
//
//   Script responds on stdout with JSON lines:
//     {"type":"scenario","name":"...","chaos":{...},"stages":[...]}
//     {"type":"http","method":"GET","url":"...","headers":{...}}
//     {"type":"check","name":"...","passed":true}
//     {"type":"log","message":"..."}
//     {"type":"done","data":{...}}
//     {"type":"error","message":"..."}

type externalWorker struct {
	stdin  *json.Encoder
	stdout *bufio.Scanner
	cmd    *exec.Cmd
}

type ExternalRunner struct {
	path        string
	interpreter string
	scenario    ScenarioConfig
	metrics     *Metrics
	workers     []*externalWorker
	workerPool  chan *externalWorker
	poolSize    int
}

type externalCmd struct {
	Cmd  string      `json:"cmd"`
	VuID int         `json:"vu_id,omitempty"`
	Data interface{} `json:"data,omitempty"`
}

type externalResponse struct {
	Type    string                 `json:"type"`
	Name    string                 `json:"name,omitempty"`
	Method  string                 `json:"method,omitempty"`
	URL     string                 `json:"url,omitempty"`
	Headers map[string]string      `json:"headers,omitempty"`
	Body    string                 `json:"body,omitempty"`
	Passed  bool                   `json:"passed,omitempty"`
	Message string                 `json:"message,omitempty"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Chaos   *ChaosConfig           `json:"chaos,omitempty"`
	Stages  []Stage                `json:"stages,omitempty"`
}

func NewExternalRunner(path string, poolSize int) (*ExternalRunner, error) {
	ext := strings.ToLower(filepath.Ext(path))
	interpreter, ok := ExternalInterpreter[ext]
	if !ok {
		return nil, fmt.Errorf("no interpreter configured for %q extension. Supported: %v", ext, supportedExts())
	}

	if poolSize <= 0 {
		poolSize = 8
	}

	return &ExternalRunner{
		path:        path,
		interpreter: interpreter,
		scenario:    ScenarioConfig{Chaos: chaosPresets["moderate"]},
		metrics:     newMetrics(),
		poolSize:    poolSize,
	}, nil
}

func (r *ExternalRunner) spawnWorker() (*externalWorker, error) {
	parts := strings.Fields(r.interpreter)
	args := append(parts[1:], r.path)
	cmd := exec.Command(parts[0], args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", r.interpreter, err)
	}

	return &externalWorker{
		stdin:  json.NewEncoder(stdinPipe),
		stdout: bufio.NewScanner(stdoutPipe),
		cmd:    cmd,
	}, nil
}

func (r *ExternalRunner) Load(path string) error {
	// Spawn one initial subprocess for init to get scenario config.
	initWorker, err := r.spawnWorker()
	if err != nil {
		return err
	}

	// Send init command and read scenario config.
	if err := initWorker.stdin.Encode(externalCmd{Cmd: "init"}); err != nil {
		initWorker.cmd.Process.Kill()
		return fmt.Errorf("sending init: %w", err)
	}

	// Read responses until we get a "done" for init.
	for initWorker.stdout.Scan() {
		var resp externalResponse
		if err := json.Unmarshal(initWorker.stdout.Bytes(), &resp); err != nil {
			continue
		}

		switch resp.Type {
		case "scenario":
			r.scenario.Name = resp.Name
			if resp.Chaos != nil {
				r.scenario.Chaos = *resp.Chaos
				if resp.Chaos.Preset != "" {
					if base, ok := chaosPresets[resp.Chaos.Preset]; ok {
						r.scenario.Chaos = base
						if resp.Chaos.SpikeFactor > 0 {
							r.scenario.Chaos.SpikeFactor = resp.Chaos.SpikeFactor
						}
						if resp.Chaos.NoiseAmplitude > 0 {
							r.scenario.Chaos.NoiseAmplitude = resp.Chaos.NoiseAmplitude
						}
					}
				}
			}
			r.scenario.Stages = resp.Stages
		case "done":
			// Init done; kill the init subprocess and build the pool.
			initWorker.cmd.Process.Kill()
			return r.initPool(r.poolSize)
		case "error":
			initWorker.cmd.Process.Kill()
			return fmt.Errorf("script init error: %s", resp.Message)
		}
	}

	initWorker.cmd.Process.Kill()
	return fmt.Errorf("script ended unexpectedly during init")
}

func (r *ExternalRunner) initPool(n int) error {
	r.workers = make([]*externalWorker, 0, n)
	r.workerPool = make(chan *externalWorker, n)

	for i := 0; i < n; i++ {
		w, err := r.spawnWorker()
		if err != nil {
			// Kill any already-spawned workers before returning the error.
			for _, existing := range r.workers {
				existing.cmd.Process.Kill()
			}
			return fmt.Errorf("spawning worker %d: %w", i, err)
		}
		r.workers = append(r.workers, w)
		r.workerPool <- w
	}

	return nil
}

func (r *ExternalRunner) Setup() (interface{}, error) {
	w := <-r.workerPool
	defer func() { r.workerPool <- w }()

	if err := w.stdin.Encode(externalCmd{Cmd: "setup"}); err != nil {
		return nil, err
	}
	return r.readUntilDone(w)
}

func (r *ExternalRunner) Iterate(vuID int, data interface{}) error {
	w := <-r.workerPool
	defer func() { r.workerPool <- w }()

	if err := w.stdin.Encode(externalCmd{Cmd: "iterate", VuID: vuID, Data: data}); err != nil {
		return err
	}
	_, err := r.readUntilDone(w)
	return err
}

func (r *ExternalRunner) Teardown(data interface{}) error {
	w := <-r.workerPool
	defer func() { r.workerPool <- w }()

	if err := w.stdin.Encode(externalCmd{Cmd: "teardown", Data: data}); err != nil {
		return err
	}
	_, err := r.readUntilDone(w)
	return err
}

func (r *ExternalRunner) readUntilDone(w *externalWorker) (interface{}, error) {
	for w.stdout.Scan() {
		var resp externalResponse
		if err := json.Unmarshal(w.stdout.Bytes(), &resp); err != nil {
			continue
		}

		switch resp.Type {
		case "http":
			r.executeExternalHTTP(w, resp)
		case "check":
			r.metrics.recordCheck(resp.Name, resp.Passed)
		case "log":
			fmt.Println(resp.Message)
		case "done":
			return resp.Data, nil
		case "error":
			return nil, fmt.Errorf("script error: %s", resp.Message)
		}
	}
	return nil, fmt.Errorf("script ended unexpectedly")
}

func (r *ExternalRunner) executeExternalHTTP(w *externalWorker, resp externalResponse) {
	start := time.Now()

	req, err := newHTTPRequest(resp.Method, resp.URL, resp.Headers, resp.Body)
	if err != nil {
		r.metrics.recordRequest(0, time.Since(start), err)
		w.stdin.Encode(map[string]interface{}{
			"status": 0, "body": "", "duration": 0, "error": err.Error(),
		})
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}
	httpResp, err := client.Do(req)
	duration := time.Since(start)

	if err != nil {
		r.metrics.recordRequest(0, duration, err)
		w.stdin.Encode(map[string]interface{}{
			"status": 0, "body": "", "duration": duration.Seconds(), "error": err.Error(),
		})
		return
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(httpResp.Body)
	r.metrics.recordRequest(httpResp.StatusCode, duration, nil)

	// Send response back to script so it can inspect status, body, etc.
	w.stdin.Encode(map[string]interface{}{
		"status":   httpResp.StatusCode,
		"body":     string(body),
		"duration": duration.Seconds(),
		"error":    "",
	})
}

func (r *ExternalRunner) Scenario() *ScenarioConfig { return &r.scenario }
func (r *ExternalRunner) Metrics() *Metrics          { return r.metrics }

func (r *ExternalRunner) Close() error {
	var firstErr error
	for _, w := range r.workers {
		if w.cmd != nil && w.cmd.Process != nil {
			if err := w.cmd.Process.Kill(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func supportedExts() []string {
	exts := make([]string, 0, len(ExternalInterpreter))
	for ext := range ExternalInterpreter {
		exts = append(exts, ext)
	}
	return exts
}

func newHTTPRequest(method, url string, headers map[string]string, body string) (*http.Request, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}
