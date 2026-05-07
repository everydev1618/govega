package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newOAuthStore builds a fresh on-disk SQLite store for tests. Uses a real
// file (not :memory:) so the schema migration runs the same as in prod.
func newOAuthStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// fakeTokenServer returns an httptest.Server that mocks Google's OAuth token
// endpoint. It records the form posted to it so tests can assert grant type,
// code, and credentials, and returns the supplied response.
type fakeTokenServer struct {
	*httptest.Server
	receivedForm url.Values
	respStatus   int
	respBody     map[string]any
}

func newFakeTokenServer(t *testing.T) *fakeTokenServer {
	t.Helper()
	f := &fakeTokenServer{
		respStatus: http.StatusOK,
		respBody: map[string]any{
			"access_token":  "fake-access",
			"refresh_token": "fake-refresh",
			"expires_in":    3600,
			"token_type":    "Bearer",
		},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		form, _ := url.ParseQuery(string(body))
		f.receivedForm = form
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.respStatus)
		_ = json.NewEncoder(w).Encode(f.respBody)
	}))
	t.Cleanup(f.Close)
	return f
}

// withGmailEndpoints points the OAuth helpers at the supplied fake servers
// and restores the originals on test cleanup.
func withGmailEndpoints(t *testing.T, tokenURL string) {
	t.Helper()
	origTokenURL := gmailTokenURL
	gmailTokenURL = tokenURL
	t.Cleanup(func() { gmailTokenURL = origTokenURL })
}

func newOAuthTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		store:           newOAuthStore(t),
		oauthStateCache: newOAuthStateCache(),
	}
}

// TestGmailOAuth_StartRedirectsToGoogle verifies that `/auth/gmail/start`
// returns a 302 to Google's authorize URL with our scope, redirect_uri, and
// a server-issued state token. Without state validation on the callback the
// flow is open to CSRF, so the start path MUST mint and remember a state
// for later callback comparison.
func TestGmailOAuth_StartRedirectsToGoogle(t *testing.T) {
	t.Setenv("GMAIL_CLIENT_ID", "test-client-id")
	t.Setenv("GMAIL_CLIENT_SECRET", "test-client-secret")

	s := newOAuthTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "https://example.invalid/auth/gmail/start", nil)
	rec := httptest.NewRecorder()

	s.handleGmailAuthStart(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("Location header missing")
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	q := u.Query()
	if got := q.Get("client_id"); got != "test-client-id" {
		t.Errorf("client_id = %q, want %q", got, "test-client-id")
	}
	if got := q.Get("scope"); !strings.Contains(got, "gmail.modify") {
		t.Errorf("scope = %q, want it to include gmail.modify", got)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q, want code", got)
	}
	if got := q.Get("access_type"); got != "offline" {
		t.Errorf("access_type = %q, want offline (required to get a refresh_token)", got)
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent (forces a refresh_token even on re-auth)", got)
	}
	state := q.Get("state")
	if state == "" {
		t.Error("state token missing from authorize URL")
	}
	if !s.oauthStateCache.exists(state) {
		t.Error("state was not recorded in the server's state cache")
	}
	if !strings.Contains(q.Get("redirect_uri"), "/auth/gmail/callback") {
		t.Errorf("redirect_uri = %q, want it to end with /auth/gmail/callback", q.Get("redirect_uri"))
	}
}

// TestGmailOAuth_StartMissingCredsErrors checks that a clear error response
// is returned (rather than a redirect into Google land that ultimately fails)
// when the operator has not yet set CLIENT_ID/CLIENT_SECRET.
func TestGmailOAuth_StartMissingCredsErrors(t *testing.T) {
	t.Setenv("GMAIL_CLIENT_ID", "")
	t.Setenv("GMAIL_CLIENT_SECRET", "")

	s := newOAuthTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "https://example.invalid/auth/gmail/start", nil)
	rec := httptest.NewRecorder()

	s.handleGmailAuthStart(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "GMAIL_CLIENT_ID") {
		t.Errorf("error body should mention which env vars are missing; got %q", rec.Body.String())
	}
}

// TestGmailOAuth_CallbackHappyPath drives the full code-to-refresh-token
// exchange against a fake Google token endpoint and asserts the resulting
// refresh token is persisted in settings AND exported to GMAIL_REFRESH_TOKEN.
func TestGmailOAuth_CallbackHappyPath(t *testing.T) {
	t.Setenv("GMAIL_CLIENT_ID", "test-client-id")
	t.Setenv("GMAIL_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GMAIL_REFRESH_TOKEN", "")

	fts := newFakeTokenServer(t)
	withGmailEndpoints(t, fts.URL)

	s := newOAuthTestServer(t)
	state := s.oauthStateCache.issue()

	req := httptest.NewRequest(http.MethodGet,
		"https://example.invalid/auth/gmail/callback?code=auth-code-xyz&state="+state, nil)
	rec := httptest.NewRecorder()

	s.handleGmailAuthCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	if got := fts.receivedForm.Get("grant_type"); got != "authorization_code" {
		t.Errorf("token request grant_type = %q, want authorization_code", got)
	}
	if got := fts.receivedForm.Get("code"); got != "auth-code-xyz" {
		t.Errorf("token request code = %q, want auth-code-xyz", got)
	}
	if got := fts.receivedForm.Get("client_id"); got != "test-client-id" {
		t.Errorf("token request client_id = %q, want test-client-id", got)
	}

	stored, err := s.store.GetSetting(gmailRefreshTokenSettingKey)
	if err != nil || stored == nil {
		t.Fatalf("refresh token not persisted in settings; err=%v stored=%v", err, stored)
	}
	if stored.Value != "fake-refresh" {
		t.Errorf("stored refresh token = %q, want fake-refresh", stored.Value)
	}
	if got := os.Getenv("GMAIL_REFRESH_TOKEN"); got != "fake-refresh" {
		t.Errorf("GMAIL_REFRESH_TOKEN env = %q, want fake-refresh", got)
	}

	// State should be consumed exactly once.
	if s.oauthStateCache.exists(state) {
		t.Error("state was not consumed after callback; replay would be possible")
	}
}

