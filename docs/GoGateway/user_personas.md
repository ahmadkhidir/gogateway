# User Personas — GoGateway

Four key personas interact with GoGateway. For a portfolio project, the **Platform Operator** is the primary target (the person evaluating my code in an interview), but all four demonstrate different dimensions of the design.

---

## 1. Platform Operator (Primary Persona)

> *"I need a lightweight gateway to put in front of my microservices. It must be fast, observable, and easy to configure."*

**Background:** SRE / Platform Engineer / Infrastructure Developer  
**Go-experience:** Moderate to high  
**Pain points:**
- Kong/Traefik/Envoy are overkill for his team's scale
- Wants something that deploys as a single binary or small container
- Needs Prometheus metrics out of the box
- Wants to understand every line of code (no black-box dependencies)

**How GoGateway serves them:**
- Single Go binary, no heavy runtime deps
- YAML/JSON config for routes, rate limits, auth rules
- `/metrics` endpoint exposing rich Prometheus data
- Health check endpoint `/health`
- Graceful shutdown (SIGTERM drains connections)

**Portfolio relevance:** This is the persona your interviewer likely embodies. Every architecture decision should be explainable to this person.

---

## 2. API Producer (Backend Dev)

> *"I just want my service to receive requests. Don't make me think about auth, throttling, or discovery."*

**Background:** Backend / Microservices developer  
**Go-experience:** Varies  
**Pain points:**
- Doesn't want to implement auth middleware in every service
- Needs to register new routes without restarting the gateway (future: dynamic config)
- Wants to see request-level metrics for debugging

**How GoGateway serves them:**
- Registers services via config (or future: API/Redis pub-sub)
- JWT validation happens at the edge — services receive clean, authenticated requests
- Circuit breaker protects their services from cascading failures

---

## 3. API Consumer (Client / App Developer)

> *"I need to call the API with my key. If it fails, I want a clear error, not a timeout."*

**Background:** Frontend, mobile, or third-party developer  
**Go-experience:** Low or none  
**Pain points:**
- Unclear error messages from gateways
- Rate limit exceeded without clear retry headers
- JWT expiry handled poorly

**How GoGateway serves them:**
- Standard HTTP error codes + JSON error bodies (`429 Too Many Requests`, `401 Unauthorized`)
- `Retry-After` header on rate limits
- `X-Request-Id` header for tracing requests across services
- CORS support for browser clients

---

## 4. Security Engineer (Secondary Reviewer)

> *"How does it handle secrets? Token validation? Do you roll your own crypto?"*

**Background:** Security-focused engineer reviewing the codebase  
**Go-experience:** High  
**Pain points:**
- Custom crypto implementations (red flag)
- Secrets in config files
- Lack of TLS support

**How GoGateway addresses concerns:**
- JWT validation via well-audited libraries (`golang-jwt/jwt`)
- API keys stored as SHA-256 hashes, not plaintext
- No custom crypto
- TLS termination support documented
- Config file permissions documented as operational concern
