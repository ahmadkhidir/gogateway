package router

import (
	"net/http/httptest"
	"testing"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

func TestMatch_ExactPath(t *testing.T) {
	r := New([]config.Route{
		{ID: "health", Path: "/health", Methods: []string{"GET"}, Upstreams: []config.Upstream{{URL: "http://h:1"}}},
		{ID: "users", Path: "/api/users", Methods: []string{"GET"}, Upstreams: []config.Upstream{{URL: "http://u:1"}}},
	})

	tests := []struct {
		method string
		path   string
		want   string // route ID; empty means no match
	}{
		{"GET", "/health", "health"},
		{"GET", "/api/users", "users"},
		{"GET", "/unknown", ""},
		{"POST", "/health", ""}, // method mismatch
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			route := r.Match(req)
			if tt.want == "" {
				if route != nil {
					t.Errorf("expected no match, got route %q", route.ID)
				}
				return
			}
			if route == nil {
				t.Fatalf("expected match for route %q, got nil", tt.want)
			}
			if route.ID != tt.want {
				t.Errorf("expected route %q, got %q", tt.want, route.ID)
			}
		})
	}
}

func TestMatch_WildcardPath(t *testing.T) {
	r := New([]config.Route{
		{ID: "api", Path: "/api/*", Methods: []string{"GET"}, Upstreams: []config.Upstream{{URL: "http://a:1"}}},
		{ID: "catchall", Path: "/*", Methods: []string{"GET"}, Upstreams: []config.Upstream{{URL: "http://c:1"}}},
	})

	tests := []struct {
		path string
		want string
	}{
		{"/api/v1/users", "api"},
		{"/api/v1/users/123", "api"},
		{"/api", "api"},          // prefix without trailing slash
		{"/health", "catchall"},  // falls through to catch-all
		{"/", "catchall"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			route := r.Match(req)
			if route == nil {
				t.Fatalf("expected match for %q, got nil", tt.path)
			}
			if route.ID != tt.want {
				t.Errorf("path %q: expected route %q, got %q", tt.path, tt.want, route.ID)
			}
		})
	}
}

func TestMatch_HostBased(t *testing.T) {
	r := New([]config.Route{
		{
			ID: "internal",
			Path: "/*",
			Methods: []string{"GET"},
			Hosts: []string{"internal.example.com"},
			Upstreams: []config.Upstream{{URL: "http://internal:1"}},
		},
		{
			ID: "public",
			Path: "/*",
			Methods: []string{"GET"},
			Hosts: []string{"api.example.com"},
			Upstreams: []config.Upstream{{URL: "http://public:1"}},
		},
	})

	tests := []struct {
		host string
		want string
	}{
		{"internal.example.com", "internal"},
		{"api.example.com", "public"},
		{"unknown.example.com", ""}, // no route without hosts
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			route := r.Match(req)
			if tt.want == "" {
				if route != nil {
					t.Errorf("expected no match, got route %q", route.ID)
				}
				return
			}
			if route == nil {
				t.Fatalf("expected route %q for host %q", tt.want, tt.host)
			}
			if route.ID != tt.want {
				t.Errorf("host %q: expected route %q, got %q", tt.host, tt.want, route.ID)
			}
		})
	}
}

func TestMatch_FirstMatchWins(t *testing.T) {
	// Routes are evaluated in order; first match wins.
	r := New([]config.Route{
		{ID: "specific", Path: "/api/v1", Methods: []string{"GET"}, Upstreams: []config.Upstream{{URL: "http://s:1"}}},
		{ID: "wildcard", Path: "/api/*", Methods: []string{"GET"}, Upstreams: []config.Upstream{{URL: "http://w:1"}}},
	})

	req := httptest.NewRequest("GET", "/api/v1", nil)
	route := r.Match(req)
	if route == nil || route.ID != "specific" {
		t.Errorf("expected 'specific' to match first, got %v", route)
	}
}

func TestMatch_NoMethods(t *testing.T) {
	// Route with no Methods should match any method.
	r := New([]config.Route{
		{ID: "any", Path: "/any", Upstreams: []config.Upstream{{URL: "http://a:1"}}},
	})

	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/any", nil)
		route := r.Match(req)
		if route == nil {
			t.Errorf("expected route to match method %s", method)
		}
	}
}

func TestMatch_NilOnEmptyRoutes(t *testing.T) {
	r := New(nil)
	req := httptest.NewRequest("GET", "/anything", nil)
	if route := r.Match(req); route != nil {
		t.Errorf("expected nil, got route %q", route.ID)
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/health", "/health", true},
		{"/health", "/health/", false},
		{"/health", "/Health", false},
		{"/api/*", "/api/v1/users", true},
		{"/api/*", "/api", true},
		{"/api/*", "/api/", true},
		{"/api/*", "/api/v1/users/123", true},
		{"/api/*", "/other", false},
		{"/*", "/anything", true},
		{"/*", "/", true},
		{"/exact", "/exact/extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.path, func(t *testing.T) {
			got := matchPath(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
