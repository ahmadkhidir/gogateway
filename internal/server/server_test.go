package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

// newTestConfig returns a minimal config suitable for testing.
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
				ID:      "test",
				Path:    "/*",
				Methods: []string{"GET"},
				Upstreams: []config.Upstream{
					{URL: "http://127.0.0.1:1"}, // unlikely to be listening
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
	// Create a test upstream that reflects request headers.
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

	// Use the server as a handler directly.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	// The upstream should have received an X-Request-Id injected by the middleware.
	requestID := resp.Header.Get("X-Request-Id-Echo")
	if requestID == "" {
		t.Error("expected X-Request-Id to be forwarded to upstream")
	}
	if len(requestID) != 32 {
		t.Errorf("expected 32-char request ID, got %q (len=%d)", requestID, len(requestID))
	}

	// The response should also carry X-Request-Id.
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

func TestServer_ServeHTTP_NotFound(t *testing.T) {
	// Without a catch-all route, the default httputil.ReverseProxy will
	// proxy everything to the configured upstream. For phase 0 this is
	// expected behaviour — routing is added in phase 1.
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
		t.Errorf("expected 200 (all requests proxied in phase 0), got %d", resp.StatusCode)
	}
}
