package middleware

import (
	"sync"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	StateClosed   CircuitState = iota // Normal operation — requests pass through.
	StateOpen                         // Fast-fail — all requests are rejected.
	StateHalfOpen                     // Probing — limited requests allowed to test recovery.
)

// String returns a human-readable name for the state.
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern for upstream
// endpoints. It tracks consecutive failures and temporarily stops
// routing requests to unhealthy upstreams, allowing them time to recover.
//
// State machine:
//
//	Closed ──(threshold failures)──▶ Open
//	Open   ──(timeout elapsed)─────▶ HalfOpen
//	HalfOpen ──(probe succeeds)────▶ Closed
//	HalfOpen ──(probe fails)───────▶ Open
type CircuitBreaker struct {
	mu           sync.RWMutex
	State        CircuitState
	FailureCount int
	LastFailure  time.Time
	OpenTime     time.Time
	config       config.CircuitBrkCfg
}

// NewCircuitBreaker creates a new circuit breaker with the given config.
func NewCircuitBreaker(cfg config.CircuitBrkCfg) *CircuitBreaker {
	return &CircuitBreaker{
		State:  StateClosed,
		config: cfg,
	}
}

// Allow checks whether a request should be forwarded to the upstream.
// Returns true if the request is allowed. When the circuit is open the
// request is rejected (caller should return 503).
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.State {
	case StateClosed:
		return true

	case StateOpen:
		if time.Since(cb.OpenTime) >= cb.config.Timeout {
			cb.State = StateHalfOpen
			cb.FailureCount = 0
			cb.LastFailure = time.Time{}
			return true
		}
		return false

	case StateHalfOpen:
		return true

	default:
		return true
	}
}

// RecordFailure records an upstream failure (5xx or transport error).
// If the failure threshold is reached the circuit transitions to Open.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.FailureCount++
	cb.LastFailure = time.Now()

	switch cb.State {
	case StateClosed:
		if cb.FailureCount >= cb.config.Threshold {
			cb.State = StateOpen
			cb.OpenTime = time.Now()
		}
	case StateHalfOpen:
		// Probe failed — back to open with a fresh timeout.
		cb.State = StateOpen
		cb.OpenTime = time.Now()
	case StateOpen:
		// Already open; just track the ongoing failure.
	}
}

// RecordSuccess records a successful upstream response.
// In HalfOpen state this closes the circuit. In Closed state it resets
// the failure count.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.State {
	case StateHalfOpen:
		cb.State = StateClosed
		cb.FailureCount = 0
		cb.OpenTime = time.Time{}
		cb.LastFailure = time.Time{}
	case StateClosed:
		// Reset failure count on every success.
		cb.FailureCount = 0
	}
}

// StateSnapshot returns a read-only snapshot of the breaker's current state.
func (cb *CircuitBreaker) StateSnapshot() (CircuitState, int, time.Time, time.Time) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.State, cb.FailureCount, cb.LastFailure, cb.OpenTime
}

// --- BreakerStore ---

// BreakerStore manages a collection of circuit breakers keyed by
// "routeID:upstreamURL".
type BreakerStore struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

// NewBreakerStore creates an empty breaker store.
func NewBreakerStore() *BreakerStore {
	return &BreakerStore{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// GetOrCreate returns the circuit breaker for the given upstream pool.
// If one does not exist it is created with the provided config.
func (bs *BreakerStore) GetOrCreate(routeID, upstreamURL string, cfg config.CircuitBrkCfg) *CircuitBreaker {
	key := routeID + ":" + upstreamURL

	bs.mu.RLock()
	cb, ok := bs.breakers[key]
	bs.mu.RUnlock()
	if ok {
		return cb
	}

	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Double-check after acquiring write lock.
	if cb, ok := bs.breakers[key]; ok {
		return cb
	}

	cb = NewCircuitBreaker(cfg)
	bs.breakers[key] = cb
	return cb
}

// Snapshot returns a copy of all breaker states (for metrics / debugging).
func (bs *BreakerStore) Snapshot() map[string]CircuitState {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	snap := make(map[string]CircuitState, len(bs.breakers))
	for k, cb := range bs.breakers {
		state, _, _, _ := cb.StateSnapshot()
		snap[k] = state
	}
	return snap
}
