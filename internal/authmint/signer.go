// Package authmint signs JWTs and serves a matching JWKS document.
//
// Used by:
//   - cmd/control-plane — long-running issuer of per-tenant access tokens
//   - cmd/issue-test-token — one-shot dev helper that exercises the auth
//     middleware without spinning up the full control plane
//
// Both binaries need to (a) produce signed JWTs and (b) expose the public
// half of the signing key as a JWKS so tenant backends can verify those
// tokens. The signer hides the cryptographic plumbing behind a tiny API.
//
// For now the signing key is generated fresh on every call to NewSigner —
// fine for dev, but production deployments will need a persistent key
// (loaded from disk/secret manager) and key rotation. Both come in 2C.2.
package authmint

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
)

// Signer holds an RSA signing key and the kid it advertises in JWKS docs
// and JWT headers. Use NewSigner to construct.
type Signer struct {
	priv *rsa.PrivateKey
	kid  string
}

// NewSigner generates a fresh 2048-bit RSA key and assigns a stable kid
// that the signer will use in both JWT headers and the JWKS document.
func NewSigner() (*Signer, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("rsa keygen: %w", err)
	}
	return &Signer{priv: priv, kid: "apex-cp-v1"}, nil
}

// KeyID returns the kid this signer uses in both JWT headers and the JWKS.
func (s *Signer) KeyID() string { return s.kid }

// Sign returns a signed RS256 JWT with the given claims. The kid header is
// set so verifiers can pick the right key from the JWKS.
func (s *Signer) Sign(claims jwt.MapClaims) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return signed, nil
}

// JWKS returns a JWKS document (RFC 7517) containing the signer's public
// key. Tenant backends point their APEX_JWKS_URL at an HTTP endpoint that
// serves these bytes.
func (s *Signer) JWKS() ([]byte, error) {
	pub := &s.priv.PublicKey
	doc := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": s.kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// JWKSHandler returns an http.HandlerFunc that serves the JWKS document.
// Convenience for control plane HTTP servers and dev helpers.
func (s *Signer) JWKSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		raw, err := s.JWKS()
		if err != nil {
			http.Error(w, "jwks", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
}
