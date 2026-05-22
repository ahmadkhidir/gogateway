// Package server manages the HTTP listener, reverse proxy, and graceful
// shutdown lifecycle for GoGateway.
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/balancer"
	"github.com/ahmadkhidir/gogateway/internal/config"
	"github.com/ahmadkhidir/gogateway/internal/discovery"
	"github.com/ahmadkhidir/gogateway/internal/middleware"
	"github.com/ahmadkhidir/gogateway/internal/router"
	"github.com/ahmadkhidir/gogateway/internal/store"
)

// Server wraps an http.Server with the GoGateway reverse proxy and provides
// lifecycle control (Start, Shutdown).
type Server struct {
	httpServer   *http.Server
	cfg          *config.Config
	router       *router.Router
	registry     discovery.Discoverer
	balancers    map[string]balancer.Balancer // route ID → balancer
	proxy        *httputil.ReverseProxy
	jwtAuth      *middleware.JWTAuth
	apiKeyAuth   *middleware.APIKeyAuth
	rateLimiter  *middleware.RateLimiter
	breakerStore *middleware.BreakerStore
}

type contextKey string

const targetURLKey contextKey = "gogateway_target_url"

// defaultJWTSecret generates a random 32-byte secret for development use.
func defaultJWTSecret() []byte {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return secret
}

// New creates a new Server from the given configuration. It builds the
// route table, service registry, load balancers, JWT/API key authenticators,
// and reverse proxy handler chain but does not start listening.
func New(cfg *config.Config) (*Server, error) {
	// Build the route matcher.
	r := router.New(cfg.Routes)

	// Build the static service registry.
	reg, err := discovery.NewStaticRegistry(cfg.Routes)
	if err != nil {
		return nil, fmt.Errorf("server: build registry: %w", err)
	}

	// Create a load balancer for each route (default: round-robin).
	balancers := make(map[string]balancer.Balancer, len(cfg.Routes))
	for _, route := range cfg.Routes {
		balancers[route.ID] = balancer.NewRoundRobin()
	}

	// Shared transport with sensible timeouts.
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	// Reverse proxy with a dynamic director that reads the target URL
	// from the request context (set during routing).
	proxy := &httputil.ReverseProxy{
		Director:  dynamicDirector,
		Transport: transport,
	}

	// --- Authentication setup ---

	// JWT: use env var GOGATEWAY_JWT_SECRET, or generate a dev secret.
	jwtSecretStr := os.Getenv("GOGATEWAY_JWT_SECRET")
	var jwtSecret []byte
	if jwtSecretStr != "" {
		jwtSecret = []byte(jwtSecretStr)
		slog.Info("JWT authentication enabled (HS256)")
	} else {
		jwtSecret = defaultJWTSecret()
		slog.Warn("GOGATEWAY_JWT_SECRET not set; using random dev secret. " +
			"Set the environment variable for a stable JWT secret.")
	}
	jwtAuth := middleware.NewJWTAuth(jwtSecret)

	// API key store: load from api-keys.yaml (optional).
	keyFilePath := os.Getenv("GOGATEWAY_API_KEY_FILE")
	if keyFilePath == "" {
		keyFilePath = "./api-keys.yaml"
	}
	keyStore, err := store.LoadKeyFile(keyFilePath)
	if err != nil {
		slog.Warn("failed to load API key file", "path", keyFilePath, "error", err)
		keyStore = store.NewKeyStore() // fall back to empty store
	}
	apiKeyAuth := middleware.NewAPIKeyAuth(keyStore)

	// --- Rate limiter setup ---

	// Try to connect to Redis; if unavailable the rate limiter falls back
	// to an in-memory counter (degraded but not broken).
	var redisClient *store.RedisClient
	if cfg.Redis.Addr != "" {
		rc, err := store.NewRedisClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, cfg.Redis.PoolSize)
		if err != nil {
			slog.Warn("Redis unavailable; rate limiter using in-memory fallback", "error", err)
		} else {
			redisClient = rc
			slog.Info("Rate limiter connected to Redis", "addr", cfg.Redis.Addr)
		}
	}
	rateLimiter := middleware.NewRateLimiter(redisClient)
	rateLimiter.StartCleanup(5 * time.Minute) // periodic cleanup of expired in-memory entries

	// --- Circuit breaker setup ---
	breakerStore := middleware.NewBreakerStore()

	s := &Server{
		cfg:          cfg,
		router:       r,
		registry:     reg,
		balancers:    balancers,
		proxy:        proxy,
		jwtAuth:      jwtAuth,
		apiKeyAuth:   apiKeyAuth,
		rateLimiter:  rateLimiter,
		breakerStore: breakerStore,
	}

	// Build the handler chain: RequestID → routing + proxy.
	handler := middleware.RequestID(http.HandlerFunc(s.serveRoute))

	httpServer := &http.Server{
		Addr:    cfg.Gateway.Listen,
		Handler: handler,
	}
	s.httpServer = httpServer

	return s, nil
}

