# Risk Log — GoGateway

| # | Risk | Likelihood | Impact | Mitigation | Status |
|---|---|---|---|---|---|
| R01 | **Redis becomes a single point of failure** | Medium | High | Rate limiter falls back to local in-memory counter if Redis is unreachable (degraded, not broken). Circuit breaker state is in-memory only, unaffected. Document that Redis is not required for proxy to start — only rate limiting fails gracefully. | ⬜ Monitor |
| R02 | **JWT library vulnerability** | Low | Critical | Use `golang-jwt/jwt/v5` — actively maintained, widely audited. Pin patch versions. Do not implement custom crypto. | ✅ Mitigated |
| R03 | **Memory leak from long-running goroutines** | Medium | Medium | All goroutines have bounded lifetimes via `context.Context` + `http.Server.Shutdown`. Review with `pprof` during development. | ⬜ Test |
| R04 | **Config reload without downtime (future)** | High | Medium | Deferred to Advanced phase. MVP uses file read on startup only. Document that changing config requires restart. | ✅ Accepted |
| R05 | **Race conditions in shared state** | Medium | High | All shared state (circuit breaker counters, balancer state) protected by `sync.RWMutex`. Run `go test -race` in CI. | ⬜ Implement |
| R06 | **Slow upstream causes connection pool exhaustion** | Medium | High | Configurable `timeout` per route. `http.Transport` with `MaxIdleConns` and `IdleConnTimeout`. Circuit breaker protects against cascading. | ⬜ Monitor |
| R07 | **Prometheus metric cardinality explosion** | Low | Medium | Labels are bounded: route IDs are finite from config, methods are HTTP verbs, status codes are 2xx/3xx/4xx/5xx. No unbounded labels (e.g., client IP). Review during Phase 5. | ✅ Mitigated |
| R08 | **API key plaintext in config file** | Medium | Medium | Keys are hashed (SHA-256) before storage. The seed file `api-keys.yaml` is for dev only. Document: never commit plaintext keys; use environment variables or secret store. | ⬜ Document |
| R09 | **Kubernetes readiness/liveness probe misconfig** | Low | Medium | `/health` endpoint returns 200 only when gateway is ready. Document probe config in K8s manifests. K8s phase includes thorough testing. | ⬜ Plan |
| R10 | **Scope creep (too many Advanced features)** | High | Medium | Strict MVP cut line. Advanced features are documented but deferred. If blocked on an MVP feature, simplify rather than gold-plate. | ✅ Active |
| R11 | **TLS termination complexity** | Low | Low | Deferred: MVP runs behind a TLS-terminating reverse proxy (ingress, LB). Document that GoGateway handles plain HTTP and expects TLS upstream. | ✅ Accepted |
| R12 | **Portfolio reviewer expects production-scale** | Medium | Low | README clearly states "portfolio project, not production-grade." Architecture docs explain trade-offs so reviewer sees intentionality, not naivete. | ✅ Mitigated |
