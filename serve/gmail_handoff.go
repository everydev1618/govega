package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Tenant-side Gmail OAuth handlers for the cloud-mode flow (Phase 2E.3).
//
// /api/v1/integrations/gmail/start    — proxies a SPA-initiated request to
//                                       the control plane's /oauth/gmail/init
//                                       and returns the Google authorize URL.
// /api/v1/integrations/gmail/handoff  — receives a control-plane-signed
//                                       handoff JWT after Google authorization
//                                       completes; validates, persists the
//                                       refresh token, and exports
//                                       GMAIL_REFRESH_TOKEN.
//
// Both endpoints live under /api/v1/* and therefore require the user JWT
// (auth middleware enforces). The handoff additionally validates a
// purpose=gmail-handoff JWT signed by the control plane and rejects
// replays via in-memory jti tracking.

const gmailHandoffPurpose = "gmail-handoff"

// handleGmailIntegrationStart is called by the SPA when the user clicks
// "Connect Gmail" in cloud mode. It proxies the user's bearer token to
// the control plane's /oauth/gmail/init endpoint, which validates the
// token, mints a state JWT, and returns Google's authorize URL.
func (s *Server) handleGmailIntegrationStart(w http.ResponseWriter, r *http.Request) {
	if s.controlPlaneURL == "" {
		http.Error(w, "cloud-mode Gmail OAuth disabled (APEX_CONTROL_PLANE_URL unset)", http.StatusServiceUnavailable)
		return
	}

	claims, ok := ClaimsFrom(r.Context())
	if !ok {
		http.Error(w, "no auth claims", http.StatusUnauthorized)
		return
	}

	var body struct {
		Return string `json:"return"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Return == "" {
		http.Error(w, "return is required", http.StatusBadRequest)
		return
	}

	upstreamBody, _ := json.Marshal(map[string]string{
		"tenant": claims.TenantID,
		"return": body.Return,
	})
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		s.controlPlaneURL+"/oauth/gmail/init", bytes.NewReader(upstreamBody))
	if err != nil {
		http.Error(w, "build upstream request", http.StatusInternalServerError)
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", r.Header.Get("Authorization"))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		slog.Error("gmail integration start: upstream call failed", "err", err)
		http.Error(w, "control plane unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// handleGmailIntegrationHandoff consumes a control-plane-issued handoff
// JWT and persists the embedded refresh token to settings (same path as
// the legacy callback). All checks must pass before any persistence:
//
//   - JWT signature, iss, aud (=this tenant), exp, purpose=gmail-handoff
//   - sub matches the authenticated user (defense in depth)
//   - jti has not been consumed before (replay protection)
//   - gmail_refresh_token claim is non-empty
func (s *Server) handleGmailIntegrationHandoff(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFrom(r.Context())
	if !ok {
		http.Error(w, "no auth claims", http.StatusUnauthorized)
		return
	}

	var body struct {
		HandoffToken string `json:"handoff_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.HandoffToken == "" {
		http.Error(w, "handoff_token is required", http.StatusBadRequest)
		return
	}

	handoffClaims, err := s.authCfg.Verify(body.HandoffToken, gmailHandoffPurpose)
	if err != nil {
		http.Error(w, "invalid handoff token: "+err.Error(), http.StatusBadRequest)
		return
	}

	if sub, _ := handoffClaims["sub"].(string); sub != claims.UserID {
		http.Error(w, "handoff sub mismatch", http.StatusForbidden)
		return
	}

	jti, _ := handoffClaims["jti"].(string)
	if jti == "" {
		http.Error(w, "handoff missing jti", http.StatusBadRequest)
		return
	}
	if !s.consumeHandoffJTI(jti, claimsExpiry(handoffClaims)) {
		http.Error(w, "handoff already consumed", http.StatusBadRequest)
		return
	}

	refresh, _ := handoffClaims["gmail_refresh_token"].(string)
	if refresh == "" {
		http.Error(w, "handoff missing gmail_refresh_token", http.StatusBadRequest)
		return
	}

	if err := s.persistGmailRefreshToken(r.Context(), refresh); err != nil {
		http.Error(w, "could not persist refresh token", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"connected":true}`)
}

// consumeHandoffJTI atomically claims a jti for one-time use. Returns
// false if the jti has been seen before. Stored entries are kept until
// process exit; volume is bounded by Gmail-connect frequency, which is
// once per user-tenant for the life of the refresh token.
func (s *Server) consumeHandoffJTI(jti string, exp int64) bool {
	_, loaded := s.consumedJTIs.LoadOrStore(jti, exp)
	return !loaded
}

// claimsExpiry pulls the exp claim out as unix seconds, returning 0 when
// absent or malformed. Caller has already validated exp via parser
// options, so this is just for cache bookkeeping.
func claimsExpiry(c map[string]any) int64 {
	switch v := c["exp"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}

// persistGmailRefreshToken writes the refresh token to settings (so a
// fresh process boot can hydrate it) and exports GMAIL_REFRESH_TOKEN so
// the Gmail MCP path picks it up immediately. Mirrors the legacy
// callback's behavior so cloud and self-hosted converge on the same
// downstream wiring.
func (s *Server) persistGmailRefreshToken(_ context.Context, refresh string) error {
	if err := s.store.UpsertSetting(Setting{
		Key:   gmailRefreshTokenSettingKey,
		Value: refresh,
	}); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if err := os.Setenv("GMAIL_REFRESH_TOKEN", refresh); err != nil {
		// Persistence already succeeded; next boot will hydrate from
		// settings, so this is logged but not fatal.
		slog.Warn("gmail handoff: export GMAIL_REFRESH_TOKEN", "err", err)
	}
	return nil
}
