# Architecture — GoGateway

## High-Level Component Diagram

```mermaid
flowchart TD
    Client[("HTTP Client")]

    subgraph GoGateway["GoGateway"]
        GL["Listener :8080"]
        
        subgraph MW["Middleware Chain"]
            direction LR
            JWT["JWT Auth\nToken Validation"]
            RL["Rate Limiter\nRedis Token Bucket"]
            CB["Circuit Breaker\n3-State Machine"]
            MR["Metrics Recorder\nPrometheus"]
        end

        subgraph Routing["Routing Layer"]
            Router{"Router"}
            PHM["Path / Host\nMatcher"]
            LB["Load Balancer"]
            RR["Round Robin"]
            LC["Least Connections"]
            RP["httputil.ReverseProxy"]
        end
    end

    subgraph Infra["Infrastructure"]
        Redis[("Redis\n· Rate Counters\n· API Key Store\n· Service Registry")]
        Prom["Prometheus\n/metrics endpoint"]
        CFG["Config File\n· gogateway.yaml\n· api-keys.yaml"]
    end

    subgraph Upstream["Upstream Services"]
        SvcA["Service A\n:8081"]
        SvcB["Service B\n:8082"]
    end

    Client -->|"HTTP Request"| GL
    GL --> JWT --> RL --> CB --> MR
    MR --> Router
    Router --> PHM
    PHM --> LB
    LB --> RR
    LB --> LC
    RR --> RP
    LC --> RP
    RP --> SvcA
    RP --> SvcB

    RL -.->|"INCR / EXPIRE"| Redis
    MR -.->|"scrape :9090"| Prom
    GL -.->|"read"| CFG
```

---

## Request Lifecycle

```mermaid
flowchart TD
    S((Start)) --> L1["1️⃣ Listen on :8080\nHTTP request arrives"]
    L1 --> L2["2️⃣ Inject X-Request-Id\nUUID if missing"]

    L2 --> L3{"3️⃣ JWT Auth\nCheck Authorization: Bearer"}
    L3 -->|"Valid token → extract claims"| L4
    L3 -->|"Invalid / missing"| R401["⛔ 401 Unauthorized"]

    L4{"4️⃣ Rate Limit\nRedis token bucket"} -->|"Under limit → increment"| L5
    L4 -->|"Over limit"| R429["⛔ 429 Too Many Requests\n+ Retry-After header"]

    L5{"5️⃣ Route Match\nMethod + Path + Host"} -->|"Match found → get upstream"| L6
    L5 -->|"No match"| R404["⛔ 404 Not Found"]

    L6{"6️⃣ Circuit Breaker\nCheck upstream state"} -->|"Closed"| L7
    L6 -->|"Open"| R503["⛔ 503 Service Unavailable"]
    L6 -->|"Half-Open"| L7

    L7["7️⃣ Load Balance\nPick instance"] --> L8["8️⃣ Reverse Proxy\nhttputil.ReverseProxy"]
    L8 --> L9["9️⃣ Record Metrics\nlatency · status · active conns"]
    L9 --> L10["🔟 Respond to Client\nWrite response + headers"]

    R401 --> E((End))
    R429 --> E
    R404 --> E
    R503 --> E
    L10 --> E
```

---

## Internal Package Structure

```
cmd/
└── gogateway/
    └── main.go              # Entry point, signal handling, server start

internal/
├── config/
│   ├── config.go            # Config struct + YAML/JSON loader
│   └── config_test.go
├── server/
│   ├── server.go            # HTTP server setup, graceful shutdown
│   └── server_test.go
├── middleware/
│   ├── jwt.go               # JWT validation middleware
│   ├── jwt_test.go
│   ├── apikey.go            # API key authentication middleware
│   ├── ratelimit.go         # Rate limiting middleware
│   ├── ratelimit_test.go
│   ├── circuitbreaker.go    # Circuit breaker middleware
│   ├── circuitbreaker_test.go
│   ├── requestid.go         # X-Request-Id injection
│   └── metrics.go           # Prometheus metrics recording
├── router/
│   ├── router.go            # Route matching (path + host)
│   ├── router_test.go
│   └── route.go             # Route struct definition
├── balancer/
│   ├── balancer.go          # Load balancer interface
│   ├── roundrobin.go        # Round-robin implementation
│   ├── leastconn.go         # Least-connections implementation
│   └── balancer_test.go
├── discovery/
│   ├── static.go            # Static service registry from config
│   ├── redis.go             # Redis-backed dynamic registry
│   └── discovery_test.go
├── store/
│   ├── redis.go             # Redis client wrapper (rate limit + API keys)
│   └── store_test.go
└── metrics/
    ├── prometheus.go        # Prometheus metric definitions
    └── metrics_test.go
```

---

## Key Design Decisions

| Decision | Rationale |
|---|---|
| **Middleware as `http.Handler` wrappers** | Idiomatic Go, composable, testable with `httptest` |
| **Config file (YAML) over CLI flags** | Complex nested structures are easier to read/write in YAML; flags for overrides (e.g., `--config-path`) |
| **Redis for rate limiting** | Atomic counters (INCR/EXPIRE), shared state across gateway instances, no external rate limit library needed |
| **Round-robin + least-connections** | Two strategies cover "simple" and "production" needs; interface makes adding more trivial |
| **Prometheus client library** | Industry standard, `promhttp.HandlerFor` exposes metrics on a separate port or path |
| **No framework** | Portfolio demonstration of `net/http` mastery; no `gin`, `chi`, `mux` — plain `http.ServeMux` + `http.Handler` |
| **Graceful shutdown** | `http.Server.Shutdown()` with configurable timeout; ensures in-flight requests complete |