// serveRoute is the core request handler. It matches the request to a route,
// authenticates (if required), picks an upstream via the load balancer,
// and proxies the request.
func (s *Server) serveRoute(w http.ResponseWriter, r *http.Request) {
	// Find matching route.
	route := s.router.Match(r)
	if route == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no route matched", Code: "ROUTE_NOT_FOUND"})
		return
	}

	// Authenticate the request if the route requires it.
	if !s.authenticate(w, r, route) {
		return // 401 already written
	}

	// Check rate limit.
	if !s.checkRateLimit(w, r, route) {
		return // 429 already written
	}

	// Resolve upstream endpoints.
	endpoints, err := s.registry.GetEndpoints(route.ID)
	if err != nil || len(endpoints) == 0 {
		slog.Warn("no upstream endpoints", "route", route.ID)
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: "no upstream available", Code: "NO_UPSTREAM"})
		return
	}

	// Pick an endpoint via load balancer.
	lb := s.balancers[route.ID]
	target := lb.Next(endpoints)

	// Check circuit breaker for this upstream pool.
	cbEnabled := route.CircuitBreaker != nil && route.CircuitBreaker.Enabled
	if cbEnabled {
		breaker := s.breakerStore.GetOrCreate(route.ID, target.String(), *route.CircuitBreaker)
		if !breaker.Allow() {
			slog.Warn("circuit breaker open, rejecting request",
				"route", route.ID, "target", target.String())
			writeJSON(w, http.StatusServiceUnavailable, errorBody{
				Error: "upstream circuit breaker open",
				Code:  "CIRCUIT_OPEN",
			})
			return
		}
	}

	slog.Debug("routing request", "route", route.ID, "method", r.Method, "path", r.URL.Path, "target", target.String())

	// Store target in request context for the director.
	ctx := context.WithValue(r.Context(), targetURLKey, target)
	r = r.WithContext(ctx)

	// Wrap the ResponseWriter to capture the upstream response status.
	rec := &statusRecorder{ResponseWriter: w}

	// Proxy the request.
	s.proxy.ServeHTTP(rec, r)

	// Record result in circuit breaker (if enabled).
	if cbEnabled {
		breaker := s.breakerStore.GetOrCreate(route.ID, target.String(), *route.CircuitBreaker)
		status := rec.Status()
		if status >= 500 {
			breaker.RecordFailure()
			slog.Debug("circuit breaker recording failure",
				"route", route.ID, "target", target.String(), "status", status)
		} else if status > 0 {
			breaker.RecordSuccess()
			slog.Debug("circuit breaker recording success",
				"route", route.ID, "target", target.String(), "status", status)
		}
	}
}

// authenticate checks the request against the route's auth configuration.
// It implements the "try JWT, fallback to API key" strategy:
//  1. If route.Auth is nil → allow (no auth required).
//  2. If JWT is required → validate JWT; on success forward claims and allow.
//  3. If JWT fails but API key is also required → try API key.
//  4. If API key succeeds → forward key info and allow.
//  5. If all methods fail → write 401 and return false.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request, route *config.Route) bool {
	if route.Auth == nil {
		return true // no auth config, allow
	}

	jwtCfg := route.Auth.JWT
	apiKeyCfg := route.Auth.APIKey

	jwtRequired := jwtCfg != nil && jwtCfg.Required
	apiKeyRequired := apiKeyCfg != nil && apiKeyCfg.Required

	if !jwtRequired && !apiKeyRequired {
		return true // auth section present but nothing required
	}

	// Try JWT first.
	if jwtRequired {
		claims, err := s.jwtAuth.Validate(r, jwtCfg)
		if err == nil {
			middleware.ForwardClaims(r, claims)
			slog.Debug("JWT authentication successful",
				"route", route.ID,
				"user", claims["sub"])
			return true
		}
		slog.Debug("JWT validation failed", "route", route.ID, "error", err)

		// JWT failed; if API key is not also an option, reject now.
		if !apiKeyRequired {
			writeJSON(w, http.StatusUnauthorized, errorBody{
				Error: "invalid or missing JWT token",
				Code:  "JWT_INVALID",
			})
			return false
		}
	}

	// Try API key as fallback (or primary if JWT not required).
	if apiKeyRequired {
		key, err := s.apiKeyAuth.Validate(r)
		if err == nil {
			r.Header.Set("X-API-Key-ID", key.ID)
			r.Header.Set("X-API-Key-Tier", key.RateTier)
			slog.Debug("API key authentication successful",
				"route", route.ID,
				"key_id", key.ID)
			return true
		}
		slog.Debug("API key validation failed", "route", route.ID, "error", err)
	}

	writeJSON(w, http.StatusUnauthorized, errorBody{
		Error: "authentication required",
		Code:  "AUTH_REQUIRED",
	})
	return false
}

