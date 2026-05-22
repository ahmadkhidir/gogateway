package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ahmadkhidir/gogateway/internal/config"
	"github.com/ahmadkhidir/gogateway/internal/metrics"
)

// setenv sets an environment variable for the duration of the test.
func setenv(t *testing.T, key, value string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if existed {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// newTestConfig returns a minimal config with a catch-all route.
func newTestConfig() *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			Listen:          ":0",
			ShutdownTimeout: 5 * time.Second,
			LogLevel:        "info",
		},
		Redis: config.RedisConfig{
			Addr: "", // empty = skip Redis (in-memory rate limiter fallback)
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

// hs256Token creates a signed HS256 JWT for testing.
func hs256Token(secret []byte, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString(secret)
	return signed
}

// --- Existing Phase 0 / Phase 1 tests (should pass unchanged) ---

func TestNewServer_InvalidUpstream(t *testing.T) {
	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = "://invalid"

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid upstream URL")
	}
}

func TestServer_ServeHTTP_RequestID(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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
}

func TestServer_ServeHTTP_ExistingRequestID(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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

	var body errorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("error decoding JSON body: %v", err)
	}
	if body.Code != "ROUTE_NOT_FOUND" {
		t.Errorf("expected code ROUTE_NOT_FOUND, got %q", body.Code)
	}
}

