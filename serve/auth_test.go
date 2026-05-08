package serve

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testAuthFixture holds an RSA key + a Keyfunc usable with the auth middleware.
// We avoid spinning up a JWKS HTTP server in unit tests by injecting the Keyfunc
// directly — production code paths (server.go) do go through MicahParks/keyfunc
// for live JWKS fetching, but here we want fast, deterministic tests.
type testAuthFixture struct {
	priv *rsa.PrivateKey
	cfg  AuthConfig
}

func newTestAuth(t *testing.T, tenantID string) *testAuthFixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return &testAuthFixture{
		priv: priv,
		cfg: AuthConfig{
			Issuer:   "https://auth.test",
			TenantID: tenantID,
			Keyfunc:  func(_ *jwt.Token) (any, error) { return &priv.PublicKey, nil },
		},
	}
}

// mintToken signs a JWT with the fixture's private key. Callers can override
// any claim to exercise failure paths (wrong iss, wrong aud, expired, etc.).
func (f *testAuthFixture) mintToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(f.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

// validClaims returns a baseline claim set that should pass validation. Tests
// override single fields to verify rejection paths.
func validClaims(tenantID string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   "https://auth.test",
		"aud":   tenantID,
		"sub":   "user_123",
		"exp":   time.Now().Add(15 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"scope": "read write",
	}
}

// downstream is a tiny handler used as the "next" in tests. It signals it was
// reached by writing 200 + the user id from claims context (or "anon" if no
// claims were injected).
func downstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFrom(r.Context())
		if !ok {
			_, _ = w.Write([]byte("anon"))
			return
		}
		_, _ = w.Write([]byte(claims.UserID))
	}
}

func TestAuthMiddleware_SelfHostedMode_PassThrough(t *testing.T) {
	// When TenantID is empty, middleware is a no-op. Every path passes through
	// without auth — preserves the existing self-hosted experience.
	mw := authMiddleware(AuthConfig{}) // zero-valued cfg
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	for _, path := range []string{"/api/v1/stats", "/", "/workspace/x", "/anything"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("path %s: %v", path, err)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("path %s: status %d, want 200 (self-hosted pass-through)", path, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestAuthMiddleware_NonAPIPath_PassThrough(t *testing.T) {
	// Cloud mode, but non-/api/v1/* paths still pass through (SPA, workspace,
	// healthz, gmail handoff). No bearer token required.
	f := newTestAuth(t, "acme")
	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	for _, path := range []string{"/", "/workspace/site/index.html", "/integrations/gmail/handoff", "/healthz"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("path %s: %v", path, err)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("path %s: status %d, want 200 (non-API pass-through)", path, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestAuthMiddleware_ValidToken_InjectsClaims(t *testing.T) {
	f := newTestAuth(t, "acme")
	tok := f.mintToken(t, validClaims("acme"))

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	body := readAll(t, res)
	if body != "user_123" {
		t.Errorf("downstream saw %q, want \"user_123\" (claims should be in context)", body)
	}
}

func TestAuthMiddleware_MissingHeader_401(t *testing.T) {
	f := newTestAuth(t, "acme")
	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", res.StatusCode)
	}
}

func TestAuthMiddleware_WrongScheme_401(t *testing.T) {
	f := newTestAuth(t, "acme")
	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz") // wrong auth scheme
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", res.StatusCode)
	}
}

func TestAuthMiddleware_BadSignature_401(t *testing.T) {
	f := newTestAuth(t, "acme")
	// Sign with a different private key — verifier should reject.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims("acme"))
	signed, _ := tok.SignedString(other)

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", res.StatusCode)
	}
}

func TestAuthMiddleware_Expired_401(t *testing.T) {
	f := newTestAuth(t, "acme")
	claims := validClaims("acme")
	claims["exp"] = time.Now().Add(-1 * time.Minute).Unix() // expired
	tok := f.mintToken(t, claims)

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", res.StatusCode)
	}
}

func TestAuthMiddleware_WrongIssuer_401(t *testing.T) {
	f := newTestAuth(t, "acme")
	claims := validClaims("acme")
	claims["iss"] = "https://attacker.example"
	tok := f.mintToken(t, claims)

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", res.StatusCode)
	}
}

func TestAuthMiddleware_WrongTenant_403(t *testing.T) {
	// Token signed for a different tenant. Should be 403, not 401, so logs
	// distinguish "wrong tenant" from "no/bad auth".
	f := newTestAuth(t, "acme")
	claims := validClaims("globex") // valid token, but for the wrong tenant
	tok := f.mintToken(t, claims)

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("status %d, want 403 (wrong tenant should be distinguishable from 401)", res.StatusCode)
	}
}

func TestAuthMiddleware_NoneAlg_Rejected(t *testing.T) {
	// "alg": "none" is the classic JWT bypass. Parser must require RS256.
	f := newTestAuth(t, "acme")
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims("acme"))
	signed, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401 (none-alg bypass must be rejected)", res.StatusCode)
	}
}

