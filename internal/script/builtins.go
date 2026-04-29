package script

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// CheckResult tracks check pass/fail counts.
type CheckResult struct {
	Name   string
	Passed int64
	Failed int64
}

// TimeBucket aggregates one minute of request data so the HTML report
// can render time-axis visualisations (heatmap + status-code stacked
// area) without retaining every sample.
type TimeBucket struct {
	StartTime   time.Time
	Histogram   *hdrhistogram.Histogram
	StatusCodes map[int]int64
}

// timeBucketResolution is the wall-clock width of each TimeBucket. One
// minute keeps the bucket count tractable for 24h runs (1440 buckets)
// while still showing intra-run trends in the heatmap.
const timeBucketResolution = time.Minute

// Metrics tracks request metrics during script execution.
type Metrics struct {
	mu            sync.Mutex
	TotalRequests int64
	TotalErrors   int64
	Histogram     *hdrhistogram.Histogram
	StatusCodes   map[int]int64
	Checks        []CheckResult
	checkMap      map[string]int
	StartTime     time.Time
	TimeBuckets   []*TimeBucket
}

func newMetrics() *Metrics {
	return &Metrics{
		// 1µs to 60s range, 3 significant digits
		Histogram:   hdrhistogram.New(1, 60000000, 3),
		StatusCodes: make(map[int]int64),
		checkMap:    make(map[string]int),
		StartTime:   time.Now(),
	}
}

// bucketFor returns the TimeBucket that should hold a sample observed
// at `at`, growing the slice with empty placeholders for any minutes
// without traffic. Caller must hold m.mu.
func (m *Metrics) bucketFor(at time.Time) *TimeBucket {
	idx := int(at.Sub(m.StartTime) / timeBucketResolution)
	if idx < 0 {
		idx = 0
	}
	for len(m.TimeBuckets) <= idx {
		bStart := m.StartTime.Add(time.Duration(len(m.TimeBuckets)) * timeBucketResolution)
		m.TimeBuckets = append(m.TimeBuckets, &TimeBucket{
			StartTime:   bStart,
			Histogram:   hdrhistogram.New(1, 60000000, 3),
			StatusCodes: make(map[int]int64),
		})
	}
	return m.TimeBuckets[idx]
}

func (m *Metrics) recordRequest(status int, duration time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	atomic.AddInt64(&m.TotalRequests, 1)
	if err != nil || status >= 400 {
		atomic.AddInt64(&m.TotalErrors, 1)
	}

	micros := duration.Microseconds()
	m.Histogram.RecordValue(micros)
	m.StatusCodes[status]++

	bucket := m.bucketFor(time.Now())
	bucket.Histogram.RecordValue(micros)
	bucket.StatusCodes[status]++
}

func (m *Metrics) recordCheck(name string, passed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx, exists := m.checkMap[name]
	if !exists {
		idx = len(m.Checks)
		m.checkMap[name] = idx
		m.Checks = append(m.Checks, CheckResult{Name: name})
	}

	if passed {
		m.Checks[idx].Passed++
	} else {
		m.Checks[idx].Failed++
	}
}

// httpModule creates the http.get/post/put/delete module.
func httpModule(rt *Runtime) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "http",
		Members: starlark.StringDict{
			"get":    starlark.NewBuiltin("http.get", makeHTTPMethod(rt, "GET")),
			"post":   starlark.NewBuiltin("http.post", makeHTTPMethod(rt, "POST")),
			"put":    starlark.NewBuiltin("http.put", makeHTTPMethod(rt, "PUT")),
			"delete": starlark.NewBuiltin("http.delete", makeHTTPMethod(rt, "DELETE")),
			"patch":  starlark.NewBuiltin("http.patch", makeHTTPMethod(rt, "PATCH")),
		},
	}
}

func makeHTTPMethod(rt *Runtime, method string) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var url starlark.String
		var headers starlark.Value = starlark.None
		var jsonBody starlark.Value = starlark.None
		var body starlark.String

		if err := starlark.UnpackArgs("http."+strings.ToLower(method), args, kwargs,
			"url", &url,
			"headers?", &headers,
			"json?", &jsonBody,
			"body?", &body,
		); err != nil {
			return nil, err
		}

		// Build request body
		var reqBody io.Reader
		contentType := ""

		if jsonBody != starlark.None {
			jsonBytes, err := starlarkValueToJSON(jsonBody)
			if err != nil {
				return nil, fmt.Errorf("json encoding: %w", err)
			}
			reqBody = bytes.NewReader(jsonBytes)
			contentType = "application/json"
		} else if string(body) != "" {
			reqBody = strings.NewReader(string(body))
		}

		// Create HTTP request
		req, err := http.NewRequest(method, string(url), reqBody)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		// Set headers
		if dict, ok := headers.(*starlark.Dict); ok {
			for _, item := range dict.Items() {
				k, _ := starlark.AsString(item[0])
				v, _ := starlark.AsString(item[1])
				req.Header.Set(k, v)
			}
		}

		// Execute request
		start := time.Now()
		resp, err := rt.httpClient.Do(req)
		duration := time.Since(start)

		if err != nil {
			rt.metrics.recordRequest(0, duration, err)
			return makeResponseValue(0, nil, duration, err), nil
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		rt.metrics.recordRequest(resp.StatusCode, duration, nil)

		return makeResponseValue(resp.StatusCode, respBody, duration, nil), nil
	}
}

