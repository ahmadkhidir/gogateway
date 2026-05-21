// Package config provides the configuration model and YAML loader for GoGateway.
//
// The configuration is specified via a YAML file (default: gogateway.yaml) and
// covers the gateway listener, Redis connection, route definitions, and per-route
// settings for authentication, rate limiting, and circuit breakers.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure for GoGateway.
type Config struct {
	Gateway GatewayConfig `yaml:"gateway"`
	Redis   RedisConfig   `yaml:"redis"`
	Routes  []Route       `yaml:"routes"`
}

// GatewayConfig holds settings for the HTTP listener, metrics, shutdown behaviour,
// and logging.
type GatewayConfig struct {
	Listen          string        `yaml:"listen"`
	MetricsListen   string        `yaml:"metrics_listen,omitempty"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	LogLevel        string        `yaml:"log_level"`
}

// RedisConfig defines the connection parameters for the Redis backend.
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password,omitempty"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

// Route defines a single API route that GoGateway will match and forward.
type Route struct {
	ID             string         `yaml:"id" json:"id"`
	Path           string         `yaml:"path" json:"path"`
	Methods        []string       `yaml:"methods" json:"methods"`
	Hosts          []string       `yaml:"hosts,omitempty" json:"hosts,omitempty"`
	Upstreams      []Upstream     `yaml:"upstreams" json:"upstreams"`
	Auth           *AuthConfig    `yaml:"auth,omitempty" json:"auth,omitempty"`
	RateLimit      *RateLimitCfg  `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	CircuitBreaker *CircuitBrkCfg `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
	Timeout        time.Duration  `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retry          int            `yaml:"retry,omitempty" json:"retry,omitempty"`
}

// Upstream represents a backend service endpoint that requests are forwarded to.
type Upstream struct {
	URL    string `yaml:"url" json:"url"`
	Weight int    `yaml:"weight,omitempty" json:"weight,omitempty"`
}

// AuthConfig holds optional JWT and API-key authentication settings for a route.
type AuthConfig struct {
	JWT    *JWTConfig    `yaml:"jwt,omitempty" json:"jwt,omitempty"`
	APIKey *APIKeyConfig `yaml:"api_key,omitempty" json:"api_key,omitempty"`
}

// JWTConfig configures JWT validation for a route.
type JWTConfig struct {
	Required bool     `yaml:"required" json:"required"`
	Issuers  []string `yaml:"issuers,omitempty" json:"issuers,omitempty"`
}

// APIKeyConfig configures API-key authentication for a route.
type APIKeyConfig struct {
	Required bool `yaml:"required" json:"required"`
}

// RateLimitCfg configures rate limiting for a route.
type RateLimitCfg struct {
	Enabled   bool          `yaml:"enabled" json:"enabled"`
	Requests  int           `yaml:"requests" json:"requests"`
	Window    time.Duration `yaml:"window" json:"window"`
	PerClient bool          `yaml:"per_client" json:"per_client"`
}

// CircuitBrkCfg configures the circuit breaker for a route's upstreams.
type CircuitBrkCfg struct {
	Enabled         bool          `yaml:"enabled" json:"enabled"`
	Threshold       int           `yaml:"threshold" json:"threshold"`
	Timeout         time.Duration `yaml:"timeout" json:"timeout"`
	HalfOpenMaxReqs int           `yaml:"half_open_max_requests" json:"half_open_max_requests"`
}

// Load reads a YAML config file from path and returns the parsed Config.
// It returns an error if the file cannot be read or does not contain valid YAML.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: validate %q: %w", path, err)
	}

	return &cfg, nil
}

// LogLevelToSlog converts a config log level string to a slog.Level.
// Valid values: "debug", "info", "warn", "error". Defaults to slog.LevelInfo
// for unrecognised values.
func LogLevelToSlog(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// validate checks that the configuration is internally consistent.
func (c *Config) validate() error {
	if c.Gateway.Listen == "" {
		return fmt.Errorf("gateway.listen is required")
	}
	if c.Gateway.ShutdownTimeout <= 0 {
		c.Gateway.ShutdownTimeout = 15 * time.Second
	}
	if c.Gateway.LogLevel == "" {
		c.Gateway.LogLevel = "info"
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}
	for i, r := range c.Routes {
		if r.ID == "" {
			return fmt.Errorf("routes[%d].id is required", i)
		}
		if r.Path == "" {
			return fmt.Errorf("routes[%d].path is required", i)
		}
		if len(r.Upstreams) == 0 {
			return fmt.Errorf("routes[%d].upstreams: at least one upstream required", i)
		}
		for j, u := range r.Upstreams {
			if u.URL == "" {
				return fmt.Errorf("routes[%d].upstreams[%d].url is required", i, j)
			}
		}
	}
	return nil
}
