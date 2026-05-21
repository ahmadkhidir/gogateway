package discovery

import (
	"testing"

	"github.com/ahmadkhidir/gogateway/internal/config"
)

func TestNewStaticRegistry(t *testing.T) {
	routes := []config.Route{
		{
			ID:   "users",
			Path: "/api/users/*",
			Upstreams: []config.Upstream{
				{URL: "http://users-1:8080"},
				{URL: "http://users-2:8080"},
			},
		},
		{
			ID:   "health",
			Path: "/health",
			Upstreams: []config.Upstream{
				{URL: "http://health:8080"},
			},
		},
	}

	reg, err := NewStaticRegistry(routes)
	if err != nil {
		t.Fatalf("NewStaticRegistry() error: %v", err)
	}

	// Check known route.
	eps, err := reg.GetEndpoints("users")
	if err != nil {
		t.Fatalf("GetEndpoints('users') error: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(eps))
	}
	if eps[0].String() != "http://users-1:8080" {
		t.Errorf("expected http://users-1:8080, got %s", eps[0].String())
	}
	if eps[1].String() != "http://users-2:8080" {
		t.Errorf("expected http://users-2:8080, got %s", eps[1].String())
	}

	// Check second route.
	eps2, err := reg.GetEndpoints("health")
	if err != nil {
		t.Fatalf("GetEndpoints('health') error: %v", err)
	}
	if len(eps2) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps2))
	}
}

func TestNewStaticRegistry_InvalidURL(t *testing.T) {
	routes := []config.Route{
		{
			ID:   "bad",
			Path: "/bad",
			Upstreams: []config.Upstream{
				{URL: "://invalid-url"},
			},
		},
	}

	_, err := NewStaticRegistry(routes)
	if err == nil {
		t.Fatal("expected error for invalid upstream URL")
	}
}

func TestGetEndpoints_Unknown(t *testing.T) {
	reg, err := NewStaticRegistry(nil)
	if err != nil {
		t.Fatalf("NewStaticRegistry() error: %v", err)
	}

	_, err = reg.GetEndpoints("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown route")
	}
}

func TestNewStaticRegistry_NilRoutes(t *testing.T) {
	reg, err := NewStaticRegistry(nil)
	if err != nil {
		t.Fatalf("NewStaticRegistry(nil) error: %v", err)
	}

	_, err = reg.GetEndpoints("anything")
	if err == nil {
		t.Fatal("expected error for unknown route in empty registry")
	}
}
