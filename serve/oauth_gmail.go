package serve

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Gmail OAuth endpoint URLs. Package-level vars rather than constants so
// tests can swap them for a httptest.Server. Production code should never
// reassign these.
var (
	gmailAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	gmailTokenURL = "https://oauth2.googleapis.com/token"
)

const (
	gmailScope                  = "https://www.googleapis.com/auth/gmail.modify"
	gmailRefreshTokenSettingKey = "gmail.refresh_token"
	gmailCallbackPath           = "/auth/gmail/callback"
	oauthStateTTL               = 5 * time.Minute
)

// oauthStateCache holds short-lived CSRF state tokens issued during the
// OAuth start phase. Each token is single-use: consume() removes it.
type oauthStateCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func newOAuthStateCache() *oauthStateCache {
	return &oauthStateCache{entries: make(map[string]time.Time)}
}

// issue mints a new random state token, records its expiry, and returns it.
func (c *oauthStateCache) issue() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		// Reading from crypto/rand should never fail; if it does, there's
		// nothing safe to do but panic and let the supervisor restart us.
		panic(fmt.Sprintf("oauth: rand.Read failed: %v", err))
	}
	state := base64.RawURLEncoding.EncodeToString(buf)
	c.mu.Lock()
	c.entries[state] = time.Now().Add(oauthStateTTL)
	c.mu.Unlock()
	return state
}

// exists reports whether the state is still valid (issued and unexpired).
// Reaps expired entries lazily on lookup.
func (c *oauthStateCache) exists(state string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry, ok := c.entries[state]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(c.entries, state)
		return false
	}
	return true
}

// consume validates the state and removes it. Returns true on success.
// Single-use semantics defeat replay even if a state token leaks.
func (c *oauthStateCache) consume(state string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry, ok := c.entries[state]
	if !ok {
		return false
	}
	delete(c.entries, state)
	return time.Now().Before(expiry)
}

// expireAllForTest is a test seam — never call from production code.
func (c *oauthStateCache) expireAllForTest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		c.entries[k] = time.Now().Add(-time.Second)
	}
}

// gmailRedirectURI computes the absolute callback URL the operator must
// register with Google. Uses serverBaseURL when set (preferred — that's the
// public hostname), falling back to scheme+host from the incoming request.
func (s *Server) gmailRedirectURI(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/") + gmailCallbackPath
	}
	scheme := "https"
	if r.TLS == nil && !strings.HasPrefix(strings.ToLower(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + gmailCallbackPath
}

// handleGmailAuthStart issues a fresh CSRF state and redirects the user to
// Google's authorize URL with the gmail.modify scope, offline access (so we
// get a refresh_token), and a forced consent prompt (so re-auth still
// returns a refresh_token even if the user has previously authorized).
func (s *Server) handleGmailAuthStart(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("GMAIL_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		http.Error(w,
			"Gmail OAuth not configured: set GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET in the server environment, then retry. These come from the OAuth client you create in Google Cloud Console.",
			http.StatusServiceUnavailable)
		return
	}

	state := s.oauthStateCache.issue()
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {s.gmailRedirectURI(r)},
		"response_type": {"code"},
		"scope":         {gmailScope},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	http.Redirect(w, r, gmailAuthURL+"?"+q.Encode(), http.StatusFound)
}

// handleGmailAuthCallback validates the state, exchanges the auth code for
// a refresh + access token, persists the refresh token to settings, and
// exports it to GMAIL_REFRESH_TOKEN so the existing Gmail MCP path picks
// it up without further configuration.
func (s *Server) handleGmailAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code on callback", http.StatusBadRequest)
		return
	}
	if !s.oauthStateCache.consume(state) {
		http.Error(w, "invalid or expired state — start a new authorization at /auth/gmail/start", http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("GMAIL_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		http.Error(w, "Gmail OAuth not configured (GMAIL_CLIENT_ID / GMAIL_CLIENT_SECRET missing)", http.StatusServiceUnavailable)
		return
	}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {s.gmailRedirectURI(r)},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, gmailTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		slog.Error("gmail oauth: build token request", "error", err)
		http.Error(w, "failed to build token request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("gmail oauth: token request failed", "error", err)
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		slog.Error("gmail oauth: token endpoint rejected exchange",
			"status", resp.StatusCode, "body", string(body))
		http.Error(w, fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, string(body)), http.StatusBadGateway)
		return
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		slog.Error("gmail oauth: decode token response", "error", err, "body", string(body))
		http.Error(w, "could not decode token response", http.StatusBadGateway)
		return
	}
	if tok.RefreshToken == "" {
		// Google only returns refresh_token when access_type=offline AND
		// prompt=consent (or when the user is authorising a fresh client).
		// We always set both, so an empty refresh_token here means Google
		// considered the consent already granted; the operator needs to
		// revoke at https://myaccount.google.com/permissions and retry.
		http.Error(w,
			"Google did not return a refresh_token. Visit https://myaccount.google.com/permissions, revoke the existing grant for this client, and retry /auth/gmail/start.",
			http.StatusBadGateway)
		return
	}

	if err := s.store.UpsertSetting(Setting{
		Key:   gmailRefreshTokenSettingKey,
		Value: tok.RefreshToken,
	}); err != nil {
		slog.Error("gmail oauth: persist refresh token", "error", err)
		http.Error(w, "could not persist refresh token", http.StatusInternalServerError)
		return
	}
	if err := os.Setenv("GMAIL_REFRESH_TOKEN", tok.RefreshToken); err != nil {
		slog.Error("gmail oauth: export GMAIL_REFRESH_TOKEN", "error", err)
		// Persistence already succeeded; next server restart will hydrate
		// from settings, so this is logged but not user-fatal.
	}

	slog.Info("gmail oauth: refresh token persisted and exported")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `<!doctype html>
<html><body style="font-family: system-ui, sans-serif; max-width: 40ch; margin: 4rem auto; line-height: 1.5">
<h1>Gmail connected</h1>
<p>The refresh token has been stored and exported. Riley (and any other Gmail-using agent) can now talk to your mailbox without further setup.</p>
<p><small>This page is served from your Vega instance, not Google.</small></p>
</body></html>`)
}

// hydrateGmailRefreshToken is the boot-time path: if GMAIL_REFRESH_TOKEN is
// empty in the environment but a token exists in settings (e.g. from a
// previous run on the same database), export it so the MCP path picks it up
// without a fresh OAuth flow. An explicit env-var always wins so operators
// can override a stale stored value without touching the database.
func (s *Server) hydrateGmailRefreshToken() {
	if os.Getenv("GMAIL_REFRESH_TOKEN") != "" {
		return
	}
	if s.store == nil {
		return
	}
	stored, err := s.store.GetSetting(gmailRefreshTokenSettingKey)
	if err != nil {
		slog.Debug("gmail oauth: no stored refresh token", "error", err)
		return
	}
	if stored == nil || stored.Value == "" {
		return
	}
	if err := os.Setenv("GMAIL_REFRESH_TOKEN", stored.Value); err != nil {
		slog.Warn("gmail oauth: hydrate env failed", "error", err)
		return
	}
	slog.Info("gmail oauth: hydrated GMAIL_REFRESH_TOKEN from settings")
}
