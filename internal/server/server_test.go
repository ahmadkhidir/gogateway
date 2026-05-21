package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

// newTestConfig returns a minimal config with a catch-all route.
func newTestConfig() *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			Listen:          ":0",
			ShutdownTimeout: 5 * time.Second,
			LogLevel:        "info",
		},
		Redis: config.RedisConfig{
			Addr: "localhost:6379",
			DB:   0,
		},
		Routes: []config.Route{
			{
				ID:      "catchall",
				Path:    "/*",
				Methods: []string{"GET", "POST"},
				Upstreams: []config.Upstream{
					{URL: "http://127.0.0.1:1"},
				},
			},
		},
	}
}

func TestNewServer_InvalidUpstream(t *testing.T) {
	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = "://invalid"

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid upstream URL")
	}
}

func TestServer_ServeHTTP_RequestID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id-Echo", r.Header.Get("X-Request-Id"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	requestID := resp.Header.Get("X-Request-Id-Echo")
	if requestID == "" {
		t.Error("expected X-Request-Id to be forwarded to upstream")
	}
	if len(requestID) != 32 {
		t.Errorf("expected 32-char request ID, got %q (len=%d)", requestID, len(requestID))
	}

	respRequestID := resp.Header.Get("X-Request-Id")
	if respRequestID == "" {
		t.Error("expected X-Request-Id in response header")
	}
	if respRequestID != requestID {
		t.Errorf("response X-Request-Id %q != upstream echo %q", respRequestID, requestID)
	}
}

func TestServer_ServeHTTP_ExistingRequestID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id-Echo", r.Header.Get("X-Request-Id"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "client-provided-id-123")
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	resp.Body.Close()

	echo := resp.Header.Get("X-Request-Id-Echo")
	if echo != "client-provided-id-123" {
		t.Errorf("expected upstream to receive client-provided ID, got %q", echo)
	}
}

func TestServer_ServeHTTP_CatchAllRoute(t *testing.T) {
	// A catch-all route "/*" should match any path.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (catch-all matches), got %d", resp.StatusCode)
	}
}

func TestServer_ServeHTTP_NoRouteMatch(t *testing.T) {
	// Without a catch-all route, an unmatched path should return 404.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes = []config.Route{
		{
			ID:      "specific",
			Path:    "/api/v1",
			Methods: []string{"GET"},
			Upstreams: []config.Upstream{
				{URL: upstream.URL},
			},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/other-path", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for unmatched path, got %d", resp.StatusCode)
	}

	// Verify JSON error body.
	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("error decoding JSON body: %v", err)
	}
	if body.Code != "ROUTE_NOT_FOUND" {
		t.Errorf("expected code ROUTE_NOT_FOUND, got %q", body.Code)
	}
}

func TestServer_MethodMismatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes = []config.Route{
		{
			ID:      "get-only",
			Path:    "/resource",
			Methods: []string{"GET"},
			Upstreams: []config.Upstream{
				{URL: upstream.URL},
			},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// POST to a GET-only route should 404.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/resource", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for method mismatch, got %d", resp.StatusCode)
	}
}

func TestServer_MultiRouteRouting(t *testing.T) {
	// Two upstream servers that echo which one handled the request.
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "A")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "B")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamB.Close()

	cfg := newTestConfig()
	cfg.Routes = []config.Route{
		{
			ID:      "api",
			Path:    "/api/*",
			Methods: []string{"GET"},
			Upstreams: []config.Upstream{
				{URL: upstreamA.URL},
			},
		},
		{
			ID:      "health",
			Path:    "/health",
			Methods: []string{"GET"},
			Upstreams: []config.Upstream{
				{URL: upstreamB.URL},
			},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Request to /api/users should go to upstream A.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	srv.ServeHTTP(rec1, req1)
	resp1 := rec1.Result()
	resp1.Body.Close()
	if resp1.Header.Get("X-Upstream") != "A" {
		t.Errorf("/api/users: expected upstream A, got %q", resp1.Header.Get("X-Upstream"))
	}

	// Request to /health should go to upstream B.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.ServeHTTP(rec2, req2)
	resp2 := rec2.Result()
	resp2.Body.Close()
	if resp2.Header.Get("X-Upstream") != "B" {
		t.Errorf("/health: expected upstream B, got %q", resp2.Header.Get("X-Upstream"))
	}
}

func TestServer_RoundRobinDistribution(t *testing.T) {
	// Two upstream servers that count how many requests they receive.
	var (
		mu     sync.Mutex
		countA int
		countB int
	)

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		countA++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		countB++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamB.Close()

	cfg := newTestConfig()
	cfg.Routes = []config.Route{
		{
			ID:      "api",
			Path:    "/*",
			Methods: []string{"GET"},
			Upstreams: []config.Upstream{
				{URL: upstreamA.URL},
				{URL: upstreamB.URL},
			},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	const n = 100
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		rec.Result().Body.Close()
	}

	if countA != n/2 {
		t.Errorf("expected upstream A to handle %d requests, got %d", n/2, countA)
	}
	if countB != n/2 {
		t.Errorf("expected upstream B to handle %d requests, got %d", n/2, countB)
	}
}


