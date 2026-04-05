package script

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// JSRunner implements Runner for JavaScript (.js) scripts.
type JSRunner struct {
	vm       *goja.Runtime
	mu       sync.Mutex
	scenario ScenarioConfig
	metrics  *Metrics
	client   *http.Client

	setupFn    goja.Callable
	defaultFn  goja.Callable
	teardownFn goja.Callable
}

func NewJSRunner() *JSRunner {
	return &JSRunner{
		scenario: ScenarioConfig{
			Chaos: chaosPresets["moderate"],
		},
		metrics: newMetrics(),
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (r *JSRunner) Load(path string) error {
	r.vm = goja.New()

	r.registerGlobals()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading script: %w", err)
	}

	_, err = r.vm.RunString(string(data))
	if err != nil {
		return fmt.Errorf("executing script: %w", err)
	}

	// Extract lifecycle functions
	if fn, ok := goja.AssertFunction(r.vm.Get("setup")); ok {
		r.setupFn = fn
	}
	if fn, ok := goja.AssertFunction(r.vm.Get("default")); ok {
		r.defaultFn = fn
	} else {
		// Try "run" as alias
		if fn, ok := goja.AssertFunction(r.vm.Get("run")); ok {
			r.defaultFn = fn
		}
	}
	if fn, ok := goja.AssertFunction(r.vm.Get("teardown")); ok {
		r.teardownFn = fn
	}

	if r.defaultFn == nil {
		return fmt.Errorf("script must export a default() or run() function")
	}

	return nil
}

func (r *JSRunner) registerGlobals() {
	// scenario()
	r.vm.Set("scenario", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(r.vm)
		if name := obj.Get("name"); name != nil {
			r.scenario.Name = name.String()
		}
		if pattern := obj.Get("pattern"); pattern != nil && pattern != goja.Undefined() {
			r.parseJSChaos(pattern.ToObject(r.vm))
		}
		if thresholds := obj.Get("thresholds"); thresholds != nil && thresholds != goja.Undefined() {
			r.scenario.Thresholds = make(map[string]string)
			obj := thresholds.ToObject(r.vm)
			for _, key := range obj.Keys() {
				r.scenario.Thresholds[key] = obj.Get(key).String()
			}
		}
		if vus := obj.Get("vus"); vus != nil && vus != goja.Undefined() {
			r.parseJSStages(vus.ToObject(r.vm))
		}
		return goja.Undefined()
	})

	// chaos()
	r.vm.Set("chaos", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(r.vm)
		cfg := chaosPresets["moderate"]

		if preset := obj.Get("preset"); preset != nil && preset != goja.Undefined() {
			if p, ok := chaosPresets[preset.String()]; ok {
				cfg = p
			}
		}
		if sf := obj.Get("spike_factor"); sf != nil && sf != goja.Undefined() {
			cfg.SpikeFactor = sf.ToFloat()
		}
		if na := obj.Get("noise_amplitude"); na != nil && na != goja.Undefined() {
			cfg.NoiseAmplitude = na.ToFloat()
		}

		result := r.vm.NewObject()
		result.Set("preset", cfg.Preset)
		result.Set("spike_factor", cfg.SpikeFactor)
		result.Set("noise_amplitude", cfg.NoiseAmplitude)
		result.Set("lambda", cfg.Lambda)
		return result
	})

	// stage()
	r.vm.Set("stage", func(call goja.FunctionCall) goja.Value {
		dur := call.Argument(0).String()
		target := call.Argument(1).ToInteger()
		obj := r.vm.NewObject()
		obj.Set("duration", dur)
		obj.Set("target", target)
		return obj
	})

	// ramp()
	r.vm.Set("ramp", func(call goja.FunctionCall) goja.Value {
		return call.Argument(0)
	})

	// http module
	httpObj := r.vm.NewObject()
	httpObj.Set("get", r.makeJSHTTPMethod("GET"))
	httpObj.Set("post", r.makeJSHTTPMethod("POST"))
	httpObj.Set("put", r.makeJSHTTPMethod("PUT"))
	httpObj.Set("delete", r.makeJSHTTPMethod("DELETE"))
	httpObj.Set("patch", r.makeJSHTTPMethod("PATCH"))
	r.vm.Set("http", httpObj)

	// check()
	r.vm.Set("check", func(call goja.FunctionCall) goja.Value {
		resp := call.Argument(0)
		checksObj := call.Argument(1).ToObject(r.vm)

		allPassed := true
		for _, key := range checksObj.Keys() {
			fn, ok := goja.AssertFunction(checksObj.Get(key))
			if !ok {
				continue
			}
			result, err := fn(goja.Undefined(), resp)
			if err != nil {
				r.metrics.recordCheck(key, false)
				allPassed = false
				continue
			}
			passed := result.ToBoolean()
			r.metrics.recordCheck(key, passed)
			if !passed {
				allPassed = false
			}
		}
		return r.vm.ToValue(allPassed)
	})

	// sleep()
	r.vm.Set("sleep", func(call goja.FunctionCall) goja.Value {
		arg := call.Argument(0)
		var d time.Duration
		if arg.ExportType().Kind().String() == "string" {
			d, _ = time.ParseDuration(arg.String())
		} else {
			d = time.Duration(arg.ToFloat() * float64(time.Second))
		}
		time.Sleep(d)
		return goja.Undefined()
	})

	// think_time()
	r.vm.Set("think_time", func(call goja.FunctionCall) goja.Value {
		minStr := call.Argument(0).String()
		maxStr := call.Argument(1).String()
		minD, _ := time.ParseDuration(minStr)
		maxD, _ := time.ParseDuration(maxStr)
		rangeMs := maxD.Milliseconds() - minD.Milliseconds()
		if rangeMs <= 0 {
			rangeMs = 1
		}
		d := minD + time.Duration(time.Now().UnixNano()%rangeMs)*time.Millisecond
		return r.vm.ToValue(d.String())
	})

	// console.log
	console := r.vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		args := make([]interface{}, len(call.Arguments))
		for i, a := range call.Arguments {
			args[i] = a.Export()
		}
		fmt.Println(args...)
		return goja.Undefined()
	})
	r.vm.Set("console", console)
}

