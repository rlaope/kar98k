package script

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/controller"
)

// newInjectionVM returns a goja runtime with registerInjection() applied.
func newInjectionVM(t *testing.T) (*JSRunner, *goja.Runtime) {
	t.Helper()
	r := NewJSRunner()
	r.vm = goja.New()
	r.registerInjection()
	return r, r.vm
}

func TestInjectionNothingFor(t *testing.T) {
	_, vm := newInjectionVM(t)
	v, err := vm.RunString(`injection.nothingFor("30s")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	obj := v.ToObject(vm)
	if got := obj.Get("type").String(); got != string(config.InjectNothingFor) {
		t.Errorf("type: got %q, want %q", got, config.InjectNothingFor)
	}
	if got := obj.Get("duration").String(); got != "30s" {
		t.Errorf("duration: got %q, want 30s", got)
	}
}

func TestInjectionConstantTps(t *testing.T) {
	_, vm := newInjectionVM(t)
	v, err := vm.RunString(`injection.constantTps(100, "10m")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	obj := v.ToObject(vm)
	if got := obj.Get("type").String(); got != string(config.InjectConstantTPS) {
		t.Errorf("type: got %q, want %q", got, config.InjectConstantTPS)
	}
	if got := obj.Get("tps").ToFloat(); got != 100 {
		t.Errorf("tps: got %v, want 100", got)
	}
}

func TestInjectionRampTps(t *testing.T) {
	_, vm := newInjectionVM(t)
	v, err := vm.RunString(`injection.rampTps(0, 100, "2m")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	obj := v.ToObject(vm)
	if got := obj.Get("type").String(); got != string(config.InjectRampTPS) {
		t.Errorf("type: got %q, want %q", got, config.InjectRampTPS)
	}
	if got := obj.Get("from").ToFloat(); got != 0 {
		t.Errorf("from: got %v, want 0", got)
	}
	if got := obj.Get("to").ToFloat(); got != 100 {
		t.Errorf("to: got %v, want 100", got)
	}
}

func TestInjectionHeavisideTpsDefaultSteepness(t *testing.T) {
	_, vm := newInjectionVM(t)
	v, err := vm.RunString(`injection.heavisideTps(100, 500, "30s")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	obj := v.ToObject(vm)
	if got := obj.Get("type").String(); got != string(config.InjectHeavisideTPS) {
		t.Errorf("type: got %q, want %q", got, config.InjectHeavisideTPS)
	}
	// steepness 0 means use default in the evaluator
	if got := obj.Get("steepness").ToFloat(); got != 0 {
		t.Errorf("steepness: got %v, want 0 (default)", got)
	}
}

func TestInjectionHeavisideTpsExplicitSteepness(t *testing.T) {
	_, vm := newInjectionVM(t)
	v, err := vm.RunString(`injection.heavisideTps(0, 1000, "10s", { steepness: 10 })`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	obj := v.ToObject(vm)
	if got := obj.Get("steepness").ToFloat(); got != 10 {
		t.Errorf("steepness: got %v, want 10", got)
	}
}

func TestInjectionParseArray(t *testing.T) {
	_, vm := newInjectionVM(t)
	v, err := vm.RunString(`[
		injection.nothingFor("30s"),
		injection.rampTps(0, 100, "2m"),
		injection.constantTps(100, "10m"),
	]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps, err := parseJSInject(v, vm)
	if err != nil {
		t.Fatalf("parseJSInject: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("len: got %d, want 3", len(steps))
	}
	want := []config.InjectStep{
		{Type: config.InjectNothingFor, Duration: 30 * time.Second},
		{Type: config.InjectRampTPS, Duration: 2 * time.Minute, From: 0, To: 100},
		{Type: config.InjectConstantTPS, Duration: 10 * time.Minute, TPS: 100},
	}
	for i, w := range want {
		if steps[i].Type != w.Type {
			t.Errorf("[%d] type: got %q, want %q", i, steps[i].Type, w.Type)
		}
		if steps[i].Duration != w.Duration {
			t.Errorf("[%d] duration: got %v, want %v", i, steps[i].Duration, w.Duration)
		}
	}
}

func TestInjectionEvaluateParity(t *testing.T) {
	_, vm := newInjectionVM(t)

	// Ramp 0→100 over 2m; at midpoint (1m) expect 50 TPS.
	v, err := vm.RunString(`injection.evaluate([injection.rampTps(0, 100, "2m")], "1m")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	jsResult := v.ToFloat()

	goSteps := []config.InjectStep{
		{Type: config.InjectRampTPS, Duration: 2 * time.Minute, From: 0, To: 100},
	}
	goResult := controller.InjectTPSAt(goSteps, 1*time.Minute)

	if jsResult != goResult {
		t.Errorf("evaluate parity: JS=%v Go=%v", jsResult, goResult)
	}
}

func TestInjectionScenarioInjectField(t *testing.T) {
	r := NewJSRunner()
	r.vm = goja.New()
	r.registerGlobals()

	_, err := r.vm.RunString(`
		scenario({
			name: "warmup",
			inject: [
				injection.nothingFor("30s"),
				injection.rampTps(0, 100, "2m"),
				injection.constantTps(100, "10m"),
			],
		});
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.scenario.Name != "warmup" {
		t.Errorf("name: got %q, want warmup", r.scenario.Name)
	}
	if len(r.scenario.Inject) != 3 {
		t.Fatalf("inject len: got %d, want 3", len(r.scenario.Inject))
	}
	if r.scenario.Inject[0].Type != config.InjectNothingFor {
		t.Errorf("step[0] type: got %q", r.scenario.Inject[0].Type)
	}
	if r.scenario.Inject[2].TPS != 100 {
		t.Errorf("step[2] TPS: got %v, want 100", r.scenario.Inject[2].TPS)
	}
}

func TestInjectionInvalidDurationThrows(t *testing.T) {
	_, vm := newInjectionVM(t)
	_, err := vm.RunString(`injection.nothingFor("not-a-duration")`)
	if err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("error should mention invalid duration, got: %v", err)
	}
}

func TestInjectionNegativeTpsThrows(t *testing.T) {
	_, vm := newInjectionVM(t)
	_, err := vm.RunString(`injection.constantTps(-1, "10s")`)
	if err == nil {
		t.Fatal("expected error for negative tps, got nil")
	}
	if !strings.Contains(err.Error(), "tps >= 0") {
		t.Errorf("error should mention tps >= 0, got: %v", err)
	}
}

func TestInjectionNegativeFromToThrows(t *testing.T) {
	_, vm := newInjectionVM(t)
	_, err := vm.RunString(`injection.rampTps(-1, 100, "10s")`)
	if err == nil {
		t.Fatal("expected error for negative from, got nil")
	}
	if !strings.Contains(err.Error(), "from/to >= 0") {
		t.Errorf("error should mention from/to >= 0, got: %v", err)
	}
}
