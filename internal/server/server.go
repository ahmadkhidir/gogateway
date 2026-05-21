// Package server manages the HTTP listener, reverse proxy, and graceful
// shutdown lifecycle for GoGateway.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/balancer"
	"github.com/ahmadkhidir/gogateway/internal/config"
	"github.com/ahmadkhidir/gogateway/internal/discovery"
	"github.com/ahmadkhidir/gogateway/internal/middleware"
	"github.com/ahmadkhidir/gogateway/internal/router"
)

// Server wraps an http.Server with the GoGateway reverse proxy and provides
// lifecycle control (Start, Shutdown).
type Server struct {
	httpServer *http.Server
	cfg        *config.Config
	router     *router.Router
	registry   discovery.Discoverer
	balancers  map[string]balancer.Balancer // route ID → balancer
	proxy      *httputil.ReverseProxy
}

type contextKey string

const targetURLKey contextKey = "gogateway_target_url"

// New creates a new Server from the given configuration. It builds the
// route table, service registry, load balancers, and reverse proxy handler
// chain but does not start listening.
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

	s := &Server{
		cfg:       cfg,
		router:    r,
		registry:  reg,
		balancers: balancers,
		proxy:     proxy,
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
// picks an upstream via the load balancer, and proxies the request.
func (s *Server) serveRoute(w http.ResponseWriter, r *http.Request) {
	// Find matching route.
	route := s.router.Match(r)
	if route == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no route matched", Code: "ROUTE_NOT_FOUND"})
		return
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

	slog.Debug("routing request", "route", route.ID, "method", r.Method, "path", r.URL.Path, "target", target.String())

	// Store target in request context for the director.
	ctx := context.WithValue(r.Context(), targetURLKey, target)
	r = r.WithContext(ctx)

	// Proxy the request.
	s.proxy.ServeHTTP(w, r)
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