// makeResponseValue creates a Starlark response struct.
func makeResponseValue(status int, body []byte, duration time.Duration, err error) starlark.Value {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	bodyStr := ""
	if body != nil {
		bodyStr = string(body)
	}

	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"status":   starlark.MakeInt(status),
		"body":     starlark.String(bodyStr),
		"duration": starlark.Float(duration.Seconds()),
		"error":    starlark.String(errStr),
		"json":     starlark.NewBuiltin("json", makeJSONParser(bodyStr)),
	})
}

func makeJSONParser(body string) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var raw interface{}
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			return starlark.None, fmt.Errorf("json parse: %w", err)
		}
		return goToStarlark(raw), nil
	}
}

// checkBuiltin implements check(response, { "name": lambda r: r.status == 200 }).
func checkBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	rt := thread.Local("runtime").(*Runtime)

	if len(args) != 2 {
		return nil, fmt.Errorf("check: expected 2 arguments (response, checks), got %d", len(args))
	}

	resp := args[0]
	checksDict, ok := args[1].(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("check: second argument must be a dict, got %s", args[1].Type())
	}

	allPassed := true
	for _, item := range checksDict.Items() {
		name, _ := starlark.AsString(item[0])
		fn := item[1]

		result, err := starlark.Call(thread, fn, starlark.Tuple{resp}, nil)
		if err != nil {
			rt.metrics.recordCheck(name, false)
			allPassed = false
			continue
		}

		ok := result.Truth() == starlark.True

		rt.metrics.recordCheck(name, ok)
		if !ok {
			allPassed = false
		}
	}

	return starlark.Bool(allPassed), nil
}

// sleepBuiltin implements sleep("1s") or sleep(1.0).
func sleepBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("sleep: expected 1 argument, got %d", len(args))
	}

	var d time.Duration

	switch v := args[0].(type) {
	case starlark.String:
		var err error
		d, err = time.ParseDuration(string(v))
		if err != nil {
			return nil, fmt.Errorf("sleep: invalid duration %q: %w", string(v), err)
		}
	case starlark.Float:
		d = time.Duration(float64(v) * float64(time.Second))
	case starlark.Int:
		i, _ := v.Int64()
		d = time.Duration(i) * time.Second
	default:
		return nil, fmt.Errorf("sleep: expected string or number, got %s", v.Type())
	}

	time.Sleep(d)
	return starlark.None, nil
}

// thinkTimeBuiltin implements think_time("1s", "3s") — chaos-aware.
// During spikes, think time compresses. During quiet periods, it expands.
func thinkTimeBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("think_time: expected 2 arguments (min, max), got %d", len(args))
	}

	minStr, _ := starlark.AsString(args[0])
	maxStr, _ := starlark.AsString(args[1])

	minD, err := time.ParseDuration(minStr)
	if err != nil {
		return nil, fmt.Errorf("think_time min: %w", err)
	}
	maxD, err := time.ParseDuration(maxStr)
	if err != nil {
		return nil, fmt.Errorf("think_time max: %w", err)
	}

	// Random duration between min and max
	rangeMs := maxD.Milliseconds() - minD.Milliseconds()
	if rangeMs <= 0 {
		rangeMs = 1
	}
	d := minD + time.Duration(rand.Int63n(rangeMs))*time.Millisecond

	// Return as a duration string for use with sleep()
	return starlark.String(d.String()), nil
}

// groupBuiltin implements group("name", fn) for logical grouping.
func groupBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("group: expected 2 arguments (name, fn), got %d", len(args))
	}

	// Just execute the function — groups are for metric labeling
	fn := args[1]
	return starlark.Call(thread, fn, nil, nil)
}

// Helper: convert Go value to Starlark value.
func goToStarlark(v interface{}) starlark.Value {
	switch val := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(val)
	case float64:
		if val == float64(int64(val)) {
			return starlark.MakeInt64(int64(val))
		}
		return starlark.Float(val)
	case string:
		return starlark.String(val)
	case []interface{}:
		list := make([]starlark.Value, len(val))
		for i, item := range val {
			list[i] = goToStarlark(item)
		}
		return starlark.NewList(list)
	case map[string]interface{}:
		dict := starlark.NewDict(len(val))
		for k, item := range val {
			dict.SetKey(starlark.String(k), goToStarlark(item))
		}
		return dict
	default:
		return starlark.String(fmt.Sprintf("%v", val))
	}
}

// Helper: convert Starlark value to JSON bytes.
func starlarkValueToJSON(v starlark.Value) ([]byte, error) {
	goVal := starlarkToGo(v)
	return json.Marshal(goVal)
}

func starlarkToGo(v starlark.Value) interface{} {
	switch val := v.(type) {
	case starlark.NoneType:
		return nil
	case starlark.Bool:
		return bool(val)
	case starlark.Int:
		i, _ := val.Int64()
		return i
	case starlark.Float:
		return float64(val)
	case starlark.String:
		return string(val)
	case *starlark.List:
		result := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			result[i] = starlarkToGo(val.Index(i))
		}
		return result
	case *starlark.Dict:
		result := make(map[string]interface{})
		for _, item := range val.Items() {
			k, _ := starlark.AsString(item[0])
			result[k] = starlarkToGo(item[1])
		}
		return result
	default:
		return val.String()
	}
}
