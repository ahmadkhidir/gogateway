package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCounterVec(t *testing.T) {
	cv := NewCounterVec("route", "method")
	cv.Inc("api", "GET")
	cv.Inc("api", "POST")
	cv.Inc("api", "GET")

	cv.mu.Lock()
	total := 0
	for _, v := range cv.values {
		total += int(v)
	}
	cv.mu.Unlock()
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}

	cv.mu.Lock()
	getCount := cv.values["api\x00GET"]
	cv.mu.Unlock()
	if getCount != 2 {
		t.Errorf("expected GET count 2, got %g", getCount)
	}
}

func TestGauge(t *testing.T) {
	g := &Gauge{}
	g.Inc()
	g.Inc()
	g.Dec()
	if g.value != 1 {
		t.Errorf("expected 1, got %g", g.value)
	}
}

func TestGaugeVec(t *testing.T) {
	gv := NewGaugeVec("route", "upstream")
	gv.Set(1, "api", "http://u:1")
	gv.Set(2, "api", "http://u:2")

	gv.mu.Lock()
	count := len(gv.values)
	gv.mu.Unlock()
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}

	gv.Reset()
	gv.mu.Lock()
	count = len(gv.values)
	gv.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 entries after Reset, got %d", count)
	}
}

func TestHistogramVec(t *testing.T) {
	hv := NewHistogramVec(nil, "route", "method")
	hv.Observe(0.1, "api", "GET")
	hv.Observe(0.2, "api", "GET")
	hv.Observe(0.3, "api", "GET")

	hv.mu.Lock()
	h := hv.values["api\x00GET"]
	hv.mu.Unlock()
	if h == nil {
		t.Fatal("expected non-nil histogram")
	}
	if h.count != 3 {
		t.Errorf("expected count 3, got %d", h.count)
	}
	if h.sum < 0.599 || h.sum > 0.601 {
		t.Errorf("expected sum ~0.6, got %g", h.sum)
	}
}

func TestRecordRequest(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest("api", "GET", "200", 100*time.Millisecond)

	m.RequestsTotal.mu.Lock()
	count := m.RequestsTotal.values["api\x00GET\x00200"]
	m.RequestsTotal.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 request, got %g", count)
	}

	m.RequestDuration.mu.Lock()
	h := m.RequestDuration.values["api\x00GET"]
	m.RequestDuration.mu.Unlock()
	if h == nil || h.count != 1 {
		t.Errorf("expected 1 duration observation")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest("test", "GET", "200", 50*time.Millisecond)
	m.ActiveConnections.Inc()
	m.SetBreakerStates(map[string]int{"route-a:http://u:1": 1})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	output := string(body)

	checks := []string{
		"gogateway_requests_total",
		"gogateway_request_duration_seconds",
		"gogateway_active_connections",
		"gogateway_circuit_breaker_state",
		`route="test"`,
		`method="GET"`,
		`status_code="200"`,
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("expected metrics output to contain %q", check)
		}
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("expected Prometheus content type, got %q", ct)
	}
}

func TestHealthEndpoint(t *testing.T) {
	handler := HealthHandler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req2 := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	resp2 := rec2.Result()
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST, got %d", resp2.StatusCode)
	}
}

func TestBreakerStateLoop(t *testing.T) {
	m := NewMetrics()
	done := make(chan struct{})

	provider := &mockBreakerProvider{
		snapshots: []map[string]int{
			{"r1:u1": 0, "r2:u2": 1},
		},
	}

	go m.RunBreakerStateLoop(10*time.Millisecond, provider, done)
	time.Sleep(30 * time.Millisecond)
	close(done)

	m.CircuitBreakerState.mu.Lock()
	count := len(m.CircuitBreakerState.values)
	m.CircuitBreakerState.mu.Unlock()
	if count == 0 {
		t.Error("expected breaker state entries after loop run")
	}
}

type mockBreakerProvider struct {
	snapshots []map[string]int
	callCount int
}

func (m *mockBreakerProvider) BreakerStates() map[string]int {
	if m.callCount < len(m.snapshots) {
		m.callCount++
		return m.snapshots[m.callCount-1]
	}
	return m.snapshots[len(m.snapshots)-1]
}