func (r *JSRunner) makeJSHTTPMethod(method string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()

		var reqBody io.Reader
		contentType := ""
		var headers map[string]string

		if len(call.Arguments) > 1 {
			opts := call.Argument(1).ToObject(r.vm)

			if h := opts.Get("headers"); h != nil && h != goja.Undefined() {
				headers = make(map[string]string)
				hObj := h.ToObject(r.vm)
				for _, k := range hObj.Keys() {
					headers[k] = hObj.Get(k).String()
				}
			}
			if j := opts.Get("json"); j != nil && j != goja.Undefined() {
				jsonBytes, _ := json.Marshal(j.Export())
				reqBody = bytes.NewReader(jsonBytes)
				contentType = "application/json"
			}
			if b := opts.Get("body"); b != nil && b != goja.Undefined() {
				reqBody = bytes.NewReader([]byte(b.String()))
			}
		}

		req, err := http.NewRequest(method, url, reqBody)
		if err != nil {
			return r.makeJSResponse(0, nil, 0, err)
		}

		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		start := time.Now()
		resp, err := r.client.Do(req)
		duration := time.Since(start)

		if err != nil {
			r.metrics.recordRequest(0, duration, err)
			return r.makeJSResponse(0, nil, duration, err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		r.metrics.recordRequest(resp.StatusCode, duration, nil)

		return r.makeJSResponse(resp.StatusCode, body, duration, nil)
	}
}

func (r *JSRunner) makeJSResponse(status int, body []byte, duration time.Duration, err error) goja.Value {
	obj := r.vm.NewObject()
	obj.Set("status", status)
	obj.Set("body", string(body))
	obj.Set("duration", duration.Seconds())

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	obj.Set("error", errStr)

	obj.Set("json", func(call goja.FunctionCall) goja.Value {
		var raw interface{}
		if json.Unmarshal(body, &raw) != nil {
			return goja.Undefined()
		}
		return r.vm.ToValue(raw)
	})

	return obj
}

func (r *JSRunner) parseJSChaos(obj *goja.Object) {
	cfg := chaosPresets["moderate"]
	if preset := obj.Get("preset"); preset != nil && preset != goja.Undefined() {
		if p, ok := chaosPresets[preset.String()]; ok {
			cfg = p
		}
	}
	if sf := obj.Get("spike_factor"); sf != nil && sf != goja.Undefined() {
		cfg.SpikeFactor = sf.ToFloat()
	}
	if na := obj.Get("noise_amplitude"); na != nil && na != goja.Undefined() {
		cfg.NoiseAmplitude = na.ToFloat()
	}
	r.scenario.Chaos = cfg
}

func (r *JSRunner) parseJSStages(obj *goja.Object) {
	// Array of stage objects
	length := obj.Get("length")
	if length == nil {
		return
	}
	n := int(length.ToInteger())
	for i := 0; i < n; i++ {
		item := obj.Get(fmt.Sprintf("%d", i)).ToObject(r.vm)
		dur, _ := time.ParseDuration(item.Get("duration").String())
		target := int(item.Get("target").ToInteger())
		r.scenario.Stages = append(r.scenario.Stages, Stage{Duration: dur, Target: target})
	}
}

func (r *JSRunner) Setup() (interface{}, error) {
	if r.setupFn == nil {
		return nil, nil
	}
	result, err := r.setupFn(goja.Undefined())
	if err != nil {
		return nil, fmt.Errorf("setup(): %w", err)
	}
	return result, nil
}

func (r *JSRunner) Iterate(vuID int, data interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var dataVal goja.Value = goja.Undefined()
	if data != nil {
		dataVal = r.vm.ToValue(data)
	}
	_, err := r.defaultFn(goja.Undefined(), dataVal)
	if err != nil {
		return fmt.Errorf("default() VU %d: %w", vuID, err)
	}
	return nil
}

func (r *JSRunner) Teardown(data interface{}) error {
	if r.teardownFn == nil {
		return nil
	}
	var dataVal goja.Value = goja.Undefined()
	if data != nil {
		dataVal = r.vm.ToValue(data)
	}
	_, err := r.teardownFn(goja.Undefined(), dataVal)
	if err != nil {
		return fmt.Errorf("teardown(): %w", err)
	}
	return nil
}

func (r *JSRunner) Scenario() *ScenarioConfig { return &r.scenario }
func (r *JSRunner) Metrics() *Metrics          { return r.metrics }
func (r *JSRunner) Close() error               { return nil }
