// Package metrics provides Prometheus-format metrics and the /metrics and
// /health HTTP endpoints for GoGateway.
//
// Metrics are served in the Prometheus text-based exposition format without
// external dependencies, using Go's standard library only.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- Metric collector types ---

// CounterVec is a vector of counters partitioned by label values.
type CounterVec struct {
	mu     sync.Mutex
	values map[string]float64
	names  []string
}

// NewCounterVec creates a counter vector with the given label names.
func NewCounterVec(names ...string) *CounterVec {
	return &CounterVec{
		values: make(map[string]float64),
		names:  names,
	}
}

// Inc increments the counter for the given label values by 1.
func (v *CounterVec) Inc(lvs ...string) {
	key := strings.Join(lvs, "\x00")
	v.mu.Lock()
	v.values[key]++
	v.mu.Unlock()
}

// Gauge is a single numeric gauge value.
type Gauge struct {
	mu    sync.Mutex
	value float64
}

func (g *Gauge) Inc() {
	g.mu.Lock()
	g.value++
	g.mu.Unlock()
}

func (g *Gauge) Dec() {
	g.mu.Lock()
	g.value--
	g.mu.Unlock()
}

// GaugeVec is a vector of gauges partitioned by label values.
type GaugeVec struct {
	mu     sync.Mutex
	values map[string]float64
	names  []string
}

// NewGaugeVec creates a gauge vector with the given label names.
func NewGaugeVec(names ...string) *GaugeVec {
	return &GaugeVec{
		values: make(map[string]float64),
		names:  names,
	}
}

// Set sets the gauge for the given label values.
func (v *GaugeVec) Set(val float64, lvs ...string) {
	key := strings.Join(lvs, "\x00")
	v.mu.Lock()
	v.values[key] = val
	v.mu.Unlock()
}

// Reset clears all values.
func (v *GaugeVec) Reset() {
	v.mu.Lock()
	v.values = make(map[string]float64)
	v.mu.Unlock()
}

// HistogramVec is a vector of histograms partitioned by label values.
type HistogramVec struct {
	mu      sync.Mutex
	buckets []float64
	values  map[string]*histogram
	names   []string
}

type histogram struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64
}

// NewHistogramVec creates a histogram vector with the given buckets and label names.
func NewHistogramVec(buckets []float64, names ...string) *HistogramVec {
	if buckets == nil {
		buckets = defaultBuckets
	}
	return &HistogramVec{
		buckets: buckets,
		values:  make(map[string]*histogram),
		names:   names,
	}
}

var defaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// Observe records an observation for the given label values.
func (v *HistogramVec) Observe(val float64, lvs ...string) {
	key := strings.Join(lvs, "\x00")
	v.mu.Lock()
	defer v.mu.Unlock()
	h, ok := v.values[key]
	if !ok {
		h = &histogram{buckets: make(map[float64]uint64)}
		v.values[key] = h
	}
	h.count++
	h.sum += val
	for _, b := range v.buckets {
		if val <= b {
			h.buckets[b]++
		}
	}
}

// --- Metrics registry ---

// Metrics holds all Prometheus metric collectors for the gateway.
type Metrics struct {
	RequestsTotal       *CounterVec
	RequestDuration     *HistogramVec
	ActiveConnections   *Gauge
	CircuitBreakerState *GaugeVec
	RateLimitRejections *CounterVec
}

// NewMetrics creates and registers all gateway metrics.
func NewMetrics() *Metrics {
	return &Metrics{
		RequestsTotal:       NewCounterVec("route", "method", "status_code"),
		RequestDuration:     NewHistogramVec(nil, "route", "method"),
		ActiveConnections:   &Gauge{},
		CircuitBreakerState: NewGaugeVec("route", "upstream"),
		RateLimitRejections: NewCounterVec("route"),
	}
}

// RecordRequest records a completed request's metrics.
func (m *Metrics) RecordRequest(route, method, statusCode string, duration time.Duration) {
	m.RequestsTotal.Inc(route, method, statusCode)
	m.RequestDuration.Observe(duration.Seconds(), route, method)
}

// RecordRateLimitRejection increments the rate limit rejection counter.
func (m *Metrics) RecordRateLimitRejection(route string) {
	m.RateLimitRejections.Inc(route)
}

// SetBreakerStates sets the circuit breaker state gauges from a snapshot.
func (m *Metrics) SetBreakerStates(snapshot map[string]int) {
	m.CircuitBreakerState.Reset()
	for key, state := range snapshot {
		m.CircuitBreakerState.Set(float64(state), key, "")
	}
}

// BreakerStateProvider returns a snapshot of circuit breaker states.
type BreakerStateProvider interface {
	BreakerStates() map[string]int
}

// RunBreakerStateLoop periodically exports circuit breaker states.
func (m *Metrics) RunBreakerStateLoop(interval time.Duration, updater BreakerStateProvider, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.SetBreakerStates(updater.BreakerStates())
		case <-done:
			return
		}
	}
}

