// Package store provides the API key store for GoGateway.
//
// API keys are stored by their SHA-256 hash and can be looked up during
// request authentication. The store supports an in-memory implementation
// for development (seeded from a YAML file) and is designed to be extended
// with a Redis-backed implementation.
package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// APIKey represents a stored API key with its associated metadata.
type APIKey struct {
	ID        string    `yaml:"id"`
	KeyHash   string    // SHA-256 hex of the raw key (computed, not from file)
	Service   string    `yaml:"service"`   // "" means all services/routes
	RateTier  string    `yaml:"rate_tier"` // "basic", "pro", "unlimited"
	CreatedAt time.Time `yaml:"created_at"`
	Revoked   bool      `yaml:"revoked"`
}

// KeyStore provides concurrent-safe API key lookup and management.
type KeyStore struct {
	mu   sync.RWMutex
	keys map[string]*APIKey // keyed by SHA-256 hex hash
}

// NewKeyStore creates an empty key store.
func NewKeyStore() *KeyStore {
	return &KeyStore{
		keys: make(map[string]*APIKey),
	}
}

// Lookup returns the API key metadata for the given SHA-256 hex hash,
// or nil if the hash is unknown.
func (ks *KeyStore) Lookup(hash string) *APIKey {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.keys[hash]
}

// Add inserts or updates an API key in the store.
func (ks *KeyStore) Add(key *APIKey) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.keys[key.KeyHash] = key
}

// Revoke marks a key as revoked by its hash. Returns false if the hash
// is unknown.
func (ks *KeyStore) Revoke(hash string) bool {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	k, ok := ks.keys[hash]
	if !ok {
		return false
	}
	k.Revoked = true
	return true
}

// HashKey returns the SHA-256 hex digest of a raw key string.
func HashKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", h)
}

// keyFile represents the YAML structure of an API key seed file.
type keyFile struct {
	APIKeys []keyFileEntry `yaml:"api_keys"`
}

type keyFileEntry struct {
	ID       string `yaml:"id"`
	Key      string `yaml:"key"`
	Service  string `yaml:"service"`
	RateTier string `yaml:"rate_tier"`
}

// LoadKeyFile reads a YAML file of API keys and seeds them into a KeyStore.
// Each key is hashed with SHA-256 at load time; the raw key is never stored.
// If the file does not exist it returns an empty store without error.
func LoadKeyFile(path string) (*KeyStore, error) {
	ks := NewKeyStore()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ks, nil // file is optional
		}
		return nil, fmt.Errorf("store: read key file %q: %w", path, err)
	}

	var kf keyFile
	if err := yaml.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("store: parse key file %q: %w", path, err)
	}

	now := time.Now().UTC()
	for _, entry := range kf.APIKeys {
		if entry.ID == "" || entry.Key == "" {
			continue
		}
		hash := HashKey(entry.Key)
		ks.Add(&APIKey{
			ID:        entry.ID,
			KeyHash:   hash,
			Service:   entry.Service,
			RateTier:  entry.RateTier,
			CreatedAt: now,
			Revoked:   false,
		})
	}

	return ks, nil
}