// checkRateLimit enforces the route's rate limit configuration.
// It sets rate limit headers on every response and returns 429 with
// a Retry-After header when the limit is exceeded.
func (s *Server) checkRateLimit(w http.ResponseWriter, r *http.Request, route *config.Route) bool {
	if route.RateLimit == nil || !route.RateLimit.Enabled {
		return true
	}

	clientID := middleware.ResolveClientID(
		middleware.AdaptRequest(r.Header.Get, r.RemoteAddr),
		route,
	)

	allowed, remaining, reset := s.rateLimiter.Allow(route, clientID)

	// Always set rate limit headers.
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(route.RateLimit.Requests))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(reset).Unix(), 10))

	if !allowed {
		retryAfter := int(reset.Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

		slog.Warn("rate limit exceeded",
			"route", route.ID,
			"client", clientID,
			"limit", route.RateLimit.Requests,
			"retry_after", retryAfter)

		writeJSON(w, http.StatusTooManyRequests, errorBody{
			Error: "rate limit exceeded",
			Code:  "RATE_LIMITED",
		})
		return false
	}

	return true
}

// statusRecorder wraps an http.ResponseWriter to capture the HTTP status
// code written by the upstream proxy response.
type statusRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.statusCode = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

// Status returns the captured status code, or 0 if no header was written.
func (r *statusRecorder) Status() int {
	return r.statusCode
}

// dynamicDirector modifies the outgoing request to point at the upstream
// target stored in the request context by serveRoute.
func dynamicDirector(r *http.Request) {
	target, ok := r.Context().Value(targetURLKey).(*url.URL)
	if !ok || target == nil {
		slog.Error("dynamic director: no target URL in context")
		return
	}

	r.URL.Scheme = target.Scheme
	r.URL.Host = target.Host
	r.URL.Path = singleJoiningSlash(target.Path, r.URL.Path)

	// Preserve query string.
	if target.RawQuery == "" || r.URL.RawQuery == "" {
		r.URL.RawQuery = target.RawQuery + r.URL.RawQuery
	} else {
		r.URL.RawQuery = target.RawQuery + "&" + r.URL.RawQuery
	}

	// Ensure User-Agent is set.
	if _, ok := r.Header["User-Agent"]; !ok {
		r.Header.Set("User-Agent", "GoGateway/1.0")
	}
}

// singleJoiningSlash joins a and b with a single slash between them.
// Handles the common case of merging a base path like "/api" with a
// request path like "/v1/users". This is equivalent to the unexported
// helper in net/http/httputil.
func singleJoiningSlash(a, b string) string {
	a = strings.TrimRight(a, "/")
	b = strings.TrimLeft(b, "/")
	if a == "" {
		return "/" + b
	}
	return a + "/" + b
}

// errorBody is a standard JSON error response.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Start begins listening and serving HTTP traffic. It returns an error
// (typically http.ErrServerClosed) when Shutdown is called.
func (s *Server) Start() error {
	slog.Info("starting gateway", "listen", s.cfg.Gateway.Listen)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server, waiting up to the configured
// shutdown timeout for in-flight requests to complete.
func (s *Server) Shutdown() error {
	slog.Info("shutting down gateway gracefully")
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Gateway.ShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("shutdown deadline exceeded, forcing close")
			return s.httpServer.Close()
		}
		return fmt.Errorf("server shutdown: %w", err)
	}
	slog.Info("gateway stopped")
	return nil
}

// ServeHTTP implements http.Handler so the server can be used directly
// in tests or embedded servers. It delegates to the configured handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.httpServer.Handler.ServeHTTP(w, r)
}
