# GoGateway — Feature Roadmap

## Phased Approach: MVP → Advanced

```
MVP (Core)                     Advanced
  │                               │
  ├─ Reverse Proxy                ├─ Dynamic Config Reload
  ├─ JWT Auth                     ├─ Distributed Tracing
  ├─ Rate Limiting (Redis)        ├─ Plugin System
  ├─ Request Routing              └─ Admin API
  ├─ Circuit Breaker
  ├─ Prometheus Metrics
  ├─ API Key Management
  ├─ Service Discovery
  ├─ Load Balancing
  ├─ Docker / Compose
  └─ Kubernetes Manifests
```

---

## Phase 0 — Foundation (Week 1)

| Task | Description | Verification |
|---|---|---|
| Project scaffold | `cmd/gogateway/main.go`, `internal/` packages | `go build ./...` |
| Config loader | YAML/JSON config file parsing | Unit tests on config structs |
| Reverse proxy core | `net/http/httputil.ReverseProxy` + custom transport | E2E test: proxy to `httpbin` |
| Graceful shutdown | Signal handling (SIGTERM, SIGINT) | Manual test: kill + verify drain |
| Logger | Structured logging (`slog` or `log/slog`) | Log output inspection |

**Goal:** A binary that reads config and proxies HTTP to a single upstream.

---

## Phase 1 — Routing + Discovery (Week 2)

| Task | Description | Verification |
|---|---|---|
| Path-based routing | Match `GET /api/v1/users/*` → `users:8080` | E2E: curl different paths |
| Host-based routing | `api.example.com` → different upstream | E2E: Host header matching |
| Static service registry | In-memory map from config | Unit tests |
| Round-robin load balancer | Distribute across upstream instances | E2E: 10 requests → distribution |
| Least-connections balancer | Track active conns, pick lowest | Unit + E2E |

**Goal:** Multiple routes, multiple upstreams per route, basic load balancing.

---

## Phase 2 — Authentication (Week 3)

| Task | Description | Verification |
|---|---|---|
| JWT validation middleware | RS256 + HS256, `kid` support | Unit: valid/invalid/expired tokens |
| JWT claims extraction | Pass claims via headers to upstream | E2E: upstream receives `X-User-ID` |
| API key lookup (Redis) | `GET api-key:{hash}` → service+rate tier | Unit + integration |
| API key CRUD | CLI or config-based key creation/revocation | Scripted test |
| Auth middleware chain | "Try JWT, fallback to API key" logic | E2E: both auth methods |

**Goal:** Unauthenticated requests rejected; authenticated requests forwarded with identity context.

---

## Phase 3 — Rate Limiting (Week 4)

| Task | Description | Verification |
|---|---|---|
| Redis token bucket | Per-client + per-route counters | Integration: burst then throttle |
| Sliding window | More accurate than fixed window | Unit: window boundary test |
| Rate limit headers | `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After` | E2E: header inspection |
| Config: limits per route | `rate_limit: 100/1m` in route config | Unit config parsing |

**Goal:** Clients that exceed limits receive `429` with proper headers; limits are configurable per-route.

---

## Phase 4 — Resilience (Week 5)

| Task | Description | Verification |
|---|---|---|
| Circuit breaker middleware | Track consecutive 5xx; open/half-open/closed states | Unit: state transitions |
| Config: thresholds per route | `circuit_breaker: { threshold: 5, timeout: 30s }` | Unit config parsing |
| Half-open probe | Allow single probe request after timeout | Integration: verify recovery |
| Circuit metrics | Expose breaker state as Prometheus gauge | Manual: `/metrics` check |

**Goal:** Failing upstreams are isolated; the gateway fast-fails instead of hanging.

---

## Phase 5 — Observability (Week 5–6)

| Task | Description | Verification |
|---|---|---|
| Prometheus HTTP metrics | Request count, latency histogram, error rate | `curl /metrics` + promtool |
| Active connections gauge | In-flight request tracking | E2E: concurrent reqs |
| Per-route metrics | Labels: `route`, `method`, `status_code` | Manual: multi-route test |
| Health endpoint | `GET /health` → `{"status": "ok"}` | E2E |
| Request ID middleware | `X-Request-Id` injection + propagation | E2E: header chain |

**Goal:** Full observability story — operator can point Prometheus at `/metrics` and get useful dashboards.

---

## Phase 6 — Deployment (Week 6–7)

| Task | Description | Verification |
|---|---|---|
| Multi-stage Dockerfile | `golang:alpine` build → `scratch` run | `docker build` + size check |
| `docker-compose.yml` | GoGateway + Redis + Prometheus | `docker compose up` smoke test |
| K8s Deployment | Stateless Deployment + Service | `kubectl apply` + port-forward |
| K8s ConfigMap | Route config via ConfigMap | `kubectl rollout` check |
| README with examples | Quickstart for both Docker and K8s | Self-review |

**Goal:** Anyone can run `docker compose up` and have a working gateway in 30 seconds.

---

## Phase 7 — Advanced (Post-MVP)

### 7a. Dynamic Config Reload
- Watch config file via `fsnotify` + atomic swap
- Redis pub-sub for runtime route changes
- Admin API: `POST /routes`, `DELETE /routes/{id}`
- Zero-downtime reload (new config → new listeners → graceful old drain)

### 7b. Distributed Tracing
- OpenTelemetry SDK integration
- Trace propagation: `traceparent` header (W3C)
- Span per middleware + per upstream call
- Export to Jaeger / Zipkin compatible backend

### 7c. Plugin System
- Go `plugin` package (Linux-only, simple)
- WASM-based plugins (more portable, slower)
- Middleware plugin interface: `func(next http.Handler) http.Handler`
- Plugin hot-reload (load/unload without restart)

---

## Timeline Summary

| Phase | Weeks | Deliverable |
|---|---|---|
| 0 — Foundation | 1 | Proxy binary, config, graceful shutdown |
| 1 — Routing | 1 | Multi-route, multi-upstream, load balancing |
| 2 — Auth | 1 | JWT + API key authentication |
| 3 — Rate Limiting | 1 | Redis-backed throttling |
| 4 — Resilience | 1 | Circuit breaker |
| 5 — Observability | 1 | Prometheus metrics + health |
| 6 — Deployment | 1–2 | Docker + Compose + K8s manifests |
| **MVP Complete** | **7–8** | **Demo-ready API gateway** |
| 7 — Advanced | Ongoing | Dynamic config, tracing, plugins |