func TestServer_MethodMismatch(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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
			ID:      "status",
			Path:    "/statusz",
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

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	srv.ServeHTTP(rec1, req1)
	resp1 := rec1.Result()
	resp1.Body.Close()
	if resp1.Header.Get("X-Upstream") != "A" {
		t.Errorf("/api/users: expected upstream A, got %q", resp1.Header.Get("X-Upstream"))
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/statusz", nil)
	srv.ServeHTTP(rec2, req2)
	resp2 := rec2.Result()
	resp2.Body.Close()
	if resp2.Header.Get("X-Upstream") != "B" {
		t.Errorf("/statusz: expected upstream B, got %q", resp2.Header.Get("X-Upstream"))
	}
}

func TestServer_RoundRobinDistribution(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

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

// --- Phase 2 Authentication tests ---

func TestAuth_RouteWithoutAuthConfig_AllowsRequest(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	// route.Auth is nil — no auth required

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for route without auth, got %d", resp.StatusCode)
	}
}

func TestAuth_JWTRequired_MissingToken(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{
			Required: true,
			Issuers:  []string{"https://auth.example.com"},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// No Authorization header.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing JWT, got %d", resp.StatusCode)
	}

	var body errorBody
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "JWT_INVALID" {
		t.Errorf("expected code JWT_INVALID, got %q", body.Code)
	}
}

func TestAuth_JWTRequired_ValidToken(t *testing.T) {
	secret := "test-secret"
	setenv(t, "GOGATEWAY_JWT_SECRET", secret)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the user ID received from the gateway.
		w.Header().Set("X-User-ID-Echo", r.Header.Get("X-User-ID"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{
			Required: true,
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	token := hs256Token([]byte(secret), jwt.MapClaims{
		"sub": "user-42",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for valid JWT, got %d", resp.StatusCode)
	}

	// Upstream should have received the forwarded user ID.
	if echo := resp.Header.Get("X-User-ID-Echo"); echo != "user-42" {
		t.Errorf("expected upstream to receive X-User-ID 'user-42', got %q", echo)
	}
}

func TestAuth_JWTRequired_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	setenv(t, "GOGATEWAY_JWT_SECRET", secret)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{
			Required: true,
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	token := hs256Token([]byte(secret), jwt.MapClaims{
		"sub": "user-42",
		"exp": float64(time.Now().Add(-1 * time.Hour).Unix()), // expired
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired JWT, got %d", resp.StatusCode)
	}
}

func TestAuth_APIKeyRequired_MissingKey(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		APIKey: &config.APIKeyConfig{
			Required: true,
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing API key, got %d", resp.StatusCode)
	}
}

func TestAuth_APIKeyRequired_ValidKey(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	// Seed an API key file for this test.
	keyFileDir := t.TempDir()
	keyFilePath := keyFileDir + "/api-keys.yaml"
	keyFileContent := `
api_keys:
  - id: "test-key-1"
    key: "gw_test_valid_key"
    service: ""
    rate_tier: "pro"
`
	os.WriteFile(keyFilePath, []byte(keyFileContent), 0644)
	setenv(t, "GOGATEWAY_API_KEY_FILE", keyFilePath)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-API-Key-ID-Echo", r.Header.Get("X-API-Key-ID"))
		w.Header().Set("X-API-Key-Tier-Echo", r.Header.Get("X-API-Key-Tier"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		APIKey: &config.APIKeyConfig{
			Required: true,
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "gw_test_valid_key")
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for valid API key, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-API-Key-ID-Echo") != "test-key-1" {
		t.Errorf("expected key ID test-key-1, got %q", resp.Header.Get("X-API-Key-ID-Echo"))
	}
	if resp.Header.Get("X-API-Key-Tier-Echo") != "pro" {
		t.Errorf("expected tier pro, got %q", resp.Header.Get("X-API-Key-Tier-Echo"))
	}
}

func TestAuth_JWTWithAPIKeyFallback_JWTSuccess(t *testing.T) {
	secret := "test-secret"
	setenv(t, "GOGATEWAY_JWT_SECRET", secret)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-Method", "JWT")
		w.Header().Set("X-User-ID-Echo", r.Header.Get("X-User-ID"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{
			Required: true,
		},
		APIKey: &config.APIKeyConfig{
			Required: true, // both JWT and API key accepted
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	token := hs256Token([]byte(secret), jwt.MapClaims{
		"sub": "user-jwt",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	// Also set an API key — JWT should take precedence.
	req.Header.Set("X-API-Key", "some-key")
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Auth-Method") != "JWT" {
		t.Errorf("expected JWT auth method, got %q", resp.Header.Get("X-Auth-Method"))
	}
	if resp.Header.Get("X-User-ID-Echo") != "user-jwt" {
		t.Errorf("expected user-jwt, got %q", resp.Header.Get("X-User-ID-Echo"))
	}
}

func TestAuth_JWTWithAPIKeyFallback_KeyFallback(t *testing.T) {
	secret := "test-secret"
	setenv(t, "GOGATEWAY_JWT_SECRET", secret)

	// Seed API key file.
	keyFileDir := t.TempDir()
	keyFilePath := keyFileDir + "/api-keys.yaml"
	os.WriteFile(keyFilePath, []byte(`
api_keys:
  - id: "fallback-key"
    key: "gw_fallback_key_value"
    service: ""
    rate_tier: "basic"
`), 0644)
	setenv(t, "GOGATEWAY_API_KEY_FILE", keyFilePath)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-Method", "APIKey")
		w.Header().Set("X-API-Key-ID-Echo", r.Header.Get("X-API-Key-ID"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{
			Required: true,
		},
		APIKey: &config.APIKeyConfig{
			Required: true, // fallback allowed
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Send an invalid JWT but a valid API key.
	badToken := hs256Token([]byte(secret), jwt.MapClaims{
		"sub": "bad",
		"exp": float64(time.Now().Add(-1 * time.Hour).Unix()), // expired
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+badToken)
	req.Header.Set("X-API-Key", "gw_fallback_key_value")
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (API key fallback), got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Auth-Method") != "APIKey" {
		t.Errorf("expected APIKey auth method, got %q", resp.Header.Get("X-Auth-Method"))
	}
	if resp.Header.Get("X-API-Key-ID-Echo") != "fallback-key" {
		t.Errorf("expected fallback-key, got %q", resp.Header.Get("X-API-Key-ID-Echo"))
	}
}

func TestAuth_BothFail_Returns401(t *testing.T) {
	secret := "test-secret"
	setenv(t, "GOGATEWAY_JWT_SECRET", secret)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{Required: true},
		APIKey: &config.APIKeyConfig{Required: true},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// No auth headers at all.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 when both auth methods fail, got %d", resp.StatusCode)
	}

	var body errorBody
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "AUTH_REQUIRED" {
		t.Errorf("expected code AUTH_REQUIRED, got %q", body.Code)
	}
}

// --- Phase 3 Rate Limiting tests ---

func TestRateLimit_UnderLimit(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].RateLimit = &config.RateLimitCfg{
		Enabled:  true,
		Requests: 5,
		Window:   time.Minute,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
		if resp.Header.Get("X-RateLimit-Limit") != "5" {
			t.Errorf("request %d: expected X-RateLimit-Limit 5, got %q", i+1, resp.Header.Get("X-RateLimit-Limit"))
		}
	}
}

func TestRateLimit_OverLimit(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].RateLimit = &config.RateLimitCfg{
		Enabled:  true,
		Requests: 3,
		Window:   time.Minute,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Use up the limit.
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	// 4th request should be 429.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 for over-limit request, got %d", resp.StatusCode)
	}

	// Check headers.
	if resp.Header.Get("X-RateLimit-Limit") != "3" {
		t.Errorf("expected X-RateLimit-Limit 3, got %q", resp.Header.Get("X-RateLimit-Limit"))
	}
	if resp.Header.Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("expected X-RateLimit-Remaining 0, got %q", resp.Header.Get("X-RateLimit-Remaining"))
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
	if resp.Header.Get("X-RateLimit-Reset") == "" {
		t.Error("expected X-RateLimit-Reset header")
	}

	// Check JSON body.
	var body errorBody
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "RATE_LIMITED" {
		t.Errorf("expected code RATE_LIMITED, got %q", body.Code)
	}
}

func TestRateLimit_Disabled(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].RateLimit = &config.RateLimitCfg{
		Enabled:  false, // disabled
		Requests: 1,
		Window:   time.Minute,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Should be able to exceed the (disabled) limit.
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200 with rate limit disabled, got %d", i+1, resp.StatusCode)
		}
	}
}

func TestRateLimit_NoConfig(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].RateLimit = nil // no rate limit config

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200 with no rate limit config, got %d", i+1, resp.StatusCode)
		}
	}
}

func TestRateLimit_WithAuth(t *testing.T) {
	secret := "test-secret"
	setenv(t, "GOGATEWAY_JWT_SECRET", secret)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{Required: true},
	}
	cfg.Routes[0].RateLimit = &config.RateLimitCfg{
		Enabled:  true,
		Requests: 2,
		Window:   time.Minute,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	token := hs256Token([]byte(secret), jwt.MapClaims{
		"sub": "user-99",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	// First two requests succeed.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	// Third request: rate limited.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 after rate limit exceeded with JWT auth, got %d", resp.StatusCode)
	}
}

func TestRateLimit_UnauthenticatedRequestNotRateLimited(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].Auth = &config.AuthConfig{
		JWT: &config.JWTConfig{Required: true},
	}
	cfg.Routes[0].RateLimit = &config.RateLimitCfg{
		Enabled:  true,
		Requests: 100,
		Window:   time.Minute,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Unauthenticated request should fail auth before rate limiting.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 (auth before rate limit), got %d", resp.StatusCode)
	}
}

// --- Phase 4 Circuit Breaker tests ---

func TestCircuitBreaker_DisabledByDefault(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	// No CircuitBreaker config — should be skipped.

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}
}

func TestCircuitBreaker_TripsOnConsecutive5xx(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	// Upstream that returns 500 on every request.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].CircuitBreaker = &config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 3,
		Timeout:   30 * time.Second,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// First 3 requests: upstream returns 500, gateway proxies them (200 proxy response = 500 from upstream).
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		// The gateway forwards the upstream 500 to the client.
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("request %d: expected 500 from upstream, got %d", i+1, resp.StatusCode)
		}
	}

	// 4th request: circuit should be open → 503.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 after circuit trips, got %d", resp.StatusCode)
	}

	var body errorBody
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "CIRCUIT_OPEN" {
		t.Errorf("expected code CIRCUIT_OPEN, got %q", body.Code)
	}
}

func TestCircuitBreaker_RecoversAfterTimeout(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	// Upstream that starts failing, then recovers.
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].CircuitBreaker = &config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 3,
		Timeout:   30 * time.Millisecond,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Trip the breaker.
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		rec.Result().Body.Close()
	}

	// Circuit open → 503.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 immediately after trip, got %d", resp.StatusCode)
	}

	// Wait for timeout → half-open.
	time.Sleep(40 * time.Millisecond)

	// Now the probe request should go through (upstream now returns 200).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec2, req2)
	resp2 := rec2.Result()
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after circuit recovers (probe), got %d", resp2.StatusCode)
	}
}

