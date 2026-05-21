package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHashKey(t *testing.T) {
	hash := HashKey("test-key")
	if len(hash) != 64 { // SHA-256 hex is 64 chars
		t.Errorf("expected 64-char hash, got %d: %q", len(hash), hash)
	}

	// Deterministic.
	if HashKey("test-key") != HashKey("test-key") {
		t.Error("HashKey should be deterministic")
	}

	// Different keys produce different hashes.
	if HashKey("key-a") == HashKey("key-b") {
		t.Error("HashKey should produce different hashes for different keys")
	}
}

func TestKeyStore_Lookup(t *testing.T) {
	ks := NewKeyStore()

	// Empty store returns nil.
	if k := ks.Lookup("nonexistent"); k != nil {
		t.Error("expected nil for unknown hash")
	}

	key := &APIKey{
		ID:       "test-key",
		KeyHash:  HashKey("raw-key-value"),
		Service:  "users-api",
		RateTier: "pro",
		Revoked:  false,
	}
	ks.Add(key)

	found := ks.Lookup(key.KeyHash)
	if found == nil {
		t.Fatal("expected to find key after Add")
	}
	if found.ID != "test-key" {
		t.Errorf("expected ID test-key, got %q", found.ID)
	}
	if found.RateTier != "pro" {
		t.Errorf("expected rate_tier pro, got %q", found.RateTier)
	}
}

func TestKeyStore_Revoke(t *testing.T) {
	ks := NewKeyStore()
	hash := HashKey("revocable-key")

	ks.Add(&APIKey{
		ID:      "revocable",
		KeyHash: hash,
	})

	if !ks.Revoke(hash) {
		t.Fatal("Revoke should return true for existing key")
	}

	found := ks.Lookup(hash)
	if found == nil {
		t.Fatal("expected key to exist after Revoke")
	}
	if !found.Revoked {
		t.Error("expected key to be marked revoked")
	}

	// Revoking nonexistent key returns false.
	if ks.Revoke("nonexistent") {
		t.Error("expected Revoke to return false for unknown hash")
	}
}

func TestLoadKeyFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api-keys.yaml")
	content := `
api_keys:
  - id: "key_k7d92h"
    key: "gw_live_abc123def456"
    service: ""
    rate_tier: "pro"
  - id: "key_b3x81m"
    key: "gw_test_xyz789"
    service: "users-api"
    rate_tier: "basic"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	ks, err := LoadKeyFile(path)
	if err != nil {
		t.Fatalf("LoadKeyFile error: %v", err)
	}

	// Check first key.
	hash1 := HashKey("gw_live_abc123def456")
	k1 := ks.Lookup(hash1)
	if k1 == nil {
		t.Fatal("expected to find first key")
	}
	if k1.ID != "key_k7d92h" {
		t.Errorf("expected ID key_k7d92h, got %q", k1.ID)
	}
	if k1.RateTier != "pro" {
		t.Errorf("expected rate_tier pro, got %q", k1.RateTier)
	}

	// Check second key.
	hash2 := HashKey("gw_test_xyz789")
	k2 := ks.Lookup(hash2)
	if k2 == nil {
		t.Fatal("expected to find second key")
	}
	if k2.Service != "users-api" {
		t.Errorf("expected service users-api, got %q", k2.Service)
	}

	// Check that raw key is not stored anywhere.
	if k1.KeyHash == "gw_live_abc123def456" {
		t.Error("KeyHash should not contain raw key")
	}
}

func TestLoadKeyFile_NotFound(t *testing.T) {
	ks, err := LoadKeyFile("/nonexistent/path/keys.yaml")
	if err != nil {
		t.Fatalf("LoadKeyFile on missing file should not error: %v", err)
	}
	if ks == nil {
		t.Fatal("expected non-nil store for missing file")
	}
}

func TestLoadKeyFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("{{invalid"), 0644)

	_, err := LoadKeyFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestKeyStore_ConcurrentSafe(t *testing.T) {
	ks := NewKeyStore()
	done := make(chan bool)

	// Concurrent writes.
	go func() {
		for i := 0; i < 100; i++ {
			ks.Add(&APIKey{
				ID:      fmt.Sprintf("key-%d", i),
				KeyHash: HashKey(fmt.Sprintf("raw-%d", i)),
			})
		}
		done <- true
	}()

	// Concurrent reads.
	go func() {
		for i := 0; i < 100; i++ {
			ks.Lookup(HashKey(fmt.Sprintf("raw-%d", i)))
		}
		done <- true
	}()

	<-done
	<-done
	// If we get here without race, test passes.
}