// TestGmailOAuth_CallbackBadStateRejected guards against a forged callback
// from an attacker who tricks the user into visiting a crafted callback URL.
func TestGmailOAuth_CallbackBadStateRejected(t *testing.T) {
	t.Setenv("GMAIL_CLIENT_ID", "test-client-id")
	t.Setenv("GMAIL_CLIENT_SECRET", "test-client-secret")

	fts := newFakeTokenServer(t)
	withGmailEndpoints(t, fts.URL)

	s := newOAuthTestServer(t)

	req := httptest.NewRequest(http.MethodGet,
		"https://example.invalid/auth/gmail/callback?code=x&state=never-issued", nil)
	rec := httptest.NewRecorder()

	s.handleGmailAuthCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if fts.receivedForm != nil {
		t.Error("token endpoint should not have been called for an invalid state")
	}
}

// TestGmailOAuth_CallbackTokenEndpointFailure verifies the operator-facing
// error path when Google rejects the code exchange.
func TestGmailOAuth_CallbackTokenEndpointFailure(t *testing.T) {
	t.Setenv("GMAIL_CLIENT_ID", "test-client-id")
	t.Setenv("GMAIL_CLIENT_SECRET", "test-client-secret")

	fts := newFakeTokenServer(t)
	fts.respStatus = http.StatusBadRequest
	fts.respBody = map[string]any{"error": "invalid_grant"}
	withGmailEndpoints(t, fts.URL)

	s := newOAuthTestServer(t)
	state := s.oauthStateCache.issue()

	req := httptest.NewRequest(http.MethodGet,
		"https://example.invalid/auth/gmail/callback?code=expired&state="+state, nil)
	rec := httptest.NewRecorder()

	s.handleGmailAuthCallback(rec, req)

	if rec.Code < 500 {
		t.Fatalf("expected 5xx, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	stored, _ := s.store.GetSetting(gmailRefreshTokenSettingKey)
	if stored != nil {
		t.Error("nothing should have been persisted on a failed exchange")
	}
}

// TestHydrateGmailRefreshTokenFromSettings checks the boot-time path: a
// fresh server with empty GMAIL_REFRESH_TOKEN env but a stored token in
// settings exports the env var so the existing Gmail MCP code can refresh
// without operator intervention.
func TestHydrateGmailRefreshTokenFromSettings(t *testing.T) {
	t.Setenv("GMAIL_REFRESH_TOKEN", "")

	s := newOAuthTestServer(t)
	if err := s.store.UpsertSetting(Setting{
		Key:   gmailRefreshTokenSettingKey,
		Value: "stored-refresh-token",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	s.hydrateGmailRefreshToken()

	if got := os.Getenv("GMAIL_REFRESH_TOKEN"); got != "stored-refresh-token" {
		t.Errorf("env GMAIL_REFRESH_TOKEN = %q, want stored-refresh-token", got)
	}
}

// TestHydrateGmailRefreshTokenSkipsWhenEnvAlreadySet ensures we never
// clobber an explicit env-var (e.g. one the operator set in their hosting
// platform's config) with a stale stored value.
func TestHydrateGmailRefreshTokenSkipsWhenEnvAlreadySet(t *testing.T) {
	t.Setenv("GMAIL_REFRESH_TOKEN", "explicit-env-token")

	s := newOAuthTestServer(t)
	if err := s.store.UpsertSetting(Setting{
		Key:   gmailRefreshTokenSettingKey,
		Value: "stored-but-stale",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	s.hydrateGmailRefreshToken()

	if got := os.Getenv("GMAIL_REFRESH_TOKEN"); got != "explicit-env-token" {
		t.Errorf("env GMAIL_REFRESH_TOKEN = %q, want explicit-env-token (env should win)", got)
	}
}

// TestOAuthStateCache_TTL verifies that issued states are reaped after
// their TTL so a leaked state token can't sit around forever waiting for
// an attacker to use it.
func TestOAuthStateCache_TTL(t *testing.T) {
	c := newOAuthStateCache()
	state := c.issue()
	if !c.exists(state) {
		t.Fatal("issued state should exist immediately")
	}
	// Force expiration by rewriting the entry's expiry.
	c.expireAllForTest()
	if c.exists(state) {
		t.Error("state should have been reaped after expiry")
	}
}
