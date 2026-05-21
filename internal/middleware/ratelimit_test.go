package middleware

import (
	"testing"
	"time"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

// mockHeaders implements requestHeaders for testing.
type mockHeaders struct {
	headers    map[string]string
	remoteAddr string
}

func (m *mockHeaders) Header(key string) string  { return m.headers[key] }
func (m *mockHeaders) RemoteAddr() string         { return m.remoteAddr }

func newMockHeaders() *mockHeaders {
	return &mockHeaders{
		headers: make(map[string]string),
	}
}

func TestRateLimiter_Allow_NoConfig(t *testing.T) {
	rl := NewRateLimiter(nil) // in-memory only

	route := &config.Route{
		ID:        "test",
		RateLimit: nil, // no rate limit config
	}

	allowed, remaining, reset := rl.Allow(route, "client-1")
	if !allowed {
		t.Error("expected allowed when no rate limit config")
	}
	if remaining != 0 {
		t.Errorf("expected remaining 0, got %d", remaining)
	}
	if reset != 0 {
		t.Errorf("expected reset 0, got %v", reset)
	}
}

func TestRateLimiter_Allow_Disabled(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "test",
		RateLimit: &config.RateLimitCfg{
			Enabled: false,
			Requests: 10,
			Window:   time.Minute,
		},
	}

	allowed, _, _ := rl.Allow(route, "client-1")
	if !allowed {
		t.Error("expected allowed when rate limit is disabled")
	}
}

func TestRateLimiter_Allow_UnderLimit(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "test",
		RateLimit: &config.RateLimitCfg{
			Enabled:  true,
			Requests: 5,
			Window:   time.Minute,
		},
	}

	for i := 0; i < 5; i++ {
		allowed, remaining, reset := rl.Allow(route, "client-1")
		if !allowed {
			t.Errorf("request %d: expected allowed, got denied", i+1)
		}
		if remaining != 5-(i+1) {
			t.Errorf("request %d: expected remaining %d, got %d", i+1, 5-(i+1), remaining)
		}
		if reset <= 0 || reset > time.Minute {
			t.Errorf("request %d: expected reset between 0 and 1m, got %v", i+1, reset)
		}
	}
}

func TestRateLimiter_Allow_OverLimit(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "test",
		RateLimit: &config.RateLimitCfg{
			Enabled:  true,
			Requests: 3,
			Window:   time.Minute,
		},
	}

	// Use up the limit.
	for i := 0; i < 3; i++ {
		allowed, _, _ := rl.Allow(route, "client-1")
		if !allowed {
			t.Errorf("request %d: expected allowed", i+1)
		}
	}

	// 4th request should be denied.
	allowed, remaining, reset := rl.Allow(route, "client-1")
	if allowed {
		t.Error("expected 4th request to be denied")
	}
	if remaining != 0 {
		t.Errorf("expected remaining 0, got %d", remaining)
	}
	if reset <= 0 {
		t.Errorf("expected positive reset duration, got %v", reset)
	}
}

func TestRateLimiter_Allow_WindowReset(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "test",
		RateLimit: &config.RateLimitCfg{
			Enabled:  true,
			Requests: 2,
			Window:   50 * time.Millisecond, // short window for testing
		},
	}

	// Use up the limit.
	rl.Allow(route, "client-1")
	rl.Allow(route, "client-1")

	// 3rd request denied.
	allowed, _, _ := rl.Allow(route, "client-1")
	if allowed {
		t.Error("expected 3rd request to be denied within window")
	}

	// Wait for window to expire.
	time.Sleep(60 * time.Millisecond)

	// Should be allowed again.
	allowed, remaining, _ := rl.Allow(route, "client-1")
	if !allowed {
		t.Error("expected allowed after window reset")
	}
	if remaining != 1 {
		t.Errorf("expected remaining 1 after reset, got %d", remaining)
	}
}

func TestRateLimiter_Allow_PerClient(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "test",
		RateLimit: &config.RateLimitCfg{
			Enabled:   true,
			Requests:  2,
			Window:    time.Minute,
			PerClient: true,
		},
	}

	// Client A uses its limit.
	rl.Allow(route, "client-A")
	rl.Allow(route, "client-A")
	allowed, _, _ := rl.Allow(route, "client-A")
	if allowed {
		t.Error("expected client A to be rate limited")
	}

	// Client B should still have its own limit.
	allowed, remaining, _ := rl.Allow(route, "client-B")
	if !allowed {
		t.Error("expected client B to be allowed (separate counter)")
	}
	if remaining != 1 {
		t.Errorf("expected client B remaining 1, got %d", remaining)
	}
}

