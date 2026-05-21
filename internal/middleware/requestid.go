// Package middleware provides HTTP middleware handlers for GoGateway.
//
// Each middleware follows the standard http.Handler wrapper pattern:
//
//	func Middleware(next http.Handler) http.Handler
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestID injects or preserves an X-Request-Id header on every request.
// If the incoming request already carries an X-Request-Id it is forwarded
// as-is; otherwise a new 16-byte hex-encoded UUID is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = generateID()
			r.Header.Set("X-Request-Id", id)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

// generateID returns a 32-character hex string (16 random bytes).
func generateID() string {
	b := make([]byte, 16)
	// crypto/rand.Read is guaranteed to return len(b) bytes and nil error
	// on all modern Go versions and operating systems.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
