package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/everydev1618/govega/internal/authmint"
	"github.com/golang-jwt/jwt/v5"
)

func newTestServer(t *testing.T, devSecret string) (*httptest.Server, *authmint.Signer) {
	t.Helper()
	s, err := authmint.NewSigner()
	if err != nil {
		t.Fatalf("authmint: %v", err)
	}
	srv := httptest.NewServer(newHandler(Config{
		Signer:    s,
		Issuer:    "http://control-plane.test",
		DevSecret: devSecret,
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func TestHealthz_Returns200(t *testing.T) {
	srv, _ := newTestServer(t, "shh")
	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", res.StatusCode)
	}
}

func TestJWKS_ServesValidDoc(t *testing.T) {
	srv, signer := newTestServer(t, "shh")
	res, err := http.Get(srv.URL + "/jwks")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	var doc struct{ Keys []struct{ Kid string } }
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Keys) != 1 || doc.Keys[0].Kid != signer.KeyID() {
		t.Errorf("JWKS doc: %s", string(body))
	}
}

func TestDevMint_NoSecretConfigured_503(t *testing.T) {
	// When APEX_DEV_SECRET isn't set, /dev/mint must be unavailable. Prevents
	// accidentally shipping a token-minting endpoint open to anyone in prod.
	srv, _ := newTestServer(t, "")
	res, err := http.Post(srv.URL+"/dev/mint", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503 (dev mode disabled)", res.StatusCode)
	}
}

func TestDevMint_MissingSecret_401(t *testing.T) {
	srv, _ := newTestServer(t, "shh")
	res, err := http.Post(srv.URL+"/dev/mint", "application/json", strings.NewReader(`{"tenant":"acme","user":"u1"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", res.StatusCode)
	}
}

func TestDevMint_WrongSecret_401(t *testing.T) {
	srv, _ := newTestServer(t, "correct-secret")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/dev/mint", strings.NewReader(`{"tenant":"acme","user":"u1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dev-Secret", "wrong-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", res.StatusCode)
	}
}

func TestDevMint_BadJSON_400(t *testing.T) {
	srv, _ := newTestServer(t, "shh")
	req := mintRequest(t, srv.URL, "shh", []byte("{not json"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}

func TestDevMint_MissingTenant_400(t *testing.T) {
	srv, _ := newTestServer(t, "shh")
	req := mintRequest(t, srv.URL, "shh", []byte(`{"user":"u1"}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}

func TestDevMint_Success_TokenValidatesAgainstJWKS(t *testing.T) {
	// End-to-end: mint a token via /dev/mint, then verify it against the
	// same control plane's /jwks endpoint. This is the contract the auth
	// middleware will rely on in production.
	srv, _ := newTestServer(t, "shh")

	body, _ := json.Marshal(map[string]any{
		"tenant":      "acme",
		"user":        "user_42",
		"ttl_seconds": 60,
		"scope":       "read write admin",
	})
	req := mintRequest(t, srv.URL, "shh", body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d, body=%s", res.StatusCode, string(respBody))
	}

	var got struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AccessToken == "" {
		t.Fatalf("empty access_token")
	}
	if got.ExpiresIn != 60 {
		t.Errorf("expires_in=%d, want 60", got.ExpiresIn)
	}

	// Verify the token resolves against /jwks.
	k, err := keyfunc.NewDefault([]string{srv.URL + "/jwks"})
	if err != nil {
		t.Fatalf("keyfunc: %v", err)
	}
	parser := jwt.NewParser(
		jwt.WithIssuer("http://control-plane.test"),
		jwt.WithAudience("acme"),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	parsed, err := parser.Parse(got.AccessToken, k.Keyfunc)
	if err != nil {
		t.Fatalf("parse via JWKS: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != "user_42" {
		t.Errorf("sub=%v, want user_42", claims["sub"])
	}
	if claims["scope"] != "read write admin" {
		t.Errorf("scope=%v, want \"read write admin\"", claims["scope"])
	}
	// exp should be close to now + 60s
	exp := int64(claims["exp"].(float64))
	if delta := exp - time.Now().Unix(); delta < 50 || delta > 70 {
		t.Errorf("exp delta=%ds, want ~60", delta)
	}
}

func TestDevMint_DefaultsTTL(t *testing.T) {
	// Omitting ttl_seconds should yield a sensible default (15 min, matching
	// the locked Decision 2 access-token TTL).
	srv, _ := newTestServer(t, "shh")
	body, _ := json.Marshal(map[string]any{"tenant": "acme", "user": "u1"})
	req := mintRequest(t, srv.URL, "shh", body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got struct{ ExpiresIn int `json:"expires_in"` }
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got.ExpiresIn != 15*60 {
		t.Errorf("default expires_in=%d, want %d (15min)", got.ExpiresIn, 15*60)
	}
}

func mintRequest(t *testing.T, baseURL, secret string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/dev/mint", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dev-Secret", secret)
	return req
}