func TestAuthMiddleware_QueryParamFallback_ForSSE(t *testing.T) {
	// EventSource can't send custom headers, so the auth middleware must
	// also accept the token via ?access_token=... (RFC 6750 §2.3). Used
	// only for SSE endpoints in practice; benign for everything else.
	f := newTestAuth(t, "acme")
	tok := f.mintToken(t, validClaims("acme"))

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events?access_token="+tok, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200 (query-param token must work)", res.StatusCode)
	}
	if got := readAll(t, res); got != "user_123" {
		t.Errorf("downstream saw %q, want \"user_123\"", got)
	}
}

func TestAuthMiddleware_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	// If both header and query-param tokens are present, the header wins.
	// Prevents a leaked-URL attack from upgrading itself to a header-class
	// request by also setting the header to a forged value.
	f := newTestAuth(t, "acme")
	headerTok := f.mintToken(t, validClaims("acme"))
	// Query-param token signed with a different key — should be ignored.
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims("acme"))
	queryTok, _ := bad.SignedString(otherKey)

	mw := authMiddleware(f.cfg)
	srv := httptest.NewServer(mw(downstream()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/stats?access_token="+queryTok, nil)
	req.Header.Set("Authorization", "Bearer "+headerTok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status %d, want 200 (header-token must take precedence over bad query token)", res.StatusCode)
	}
}

func TestAuthConfig_Verify_ValidHandoffToken(t *testing.T) {
	// Verify is the seam by which non-/api/v1 handlers (e.g. Gmail handoff)
	// validate JWTs from the control plane. Same Keyfunc + iss + aud as
	// the middleware; adds an optional purpose-claim check.
	f := newTestAuth(t, "acme")
	claims := jwt.MapClaims{
		"iss":     "https://auth.test",
		"aud":     "acme",
		"sub":     "user_123",
		"exp":     time.Now().Add(time.Minute).Unix(),
		"purpose": "gmail-handoff",
		"jti":     "abc123",
	}
	tok := f.mintToken(t, claims)

	got, err := f.cfg.Verify(tok, "gmail-handoff")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got["sub"] != "user_123" {
		t.Errorf("sub=%v, want user_123", got["sub"])
	}
	if got["jti"] != "abc123" {
		t.Errorf("jti=%v, want abc123", got["jti"])
	}
}

func TestAuthConfig_Verify_WrongPurpose_Rejected(t *testing.T) {
	f := newTestAuth(t, "acme")
	claims := validClaims("acme")
	claims["purpose"] = "user-access" // not "gmail-handoff"
	tok := f.mintToken(t, claims)

	_, err := f.cfg.Verify(tok, "gmail-handoff")
	if err == nil {
		t.Errorf("Verify accepted token with wrong purpose")
	}
}

func TestAuthConfig_Verify_MissingPurpose_RejectedWhenRequired(t *testing.T) {
	// When requirePurpose != "", a missing purpose claim is a rejection —
	// prevents an attacker from substituting a regular access token.
	f := newTestAuth(t, "acme")
	tok := f.mintToken(t, validClaims("acme")) // no purpose claim

	_, err := f.cfg.Verify(tok, "gmail-handoff")
	if err == nil {
		t.Errorf("Verify accepted token with missing purpose")
	}
}

func TestAuthConfig_Verify_EmptyPurposeArg_AcceptsAnyPurpose(t *testing.T) {
	// requirePurpose="" disables the check (e.g. for plain access-token
	// validation outside the middleware).
	f := newTestAuth(t, "acme")
	tok := f.mintToken(t, validClaims("acme"))

	if _, err := f.cfg.Verify(tok, ""); err != nil {
		t.Errorf("Verify(_, \"\") rejected a valid access token: %v", err)
	}
}

func TestAuthConfig_Verify_WrongAudience_Rejected(t *testing.T) {
	f := newTestAuth(t, "acme")
	tok := f.mintToken(t, validClaims("globex")) // different tenant

	if _, err := f.cfg.Verify(tok, ""); err == nil {
		t.Errorf("Verify accepted token with wrong audience")
	}
}

func TestAuthConfig_Verify_Expired_Rejected(t *testing.T) {
	f := newTestAuth(t, "acme")
	claims := validClaims("acme")
	claims["exp"] = time.Now().Add(-time.Minute).Unix()
	tok := f.mintToken(t, claims)

	if _, err := f.cfg.Verify(tok, ""); err == nil {
		t.Errorf("Verify accepted expired token")
	}
}

func TestAuthConfig_Verify_NoAuthDisabled(t *testing.T) {
	// Self-hosted mode (no TenantID) — Verify always returns an error so
	// callers can't accidentally treat unauth contexts as authenticated.
	cfg := AuthConfig{}
	if _, err := cfg.Verify("any.token.here", ""); err == nil {
		t.Errorf("Verify on zero-value AuthConfig returned nil error")
	}
}

func TestClaimsFrom_AbsentReturnsFalse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	if _, ok := ClaimsFrom(req.Context()); ok {
		t.Errorf("ClaimsFrom returned ok=true on a context with no claims")
	}
}

// readAll is a tiny helper to read response bodies in tests.
func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	buf := make([]byte, 1024)
	n, _ := res.Body.Read(buf)
	return string(buf[:n])
}
