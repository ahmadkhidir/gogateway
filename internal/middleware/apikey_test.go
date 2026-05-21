package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/ahmadkhidir/gogateway/internal/store"
)

func newTestKeyStore(t *testing.T) *store.KeyStore {
	t.Helper()
	ks := store.NewKeyStore()
	ks.Add(&store.APIKey{
		ID:       "key_k7d92h",
		KeyHash:  store.HashKey("gw_live_abc123def456"),
		Service:  "",
		RateTier: "pro",
	})
	ks.Add(&store.APIKey{
		ID:       "key_b3x81m",
		KeyHash:  store.HashKey("gw_test_xyz789"),
		Service:  "users-api",
		RateTier: "basic",
	})
	return ks
}

func TestAPIKeyAuth_Valid_XAPIKeyHeader(t *testing.T) {
	auth := NewAPIKeyAuth(newTestKeyStore(t))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "gw_live_abc123def456")

	key, err := auth.Validate(req)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if key.ID != "key_k7d92h" {
		t.Errorf("expected ID key_k7d92h, got %q", key.ID)
	}
	if key.RateTier != "pro" {
		t.Errorf("expected rate_tier pro, got %q", key.RateTier)
	}
}

func TestAPIKeyAuth_Valid_BearerHeader(t *testing.T) {
	auth := NewAPIKeyAuth(newTestKeyStore(t))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer gw_test_xyz789")

	key, err := auth.Validate(req)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if key.ID != "key_b3x81m" {
		t.Errorf("expected ID key_b3x81m, got %q", key.ID)
	}
}

func TestAPIKeyAuth_Missing(t *testing.T) {
	auth := NewAPIKeyAuth(newTestKeyStore(t))

	req := httptest.NewRequest("GET", "/", nil)
	// No API key header.

	_, err := auth.Validate(req)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestAPIKeyAuth_Unknown(t *testing.T) {
	auth := NewAPIKeyAuth(newTestKeyStore(t))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "unknown_key_value")

	_, err := auth.Validate(req)
	if err == nil {
		t.Fatal("expected error for unknown API key")
	}
}

func TestAPIKeyAuth_Revoked(t *testing.T) {
	ks := newTestKeyStore(t)
	hash := store.HashKey("gw_live_abc123def456")
	ks.Revoke(hash)

	auth := NewAPIKeyAuth(ks)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "gw_live_abc123def456")

	_, err := auth.Validate(req)
	if err == nil {
		t.Fatal("expected error for revoked key")
	}
}

func TestAPIKeyAuth_XAPIKeyOverridesBearer(t *testing.T) {
	// X-API-Key header takes precedence over Authorization header.
	auth := NewAPIKeyAuth(newTestKeyStore(t))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "gw_live_abc123def456")
	req.Header.Set("Authorization", "Bearer gw_test_xyz789")

	key, err := auth.Validate(req)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if key.ID != "key_k7d92h" {
		t.Errorf("expected key_k7d92h (from X-API-Key), got %q", key.ID)
	}
}
