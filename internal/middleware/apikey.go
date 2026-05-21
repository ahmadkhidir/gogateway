package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ahmadkhidir/gogateway/internal/store"
)

// APIKeyAuth validates API keys from the X-API-Key header or
// Authorization: Bearer <key> header.
type APIKeyAuth struct {
	keyStore *store.KeyStore
}

// NewAPIKeyAuth creates an API key validator backed by the given store.
func NewAPIKeyAuth(keyStore *store.KeyStore) *APIKeyAuth {
	return &APIKeyAuth{
		keyStore: keyStore,
	}
}

// Validate extracts an API key from the request, looks it up in the store,
// and returns the key metadata on success.
//
// It checks these sources in order:
//  1. X-API-Key header
//  2. Authorization: Bearer <key> header
func (a *APIKeyAuth) Validate(r *http.Request) (*store.APIKey, error) {
	rawKey := r.Header.Get("X-API-Key")
	if rawKey == "" {
		// Fall back to Authorization: Bearer <key>
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			rawKey = strings.TrimPrefix(auth, "Bearer ")
		}
	}

	if rawKey == "" {
		return nil, fmt.Errorf("missing API key")
	}

	hash := store.HashKey(rawKey)
	key := a.keyStore.Lookup(hash)
	if key == nil {
		return nil, fmt.Errorf("unknown API key")
	}

	if key.Revoked {
		return nil, fmt.Errorf("API key is revoked")
	}

	return key, nil
}
