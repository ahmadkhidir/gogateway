// GoGateway — Mini API Gateway & Reverse Proxy
//
// A lightweight, high-performance API gateway and reverse proxy built from
// scratch in Go. Inspired by Kong and Traefik, designed as a portfolio
// project demonstrating distributed systems, cloud-native patterns, and Go
// systems programming.
//
// Usage:
//
//	gogateway [--config-path <path>]
//
// The default config path is ./gogateway.yaml in the current working directory.
// The binary listens on the address specified in the config and proxies
// HTTP requests to configured upstream services.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
	"github.com/ahmadkhidir/gogateway/internal/logger"
	"github.com/ahmadkhidir/gogateway/internal/server"
)

func main() {
	configPath := flag.String("config-path", "./gogateway.yaml", "path to config file")
	flag.Parse()

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Initialise structured logger.
	logger.Init(config.LogLevelToSlog(cfg.Gateway.LogLevel), nil)
	slog.Info("config loaded", "path", *configPath)

	// Create server.
	srv, err := server.New(cfg)
	if err != nil {
		slog.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	// Channel to capture server errors (typically http.ErrServerClosed on
	// graceful shutdown).
	errCh := make(chan error, 1)

	// Start server in a goroutine.
	go func() {
		slog.Info("listening", "addr", cfg.Gateway.Listen)
		if err := srv.Start(); err != nil {
			errCh <- err
		}
	}()

	// Wait for interrupt signal for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("received signal", "signal", sig)
	case err := <-errCh:
		if err != nil {
			slog.Error("server error", "error", err)
		}
	}

	// Attempt graceful shutdown with the configured timeout.
	slog.Info("shutting down")
	if err := srv.Shutdown(); err != nil {
		slog.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	// Drain the error channel to check for server errors during shutdown.
	// Use a short context to avoid blocking indefinitely.
	drainCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped with error", "error", err)
			os.Exit(1)
		}
	case <-drainCtx.Done():
	}

	slog.Info("gateway stopped")
}
