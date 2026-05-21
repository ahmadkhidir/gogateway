// Package server manages the HTTP listener, reverse proxy, and graceful
// shutdown lifecycle for GoGateway.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
	"github.com/ahmadkhidir/gogateway/internal/middleware"
)

// Server wraps an http.Server with the GoGateway reverse proxy and provides
// lifecycle control (Start, Shutdown).
type Server struct {
	httpServer *http.Server
	cfg        *config.Config
}

// New creates a new Server from the given configuration. It builds the
// reverse proxy handler chain but does not start listening.
func New(cfg *config.Config) (*Server, error) {
	// Build the reverse proxy for the first route's first upstream.
	// Phase 1 will introduce full route matching and load balancing.
	handler, err := buildHandler(cfg)
	if err != nil {
		return nil, fmt.Errorf("server: build handler: %w", err)
	}

	httpServer := &http.Server{
		Addr:    cfg.Gateway.Listen,
		Handler: handler,
	}

	return &Server{
		httpServer: httpServer,
		cfg:        cfg,
	}, nil
}

// buildHandler constructs the full HTTP handler chain:
//  1. RequestID middleware
//  2. Reverse proxy
func buildHandler(cfg *config.Config) (http.Handler, error) {
	// For Phase 0 we proxy to the first route's first upstream.
	// Full routing (path/host matching, load balancing) comes in Phase 1.
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("no routes configured")
	}
	route := cfg.Routes[0]
	target := route.Upstreams[0]

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL %q: %w", target.URL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Customise the default transport with sensible timeouts.
	proxy.Transport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	// Wrap with middleware chain.
	handler := middleware.RequestID(proxy)

	return handler, nil
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