func TestRateLimiter_Allow_SharedCounter(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "test",
		RateLimit: &config.RateLimitCfg{
			Enabled:   true,
			Requests:  3,
			Window:    time.Minute,
			PerClient: false,
		},
	}

	// When PerClient=false, the caller (via ResolveClientID) passes the
	// route ID as the clientID so all requests share the same counter.
	clientID := route.ID // this is what ResolveClientID returns for PerClient=false

	rl.Allow(route, clientID)
	rl.Allow(route, clientID)
	allowed, _, _ := rl.Allow(route, clientID)
	if !allowed {
		t.Error("expected 3rd request allowed (limit 3)")
	}

	// 4th request with same clientID → denied (over limit).
	allowed, _, _ = rl.Allow(route, clientID)
	if allowed {
		t.Error("expected 4th request to be denied — shared route counter exceeded")
	}
}

func TestResolveClientID_JWT(t *testing.T) {
	mh := newMockHeaders()
	mh.headers["X-User-ID"] = "user-42"

	route := &config.Route{RateLimit: &config.RateLimitCfg{PerClient: true}}
	id := ResolveClientID(mh, route)
	if id != "user-42" {
		t.Errorf("expected user-42, got %q", id)
	}
}

func TestResolveClientID_APIKey(t *testing.T) {
	mh := newMockHeaders()
	mh.headers["X-API-Key-ID"] = "key_k7d92h"

	route := &config.Route{RateLimit: &config.RateLimitCfg{PerClient: true}}
	id := ResolveClientID(mh, route)
	if id != "key_k7d92h" {
		t.Errorf("expected key_k7d92h, got %q", id)
	}
}

func TestResolveClientID_XForwardedFor(t *testing.T) {
	mh := newMockHeaders()
	mh.headers["X-Forwarded-For"] = "10.0.0.1"

	route := &config.Route{RateLimit: &config.RateLimitCfg{PerClient: true}}
	id := ResolveClientID(mh, route)
	if id != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %q", id)
	}
}

func TestResolveClientID_RemoteAddr(t *testing.T) {
	mh := newMockHeaders()
	mh.remoteAddr = "192.168.1.1:54321"

	route := &config.Route{RateLimit: &config.RateLimitCfg{PerClient: true}}
	id := ResolveClientID(mh, route)
	if id != "192.168.1.1:54321" {
		t.Errorf("expected remote addr, got %q", id)
	}
}

func TestResolveClientID_PerClientFalse(t *testing.T) {
	mh := newMockHeaders()
	mh.headers["X-User-ID"] = "user-42"

	// PerClient=false → returns route ID.
	route := &config.Route{
		ID:        "my-route",
		RateLimit: &config.RateLimitCfg{PerClient: false},
	}
	id := ResolveClientID(mh, route)
	if id != "my-route" {
		t.Errorf("expected route ID my-route, got %q", id)
	}
}

func TestResolveClientID_NoRateLimitConfig(t *testing.T) {
	mh := newMockHeaders()
	mh.headers["X-User-ID"] = "user-42"

	// RateLimit is nil → returns route ID.
	route := &config.Route{
		ID:        "my-route",
		RateLimit: nil,
	}
	id := ResolveClientID(mh, route)
	if id != "my-route" {
		t.Errorf("expected route ID my-route, got %q", id)
	}
}

func TestMemoryLimiter_CleanExpired(t *testing.T) {
	rl := NewRateLimiter(nil)

	route := &config.Route{
		ID: "clean-test",
		RateLimit: &config.RateLimitCfg{
			Enabled:  true,
			Requests: 1,
			Window:   10 * time.Millisecond,
		},
	}

	rl.Allow(route, "client-x")
	time.Sleep(20 * time.Millisecond)

	// Clean expired entries.
	rl.memory.cleanExpired()

	// After cleanup, the entry should be gone and a new request allowed.
	allowed, _, _ := rl.Allow(route, "client-x")
	if !allowed {
		t.Error("expected allowed after cleanup of expired entry")
	}
}