// --- Prometheus text format rendering ---

func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var buf strings.Builder
	m.renderCounters(&buf)
	m.renderGauges(&buf)
	m.renderHistograms(&buf)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(buf.String()))
}

// Handler returns an http.Handler that serves Prometheus metrics.
func (m *Metrics) Handler() http.Handler {
	return m
}

func (m *Metrics) renderCounters(buf *strings.Builder) {
	// RequestsTotal
	buf.WriteString("# HELP gogateway_requests_total Total requests processed\n")
	buf.WriteString("# TYPE gogateway_requests_total counter\n")
	m.renderCounterMap(buf, m.RequestsTotal, "gogateway_requests_total")

	// RateLimitRejections
	buf.WriteString("# HELP gogateway_rate_limit_rejections_total Total rate-limited requests\n")
	buf.WriteString("# TYPE gogateway_rate_limit_rejections_total counter\n")
	m.renderCounterMap(buf, m.RateLimitRejections, "gogateway_rate_limit_rejections_total")
}

func (m *Metrics) renderCounterMap(buf *strings.Builder, cv *CounterVec, name string) {
	cv.mu.Lock()
	keys := sortedKeys(cv.values)
	for _, key := range keys {
		vals := strings.Split(key, "\x00")
		ls := formatLabels(cv.names, vals)
		fmt.Fprintf(buf, "%s%s %g\n", name, ls, cv.values[key])
	}
	cv.mu.Unlock()
}

func (m *Metrics) renderGauges(buf *strings.Builder) {
	// ActiveConnections
	m.ActiveConnections.mu.Lock()
	buf.WriteString("# HELP gogateway_active_connections Number of currently active connections\n")
	buf.WriteString("# TYPE gogateway_active_connections gauge\n")
	fmt.Fprintf(buf, "gogateway_active_connections %g\n", m.ActiveConnections.value)
	m.ActiveConnections.mu.Unlock()

	// CircuitBreakerState
	m.CircuitBreakerState.mu.Lock()
	buf.WriteString("# HELP gogateway_circuit_breaker_state Circuit breaker state per upstream (0=closed, 1=open, 2=half-open)\n")
	buf.WriteString("# TYPE gogateway_circuit_breaker_state gauge\n")
	keys := sortedKeys(m.CircuitBreakerState.values)
	for _, key := range keys {
		vals := strings.Split(key, "\x00")
		ls := formatLabels(m.CircuitBreakerState.names, vals)
		fmt.Fprintf(buf, "gogateway_circuit_breaker_state%s %g\n", ls, m.CircuitBreakerState.values[key])
	}
	m.CircuitBreakerState.mu.Unlock()
}

func (m *Metrics) renderHistograms(buf *strings.Builder) {
	m.RequestDuration.mu.Lock()
	buf.WriteString("# HELP gogateway_request_duration_seconds Request latency in seconds\n")
	buf.WriteString("# TYPE gogateway_request_duration_seconds histogram\n")
	keys := sortedKeysString(m.RequestDuration.values)
	for _, key := range keys {
		h := m.RequestDuration.values[key]
		vals := strings.Split(key, "\x00")
		base := "gogateway_request_duration_seconds"
		for _, b := range m.RequestDuration.buckets {
			ls := mergeLabels(m.RequestDuration.names, vals, "le", fmt.Sprintf("%g", b))
			fmt.Fprintf(buf, "%s_bucket%s %d\n", base, ls, h.buckets[b])
		}
		lsInf := mergeLabels(m.RequestDuration.names, vals, "le", "+Inf")
		fmt.Fprintf(buf, "%s_bucket%s %d\n", base, lsInf, h.count)
		ls := formatLabels(m.RequestDuration.names, vals)
		fmt.Fprintf(buf, "%s_sum%s %g\n", base, ls, h.sum)
		fmt.Fprintf(buf, "%s_count%s %d\n", base, ls, h.count)
	}
	m.RequestDuration.mu.Unlock()
}

// mergeLabels builds a Prometheus label string combining existing labels
// with one extra key=value pair. The extra pair is appended last.
func mergeLabels(names, vals []string, extraKey, extraVal string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(vals[i])
		b.WriteByte('"')
	}
	if len(names) > 0 {
		b.WriteByte(',')
	}
	b.WriteString(extraKey)
	b.WriteString(`="`)
	b.WriteString(extraVal)
	b.WriteByte('"')
	b.WriteByte('}')
	return b.String()
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysString(m map[string]*histogram) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatLabels(names, vals []string) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(vals[i])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// --- Health endpoint ---

type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	})
}

// --- Metrics server ---

func StartMetricsServer(addr string, metrics *Metrics) (*http.Server, error) {
	if addr == "" {
		return nil, nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.Handle("/health", HealthHandler())
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		slog.Info("metrics server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()
	return srv, nil
}

func ShutdownMetricsServer(srv *http.Server, timeout time.Duration) error {
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
