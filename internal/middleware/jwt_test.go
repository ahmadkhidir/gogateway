package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ahmadkhidir/gogateway/internal/config"
)

// hs256Token creates a signed HS256 JWT token string for testing.
func hs256Token(secret []byte, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString(secret)
	return signed
}

// rs256Token creates a signed RS256 JWT token string for testing.
func rs256Token(key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, _ := token.SignedString(key)
	return signed
}

func TestJWTAuth_HS256_Valid(t *testing.T) {
	secret := []byte("test-secret")
	auth := NewJWTAuth(secret)

	token := hs256Token(secret, jwt.MapClaims{
		"sub": "user-123",
		"iss": "https://auth.example.com",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	claims, err := auth.Validate(req, &config.JWTConfig{
		Required: true,
		Issuers:  []string{"https://auth.example.com"},
	})
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if claims["sub"] != "user-123" {
		t.Errorf("expected sub user-123, got %v", claims["sub"])
	}
}

func TestJWTAuth_HS256_WrongSecret(t *testing.T) {
	auth := NewJWTAuth([]byte("correct-secret"))
	token := hs256Token([]byte("wrong-secret"), jwt.MapClaims{
		"sub": "user-123",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err := auth.Validate(req, nil)
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestJWTAuth_HS256_Expired(t *testing.T) {
	secret := []byte("test-secret")
	auth := NewJWTAuth(secret)

	token := hs256Token(secret, jwt.MapClaims{
		"sub": "user-123",
		"exp": float64(time.Now().Add(-1 * time.Hour).Unix()), // expired
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err := auth.Validate(req, nil)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWTAuth_HS256_MissingAuthHeader(t *testing.T) {
	auth := NewJWTAuth([]byte("secret"))

	req := httptest.NewRequest("GET", "/", nil)
	// No Authorization header.

	_, err := auth.Validate(req, nil)
	if err == nil {
		t.Fatal("expected error for missing Authorization header")
	}
}

func TestJWTAuth_HS256_InvalidBearerFormat(t *testing.T) {
	auth := NewJWTAuth([]byte("secret"))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz") // not Bearer

	_, err := auth.Validate(req, nil)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestJWTAuth_RS256_Valid(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey error: %v", err)
	}

	auth := NewJWTAuth(nil)
	auth.AddRSAKey("my-key", &privateKey.PublicKey)

	token := rs256Token(privateKey, "my-key", jwt.MapClaims{
		"sub": "user-rsa",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	claims, err := auth.Validate(req, nil)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if claims["sub"] != "user-rsa" {
		t.Errorf("expected sub user-rsa, got %v", claims["sub"])
	}
}

func TestJWTAuth_RS256_UnknownKid(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey error: %v", err)
	}

	auth := NewJWTAuth(nil)
	auth.AddRSAKey("key-1", &privateKey.PublicKey)

	token := rs256Token(privateKey, "unknown-kid", jwt.MapClaims{
		"sub": "user-rsa",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err = auth.Validate(req, nil)
	if err == nil {
		t.Fatal("expected error for unknown kid")
	}
}

func TestJWTAuth_RS256_MissingKid(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey error: %v", err)
	}

	auth := NewJWTAuth(nil)
	auth.AddRSAKey("key-1", &privateKey.PublicKey)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "user-rsa",
	})
	// No kid header.
	signed, _ := token.SignedString(privateKey)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	_, err = auth.Validate(req, nil)
	if err == nil {
		t.Fatal("expected error for missing kid")
	}
}

func TestJWTAuth_IssuerValidation(t *testing.T) {
	secret := []byte("secret")
	auth := NewJWTAuth(secret)

	token := hs256Token(secret, jwt.MapClaims{
		"sub": "user-123",
		"iss": "https://evil.com",
		"exp": float64(time.Now().Add(1 * time.Hour).Unix()),
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err := auth.Validate(req, &config.JWTConfig{
		Required: true,
		Issuers:  []string{"https://trusted.example.com"},
	})
	if err == nil {
		t.Fatal("expected error for untrusted issuer")
	}
}

func TestForwardClaims(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)

	claims := jwt.MapClaims{
		"sub":   "user-456",
		"email": "user@example.com",
		"roles": []any{"admin", "editor"},
	}

	ForwardClaims(req, claims)

	if req.Header.Get("X-User-ID") != "user-456" {
		t.Errorf("expected X-User-ID user-456, got %q", req.Header.Get("X-User-ID"))
	}

	claimsHeader := req.Header.Get("X-User-Claims")
	if claimsHeader == "" {
		t.Fatal("expected X-User-Claims header")
	}

	// Verify it's valid JSON containing expected fields.
	if !strings.Contains(claimsHeader, `"sub":"user-456"`) {
		t.Errorf("expected sub in claims JSON, got %q", claimsHeader)
	}
}

func TestForwardClaims_NoSub(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	claims := jwt.MapClaims{"iss": "test"}

	ForwardClaims(req, claims)

	if req.Header.Get("X-User-ID") != "" {
		t.Errorf("expected no X-User-ID for missing sub, got %q", req.Header.Get("X-User-ID"))
	}
}
