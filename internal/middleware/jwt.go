package middleware

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ahmadkhidir/gogateway/internal/config"
)

// JWTAuth validates JWT bearer tokens and forwards identity claims to
// upstream services as HTTP headers.
//
// Supported signing methods:
//   - HS256 (HMAC-SHA256) — shared secret
//   - RS256 (RSA-SHA256)  — public key, selected by kid header
type JWTAuth struct {
	// HMACSecret is the shared secret for HS256 tokens.
	HMACSecret []byte

	// RSAPublicKeys maps key ID (kid) to RSA public keys for RS256 tokens.
	RSAPublicKeys map[string]*rsa.PublicKey

	// KeyFunc can be set to override key selection entirely.
	// When nil, the default keyFunc based on HMACSecret and RSAPublicKeys is used.
	KeyFunc func(token *jwt.Token) (interface{}, error)
}

// NewJWTAuth creates a JWTAuth with the given HMAC secret for HS256 tokens.
// Pass nil for secret to disable HS256 support.
func NewJWTAuth(secret []byte) *JWTAuth {
	return &JWTAuth{
		HMACSecret:    secret,
		RSAPublicKeys: make(map[string]*rsa.PublicKey),
	}
}

// AddRSAKey registers an RSA public key for the given key ID (kid).
func (j *JWTAuth) AddRSAKey(kid string, key *rsa.PublicKey) {
	if j.RSAPublicKeys == nil {
		j.RSAPublicKeys = make(map[string]*rsa.PublicKey)
	}
	j.RSAPublicKeys[kid] = key
}

// Validate extracts and validates a JWT from the Authorization header.
// On success it returns the parsed claims. On failure it returns an error
// describing the reason.
func (j *JWTAuth) Validate(r *http.Request, jwtCfg *config.JWTConfig) (jwt.MapClaims, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}

	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, fmt.Errorf("invalid Authorization header format")
	}

	tokenString := strings.TrimPrefix(auth, "Bearer ")

	keyFunc := j.KeyFunc
	if keyFunc == nil {
		keyFunc = j.defaultKeyFunc
	}

	token, err := jwt.Parse(tokenString, keyFunc)
	if err != nil {
		return nil, fmt.Errorf("jwt: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Check issuer if configured.
	if jwtCfg != nil && len(jwtCfg.Issuers) > 0 {
		iss, _ := claims["iss"].(string)
		if !contains(jwtCfg.Issuers, iss) {
			return nil, fmt.Errorf("unexpected issuer: %q", iss)
		}
	}

	return claims, nil
}

// defaultKeyFunc selects the verification key based on the token's signing
// method and kid header.
func (j *JWTAuth) defaultKeyFunc(token *jwt.Token) (interface{}, error) {
	switch method := token.Method.(type) {
	case *jwt.SigningMethodHMAC:
		if j.HMACSecret == nil {
			return nil, fmt.Errorf("HMAC secret not configured")
		}
		return j.HMACSecret, nil

	case *jwt.SigningMethodRSA:
		if j.RSAPublicKeys == nil {
			return nil, fmt.Errorf("RSA public keys not configured")
		}
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("token missing kid header for RS256")
		}
		pubKey, ok := j.RSAPublicKeys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown kid: %q", kid)
		}
		return pubKey, nil

	default:
		return nil, fmt.Errorf("unexpected signing method: %v", method)
	}
}

// contains reports whether item is present in items.
func contains(items []string, item string) bool {
	for _, i := range items {
		if i == item {
			return true
		}
	}
	return false
}

// ForwardClaims sets identity headers on the request so upstream services
// receive authenticated user context without needing to re-validate the token.
//
// Headers forwarded:
//   - X-User-ID:  the "sub" claim
//   - X-User-Claims: JSON-encoded map of all claims (for custom claims)
func ForwardClaims(r *http.Request, claims jwt.MapClaims) {
	if sub, ok := claims["sub"].(string); ok && sub != "" {
		r.Header.Set("X-User-ID", sub)
	}

	// Forward all claims as JSON for upstream consumption.
	data, err := json.Marshal(claims)
	if err == nil {
		r.Header.Set("X-User-Claims", string(data))
	}
}
