// Package middleware provides HTTP middleware handlers for GoGateway.
package middleware

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
	"github.com/ahmadkhidir/gogateway/internal/store"
)

// RateLimiter manages per-route request rate limiting with Redis as the
// primary backend and an in-memory fallback when Redis is unavailable.
type RateLimiter struct {
	redis  *store.RedisClient
	memory *memoryLimiter
}

// NewRateLimiter creates a RateLimiter. If redisClient is nil all rate
// limiting falls back to the in-memory implementation.
func NewRateLimiter(redisClient *store.RedisClient) *RateLimiter {
	return &RateLimiter{
		redis:  redisClient,
		memory: newMemoryLimiter(),
	}
}

// Allow checks whether a request should be permitted based on the route's
// rate limit configuration. It returns the number of remaining requests
// and the duration until the rate limit window resets.
//
// If the route has no rate limit config or rate limiting is disabled the
// request is always allowed.
func (rl *RateLimiter) Allow(route *config.Route, clientID string) (allowed bool, remaining int, reset time.Duration) {
	if route.RateLimit == nil || !route.RateLimit.Enabled {
		return true, 0, 0
	}

	lim := route.RateLimit

	// Try Redis first.
	if rl.redis != nil {
		allowed, remaining, reset, err := rl.redis.CheckRateLimit(
			context.Background(),
			route.ID,
			clientID,
			lim.Requests,
			lim.Window,
		)
		if err == nil {
			return allowed, remaining, reset
		}
		slog.Warn("Redis rate limit failed, falling back to in-memory", "route", route.ID, "error", err)
	}

	// Fall back to in-memory.
	return rl.memory.allow(route.ID, clientID, lim.Requests, lim.Window)
}

// ResolveClientID determines the client identifier for rate limiting.
// Priority:
//  1. X-User-ID header (JWT-authenticated)
//  2. X-API-Key-ID header (API-key-authenticated)
//  3. X-Forwarded-For header
//  4. RemoteAddr (fallback)
func ResolveClientID(r requestHeaders, route *config.Route) string {
	if route.RateLimit == nil || !route.RateLimit.PerClient {
		return route.ID // shared per-route counter
	}

	if u := r.Header("X-User-ID"); u != "" {
		return u
	}
	if k := r.Header("X-API-Key-ID"); k != "" {
		return k
	}
	if fwd := r.Header("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return r.RemoteAddr()
}

// requestHeaders is an interface satisfied by *http.Request for testability.
type requestHeaders interface {
	Header(string) string
	RemoteAddr() string
}

// --- In-memory fallback ---

type memoryEntry struct {
	count   int
	resetAt time.Time
}

type memoryLimiter struct {
	mu       sync.Mutex
	entries  map[string]*memoryEntry
}

func newMemoryLimiter() *memoryLimiter {
	return &memoryLimiter{
		entries: make(map[string]*memoryEntry),
	}
}

func (m *memoryLimiter) allow(routeID, clientID string, limit int, window time.Duration) (bool, int, time.Duration) {
	key := routeID + ":" + clientID

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	entry, exists := m.entries[key]

	// Reset if window has expired.
	if !exists || now.After(entry.resetAt) {
		entry = &memoryEntry{
			count:   0,
			resetAt: now.Add(window),
		}
		m.entries[key] = entry
	}

	entry.count++
	remaining := limit - entry.count
	if remaining < 0 {
		remaining = 0
	}
	reset := time.Until(entry.resetAt)
	if reset < 0 {
		reset = 0
	}

	if entry.count <= limit {
		return true, remaining, reset
	}

	return false, 0, reset
}

// --- Header accessor for *http.Request ---

// requestAdapter adapts *http.Request to the requestHeaders interface.
type requestAdapter struct {
	getHeader func(string) string
	remoteAddr string
}

func (a *requestAdapter) Header(key string) string  { return a.getHeader(key) }
func (a *requestAdapter) RemoteAddr() string         { return a.remoteAddr }

// AdaptRequest wraps an *http.Request as a requestHeaders.
func AdaptRequest(getHeader func(string) string, remoteAddr string) requestHeaders {
	return &requestAdapter{
		getHeader:  getHeader,
		remoteAddr: remoteAddr,
	}
}

// Ensure memoryLimiter doesn't grow unbounded: clean expired entries
// periodically (called via a time.Ticker in production).
func (m *memoryLimiter) cleanExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, v := range m.entries {
		if now.After(v.resetAt) {
			delete(m.entries, k)
		}
	}
}

// StartCleanup runs a background goroutine that periodically removes
// expired in-memory rate limit entries.
func (rl *RateLimiter) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			rl.memory.cleanExpired()
		}
	}()
}

// --- Helper: ceil division for sliding window ---

func ceilDiv(a, b int) int {
	return int(math.Ceil(float64(a) / float64(b)))
}
