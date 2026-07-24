package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Google Calendar OAuth — a read-only mirror of the Gmail flow (oauth_gmail.go).
// Same Google endpoints and single-use CSRF state cache (s.oauthStateCache);
// only the scope, setting key, env var, and callback path differ. The token
// endpoints reuse the gmail* package vars so tests can point both at one
// httptest server.
const (
	gcalScope                  = "https://www.googleapis.com/auth/calendar.readonly"
	gcalRefreshTokenSettingKey = "gcal.refresh_token"
	gcalCallbackPath           = "/auth/gcal/callback"
)

// gcalClientCreds returns the OAuth client id/secret for the Calendar flow.
// Prefers GCAL_-prefixed vars, falling back to the shared GOOGLE_ ones so a
// single Google Cloud OAuth client can back both Gmail and Calendar.
func gcalClientCreds() (id, secret string) {
	id = os.Getenv("GCAL_CLIENT_ID")
	if id == "" {
		id = os.Getenv("GOOGLE_CLIENT_ID")
	}
	secret = os.Getenv("GCAL_CLIENT_SECRET")
	if secret == "" {
		secret = os.Getenv("GOOGLE_CLIENT_SECRET")
	}
	return id, secret
}

// gcalRedirectURI computes the absolute callback URL the operator registers
// with Google. Mirrors gmailRedirectURI.
func (s *Server) gcalRedirectURI(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/") + gcalCallbackPath
	}
	scheme := "https"
	if r.TLS == nil && !strings.HasPrefix(strings.ToLower(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + gcalCallbackPath
}

// handleGcalAuthStart redirects to Google's consent screen for read-only
// calendar access, requesting offline access + forced consent so we always
// get a refresh_token.
func (s *Server) handleGcalAuthStart(w http.ResponseWriter, r *http.Request) {
	clientID, clientSecret := gcalClientCreds()
	if clientID == "" || clientSecret == "" {
		http.Error(w,
			"Google Calendar OAuth not configured: set GCAL_CLIENT_ID and GCAL_CLIENT_SECRET (or GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET) in the server environment, then retry. These come from the OAuth client you create in Google Cloud Console.",
			http.StatusServiceUnavailable)
		return
	}

	state := s.oauthStateCache.issue()
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {s.gcalRedirectURI(r)},
		"response_type": {"code"},
		"scope":         {gcalScope},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	http.Redirect(w, r, gmailAuthURL+"?"+q.Encode(), http.StatusFound)
}

// handleGcalAuthCallback exchanges the auth code for a refresh token, persists
// it to settings, and exports GCAL_REFRESH_TOKEN so the check_availability tool
// picks it up without a restart.
func (s *Server) handleGcalAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code on callback", http.StatusBadRequest)
		return
	}
	if !s.oauthStateCache.consume(state) {
		http.Error(w, "invalid or expired state — start a new authorization at /auth/gcal/start", http.StatusBadRequest)
		return
	}

	clientID, clientSecret := gcalClientCreds()
	if clientID == "" || clientSecret == "" {
		http.Error(w, "Google Calendar OAuth not configured (client id/secret missing)", http.StatusServiceUnavailable)
		return
	}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {s.gcalRedirectURI(r)},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, gmailTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		slog.Error("gcal oauth: build token request", "error", err)
		http.Error(w, "failed to build token request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("gcal oauth: token request failed", "error", err)
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		slog.Error("gcal oauth: token endpoint rejected exchange", "status", resp.StatusCode, "body", string(body))
		http.Error(w, fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, string(body)), http.StatusBadGateway)
		return
	}

	var tok struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		slog.Error("gcal oauth: decode token response", "error", err)
		http.Error(w, "could not decode token response", http.StatusBadGateway)
		return
	}
	if tok.RefreshToken == "" {
		http.Error(w,
			"Google did not return a refresh_token. Visit https://myaccount.google.com/permissions, revoke the existing grant for this client, and retry /auth/gcal/start.",
			http.StatusBadGateway)
		return
	}

	if err := s.store.UpsertSetting(Setting{Key: gcalRefreshTokenSettingKey, Value: tok.RefreshToken}); err != nil {
		slog.Error("gcal oauth: persist refresh token", "error", err)
		http.Error(w, "could not persist refresh token", http.StatusInternalServerError)
		return
	}
	if err := os.Setenv("GCAL_REFRESH_TOKEN", tok.RefreshToken); err != nil {
		slog.Error("gcal oauth: export GCAL_REFRESH_TOKEN", "error", err)
	}

	slog.Info("gcal oauth: refresh token persisted and exported")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `<!doctype html>
<html><body style="font-family: system-ui, sans-serif; max-width: 40ch; margin: 4rem auto; line-height: 1.5">
<h1>Google Calendar connected</h1>
<p>Read-only calendar access is stored. Your assistant can now check your availability (free/busy). It cannot create or change events.</p>
<p><small>This page is served from your Vega instance, not Google.</small></p>
</body></html>`)
}

// hydrateGcalRefreshToken exports a stored refresh token to the environment at
// boot when GCAL_REFRESH_TOKEN isn't already set. Mirrors the Gmail path.
func (s *Server) hydrateGcalRefreshToken() {
	if os.Getenv("GCAL_REFRESH_TOKEN") != "" {
		return
	}
	if s.store == nil {
		return
	}
	stored, err := s.store.GetSetting(gcalRefreshTokenSettingKey)
	if err != nil || stored == nil || stored.Value == "" {
		return
	}
	if err := os.Setenv("GCAL_REFRESH_TOKEN", stored.Value); err != nil {
		slog.Warn("gcal oauth: hydrate env failed", "error", err)
		return
	}
	slog.Info("gcal oauth: hydrated GCAL_REFRESH_TOKEN from settings")
}
