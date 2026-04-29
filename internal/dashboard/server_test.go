package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestForecastEndpoint_ReturnsConfiguredPoints exercises the wiring
// the daemon relies on: when SetForecastSource is called, GET
// /api/forecast must return the source's points as JSON.
func TestForecastEndpoint_ReturnsConfiguredPoints(t *testing.T) {
	s := New(":0")
	want := []ForecastPoint{
		{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), TPS: 100},
		{Time: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), TPS: 200, Spiking: true, Phase: "spike"},
	}
	s.SetForecastSource(func() []ForecastPoint { return want })

	req := httptest.NewRequest("GET", "/api/forecast", nil)
	rec := httptest.NewRecorder()
	s.handleForecast(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []ForecastPoint
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	if got[0].TPS != 100 {
		t.Fatalf("got[0].TPS = %v, want 100", got[0].TPS)
	}
	if got[1].Phase != "spike" || !got[1].Spiking {
		t.Fatalf("got[1] = %+v, want spike + spiking", got[1])
	}
}

// TestForecastEndpoint_ReturnsNotImplementedWhenSourceUnset is the
// script-mode path: forecast is meaningless without a pattern engine,
// so the endpoint must signal that explicitly rather than returning
// an empty array.
func TestForecastEndpoint_ReturnsNotImplementedWhenSourceUnset(t *testing.T) {
	s := New(":0")

	req := httptest.NewRequest("GET", "/api/forecast", nil)
	rec := httptest.NewRecorder()
	s.handleForecast(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
