package authmint

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

func TestNewSigner_GeneratesUsableKey(t *testing.T) {
	s, err := NewSigner()
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if s.KeyID() == "" {
		t.Errorf("KeyID is empty")
	}
}

func TestSigner_SignProducesValidJWT(t *testing.T) {
	s, err := NewSigner()
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	tok, err := s.Sign(jwt.MapClaims{
		"iss":   "https://auth.test",
		"aud":   "acme",
		"sub":   "user_1",
		"exp":   time.Now().Add(time.Minute).Unix(),
		"scope": "read",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" {
		t.Errorf("Sign returned empty token")
	}
}

func TestSigner_JWKSDocIsWellFormed(t *testing.T) {
	s, err := NewSigner()
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	raw, err := s.JWKS()
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}

	var doc struct {
		Keys []struct {
			Kty, Use, Alg, Kid, N, E string
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("JWKS doc not valid JSON: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("JWKS: %d keys, want 1", len(doc.Keys))
	}
	k := doc.Keys[0]
	if k.Kty != "RSA" || k.Alg != "RS256" || k.Use != "sig" {
		t.Errorf("JWKS key: kty=%q alg=%q use=%q, want RSA/RS256/sig", k.Kty, k.Alg, k.Use)
	}
	if k.Kid != s.KeyID() {
		t.Errorf("JWKS kid=%q, signer KeyID=%q (must match)", k.Kid, s.KeyID())
	}
	if k.N == "" || k.E == "" {
		t.Errorf("JWKS key missing modulus/exponent")
	}
}

func TestSigner_JWKSHandlerServesDoc(t *testing.T) {
	s, _ := NewSigner()
	srv := httptest.NewServer(s.JWKSHandler())
	defer srv.Close()

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}
}

// TestSigner_RoundTripThroughJWKS proves the signer is internally
// consistent: a signed token must verify using a Keyfunc backed by the
// same signer's published JWKS. This is the critical property — anything
// else can be broken without breaking auth, but if this fails, tenant
// backends will reject every token the control plane issues.
func TestSigner_RoundTripThroughJWKS(t *testing.T) {
	s, _ := NewSigner()
	srv := httptest.NewServer(s.JWKSHandler())
	defer srv.Close()

	tok, err := s.Sign(jwt.MapClaims{
		"iss":   "https://auth.test",
		"aud":   "acme",
		"sub":   "user_1",
		"exp":   time.Now().Add(time.Minute).Unix(),
		"scope": "read write",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	k, err := keyfunc.NewDefault([]string{srv.URL})
	if err != nil {
		t.Fatalf("keyfunc.NewDefault: %v", err)
	}

	parser := jwt.NewParser(
		jwt.WithIssuer("https://auth.test"),
		jwt.WithAudience("acme"),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	parsed, err := parser.Parse(tok, k.Keyfunc)
	if err != nil {
		t.Fatalf("parse via JWKS: %v", err)
	}
	if !parsed.Valid {
		t.Errorf("parsed token reports !Valid")
	}
}