func TestCircuitBreaker_SuccessResetsCounter(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	// Upstream returns: 500, 500, 200 (resets), 500, 500.
	// After the 200 the failure count is 0, so two more 500s (count=1, count=2)
	// are not enough to trip the breaker (threshold=3).
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 3 {
			w.WriteHeader(http.StatusOK) // success resets counter
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes[0].Upstreams[0].URL = upstream.URL
	cfg.Routes[0].CircuitBreaker = &config.CircuitBrkCfg{
		Enabled:   true,
		Threshold: 3,
		Timeout:   30 * time.Second,
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// First 5 requests: 500, 500, 200 (resets), 500, 500.
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		// All should go through (no 503 from circuit breaker).
		if resp.StatusCode == http.StatusServiceUnavailable {
			t.Errorf("request %d: unexpected 503 (circuit should be closed)", i+1)
		}
	}

	// Circuit is still closed (count=2 after 5th request, below threshold 3).
	// 6th request: upstream returns 500, count becomes 3, trips AFTER response.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	// Response is the upstream 500 (circuit trips after writing the response).
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 from upstream (circuit tripped after response), got %d", resp.StatusCode)
	}

	// 7th request: circuit is now open → 503.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec2, req2)
	resp2 := rec2.Result()
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (circuit now open), got %d", resp2.StatusCode)
	}
}

// --- Phase 5 Observability tests ---

func TestHealthEndpoint_Returns200(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	cfg := newTestConfig()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for health endpoint, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("expected JSON content type, got %q", ct)
	}

	var health metrics.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("error decoding health response: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("expected status ok, got %q", health.Status)
	}
	if health.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestHealthEndpoint_OnlyAcceptsGET(t *testing.T) {
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	cfg := newTestConfig()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/health", nil)
		srv.ServeHTTP(rec, req)
		resp := rec.Result()
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /health: expected 405, got %d", method, resp.StatusCode)
		}
	}
}

func TestConfiguredRouteStillRoutable(t *testing.T) {
	// Verify that non-/health paths are still matched by the router.
	setenv(t, "GOGATEWAY_JWT_SECRET", "test-secret")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "reached")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := newTestConfig()
	cfg.Routes = []config.Route{
		{
			ID:      "api",
			Path:    "/api/*",
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	srv.ServeHTTP(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for proxied route, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Upstream") != "reached" {
		t.Errorf("expected upstream to be reached, got header %q", resp.Header.Get("X-Upstream"))
	}
}
