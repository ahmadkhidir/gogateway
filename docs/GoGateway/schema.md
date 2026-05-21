# Schema Design — GoGateway

This document defines the data structures and storage schemas for GoGateway MVP.

---

## 1. Config File (YAML)

```yaml
# gogateway.yaml
gateway:
  listen: ":8080"
  metrics_listen: ":9090"           # separate port for /metrics (optional)
  shutdown_timeout: 15s
  log_level: "info"                 # debug | info | warn | error

redis:
  addr: "localhost:6379"
  password: ""
  db: 0
  pool_size: 20

routes:
  - id: "users-api"
    path: "/api/v1/users/*"
    methods: ["GET", "POST", "PUT", "DELETE"]
    hosts: ["api.example.com"]
    upstreams:
      - url: "http://users-service:8080"
        weight: 1
    auth:
      jwt:
        required: true
        issuers: ["https://auth.example.com"]
      api_key:
        required: false
    rate_limit:
      enabled: true
      requests: 100
      window: 1m                    # 1m, 5m, 1h
      per_client: true              # separate limit per API key / client IP
    circuit_breaker:
      enabled: true
      threshold: 5                  # consecutive 5xx
      timeout: 30s                  # time before half-open
      half_open_max_requests: 3
    timeout: 30s
    retry: 2                        # max retries on 5xx (idempotent only)

  - id: "health"
    path: "/health"
    methods: ["GET"]
    auth:
      jwt:
        required: false
      api_key:
        required: false
    upstreams:
      - url: "http://health-service:8080"
```

---

## 2. Route Model (Internal Go Struct)

```go
type Route struct {
    ID             string         `yaml:"id" json:"id"`
    Path           string         `yaml:"path" json:"path"`         // e.g., "/api/v1/users/*"
    Methods        []string       `yaml:"methods" json:"methods"`
    Hosts          []string       `yaml:"hosts,omitempty" json:"hosts,omitempty"`
    Upstreams      []Upstream     `yaml:"upstreams" json:"upstreams"`
    Auth           *AuthConfig    `yaml:"auth,omitempty" json:"auth,omitempty"`
    RateLimit      *RateLimitCfg  `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
    CircuitBreaker *CircuitBrkCfg `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
    Timeout        time.Duration  `yaml:"timeout,omitempty" json:"timeout,omitempty"`
    Retry          int            `yaml:"retry,omitempty" json:"retry,omitempty"`
}

type Upstream struct {
    URL    string `yaml:"url" json:"url"`
    Weight int    `yaml:"weight,omitempty" json:"weight,omitempty"`
}

type AuthConfig struct {
    JWT    *JWTConfig    `yaml:"jwt,omitempty" json:"jwt,omitempty"`
    APIKey *APIKeyConfig `yaml:"api_key,omitempty" json:"api_key,omitempty"`
}

type JWTConfig struct {
    Required bool     `yaml:"required" json:"required"`
    Issuers  []string `yaml:"issuers,omitempty" json:"issuers,omitempty"`
}

type APIKeyConfig struct {
    Required bool `yaml:"required" json:"required"`
}

type RateLimitCfg struct {
    Enabled    bool          `yaml:"enabled" json:"enabled"`
    Requests   int           `yaml:"requests" json:"requests"`
    Window     time.Duration `yaml:"window" json:"window"`
    PerClient  bool          `yaml:"per_client" json:"per_client"`
}

type CircuitBrkCfg struct {
    Enabled           bool          `yaml:"enabled" json:"enabled"`
    Threshold         int           `yaml:"threshold" json:"threshold"`
    Timeout           time.Duration `yaml:"timeout" json:"timeout"`
    HalfOpenMaxReqs   int           `yaml:"half_open_max_requests" json:"half_open_max_requests"`
}
```

---

## 3. Redis Key Schema

### 3a. Rate Limiting

```
# Token bucket: client+route counter
rate:{route_id}:{client_id}      →  Integer (remaining tokens)
  TTL: matches the rate window (e.g., 60s for 1m window)

# Sliding window log
rate:{route_id}:{client_id}:log  →  Sorted Set (epoch timestamps of requests)
  TTL: window duration + 10s buffer
```

### 3b. API Key Storage

```
# API key hash → key metadata
apikey:{sha256_hash}              →  Hash
  Fields:
    id           →  "key_k7d92h"       # human-readable key ID
    service      →  "users-service"    # optional: restrict to specific route
    rate_tier    →  "basic"            # "basic" | "pro" | "unlimited"
    created_at   →  "2026-05-21T00:00:00Z"
    revoked      →  "false"

# Reverse lookup: list all keys
apikeys:index                      →  Set of {sha256_hash}
```

### 3c. Service Registry

```
# Static registry (from config) is in-memory.
# For dynamic discovery (future/optional):
service:{service_name}             →  Set of endpoint URLs
  Members: "http://10.0.1.5:8080", "http://10.0.2.5:8080"

service:{service_name}:ttl         →  Key with TTL (heartbeat-based expiry)
```

---

## 4. Circuit Breaker State (In-Memory)

```go
type CircuitState int

const (
    StateClosed   CircuitState = iota  // Normal operation
    StateOpen                          // Fast-fail
    StateHalfOpen                      // Probing recovery
)

type CircuitBreaker struct {
    mu            sync.RWMutex
    State         CircuitState
    FailureCount  int
    LastFailure   time.Time
    OpenTime      time.Time
    Config        CircuitBrkCfg
}

// Keyed by upstream pool ID (route ID + upstream URL)
// Stored in a map: map[string]*CircuitBreaker
```

---

## 5. Prometheus Metrics Schema

```go
// Defined in internal/metrics/prometheus.go
var (
    // Request counter: partitioned by route, method, status code
    RequestsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "gogateway_requests_total",
            Help: "Total requests processed",
        },
        []string{"route", "method", "status_code"},
    )

    // Latency histogram: in seconds
    RequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "gogateway_request_duration_seconds",
            Help:    "Request latency in seconds",
            Buckets: prometheus.DefBuckets, // .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10
        },
        []string{"route", "method"},
    )

    // Active connections gauge
    ActiveConnections = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "gogateway_active_connections",
            Help: "Number of currently active connections",
        },
    )

    // Circuit breaker state: 0=closed, 1=open, 2=half-open
    CircuitBreakerState = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "gogateway_circuit_breaker_state",
            Help: "Circuit breaker state per upstream (0=closed, 1=open, 2=half-open)",
        },
        []string{"route", "upstream"},
    )

    // Rate limit rejections
    RateLimitRejections = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "gogateway_rate_limit_rejections_total",
            Help: "Total rate-limited requests",
        },
        []string{"route"},
    )
)
```

---

## 6. API Key File (For Local Development / Seeding)

```yaml
# api-keys.yaml
api_keys:
  - id: "key_k7d92h"
    key: "gw_live_abc123def456"       # plaintext (hashed on load)
    service: ""                        # "" = all services
    rate_tier: "pro"
  - id: "key_b3x81m"
    key: "gw_test_xyz789"
    service: "users-api"
    rate_tier: "basic"
```

> **Note:** In production, keys are managed via the Redis store + CLI/admin tool. The file seed is for development convenience only.
