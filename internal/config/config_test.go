package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_ValidMinimal(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "valid_minimal.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Gateway.Listen != ":8080" {
		t.Errorf("expected listen :8080, got %q", cfg.Gateway.Listen)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if cfg.Routes[0].Upstreams[0].URL != "http://localhost:8081" {
		t.Errorf("expected upstream http://localhost:8081, got %q", cfg.Routes[0].Upstreams[0].URL)
	}
}

func TestLoad_ValidFull(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "valid_full.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Gateway.Listen != ":8080" {
		t.Errorf("expected listen :8080, got %q", cfg.Gateway.Listen)
	}
	if cfg.Gateway.MetricsListen != ":9090" {
		t.Errorf("expected metrics_listen :9090, got %q", cfg.Gateway.MetricsListen)
	}
	if cfg.Gateway.ShutdownTimeout != 30*time.Second {
		t.Errorf("expected shutdown_timeout 30s, got %v", cfg.Gateway.ShutdownTimeout)
	}
	if cfg.Gateway.LogLevel != "debug" {
		t.Errorf("expected log_level debug, got %q", cfg.Gateway.LogLevel)
	}

	if cfg.Redis.Addr != "redis:6379" {
		t.Errorf("expected redis addr redis:6379, got %q", cfg.Redis.Addr)
	}

	if len(cfg.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(cfg.Routes))
	}

	// First route: full-featured
	r0 := cfg.Routes[0]
	if r0.ID != "users-api" {
		t.Errorf("expected id users-api, got %q", r0.ID)
	}
	if r0.Path != "/api/v1/users/*" {
		t.Errorf("expected path /api/v1/users/*, got %q", r0.Path)
	}
	if len(r0.Methods) != 4 {
		t.Errorf("expected 4 methods, got %d", len(r0.Methods))
	}
	if len(r0.Upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d", len(r0.Upstreams))
	}
	if r0.Upstreams[0].URL != "http://users-service:8080" {
		t.Errorf("unexpected upstream URL: %q", r0.Upstreams[0].URL)
	}
	if r0.Upstreams[1].Weight != 2 {
		t.Errorf("unexpected upstream weight: %d", r0.Upstreams[1].Weight)
	}

	if r0.Auth == nil || r0.Auth.JWT == nil || !r0.Auth.JWT.Required {
		t.Error("expected JWT auth required")
	}
	if len(r0.Auth.JWT.Issuers) != 1 || r0.Auth.JWT.Issuers[0] != "https://auth.example.com" {
		t.Errorf("unexpected JWT issuers: %v", r0.Auth.JWT.Issuers)
	}

	if r0.RateLimit == nil || !r0.RateLimit.Enabled {
		t.Error("expected rate_limit enabled")
	}
	if r0.RateLimit.Requests != 100 {
		t.Errorf("expected 100 requests, got %d", r0.RateLimit.Requests)
	}
	if r0.RateLimit.Window != time.Minute {
		t.Errorf("expected 1m window, got %v", r0.RateLimit.Window)
	}

	if r0.CircuitBreaker == nil || !r0.CircuitBreaker.Enabled {
		t.Error("expected circuit_breaker enabled")
	}
	if r0.CircuitBreaker.Threshold != 5 {
		t.Errorf("expected threshold 5, got %d", r0.CircuitBreaker.Threshold)
	}

	// Second route: public health endpoint
	r1 := cfg.Routes[1]
	if r1.ID != "health" {
		t.Errorf("expected id health, got %q", r1.ID)
	}
	if r1.Path != "/health" {
		t.Errorf("expected path /health, got %q", r1.Path)
	}
	if r1.Auth != nil {
		t.Error("expected no auth config for health route")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "invalid_yaml.yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_NoRoutes(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "no_routes.yaml"))
	if err == nil {
		t.Fatal("expected error for no routes")
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "defaults.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Gateway.ShutdownTimeout != 15*time.Second {
		t.Errorf("expected default shutdown_timeout 15s, got %v", cfg.Gateway.ShutdownTimeout)
	}
	if cfg.Gateway.LogLevel != "info" {
		t.Errorf("expected default log_level info, got %q", cfg.Gateway.LogLevel)
	}
}

func TestLogLevelToSlog(t *testing.T) {
	tests := []struct {
		level string
		want  int
	}{
		{"debug", -4},
		{"info", 0},
		{"warn", 4},
		{"error", 8},
		{"unknown", 0}, // defaults to info
	}

	for _, tt := range tests {
		got := LogLevelToSlog(tt.level)
		if int(got) != tt.want {
			t.Errorf("LogLevelToSlog(%q) = %d, want %d", tt.level, int(got), tt.want)
		}
	}
}
