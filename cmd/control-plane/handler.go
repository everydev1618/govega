package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/everydev1618/govega/internal/authmint"
	"github.com/golang-jwt/jwt/v5"
)

// Config wires the control plane handlers. DevSecret guards /dev/mint —
// when empty, the endpoint returns 503. Set APEX_DEV_SECRET to enable.
type Config struct {
	Signer    *authmint.Signer
	Issuer    string
	DevSecret string

	// Memberships maps user_id → list of tenant slugs that user can
	// log in to. Used by /dev/login (returns the slugs) and /dev/mint
	// (rejects if requested tenant isn't in the user's list).
	// When nil/empty, /dev/mint keeps its pre-#9 behavior of accepting
	// any (tenant, user) pair — that backcompat is intentional so this
	// rollout doesn't break existing tenant deployments. Populated from
	// DEV_MEMBERSHIPS_JSON in main.go.
	Memberships map[string][]string

	// Gmail OAuth (Phase 2E). When GoogleClientID is empty, the
	// /oauth/gmail/* endpoints return 503.
	GoogleClientID     string
	GoogleClientSecret string
	// GoogleRedirectURI is the redirect_uri registered with Google. Must
	// be reachable at this control plane's public URL plus
	// /oauth/gmail/callback.
	GoogleRedirectURI string
	// GoogleAuthURL / GoogleTokenURL are overridable for tests; default to
	// Google's production endpoints in newHandler.
	GoogleAuthURL  string
	GoogleTokenURL string
	// ReturnURLPattern (optional) restricts where the callback may
	// redirect users back to. Production should set this to match the
	// tenant subdomain pattern (e.g. ^https://[a-z0-9-]+\.apex\.io/.*$).
	ReturnURLPattern *regexp.Regexp
}

// Default access-token TTL when /dev/mint is called without ttl_seconds.
// Matches Decision 2 of the Phase 2 RFC.
const defaultAccessTTLSeconds = 15 * 60

// newHandler builds the control plane router. Routes:
//
//   - GET  /healthz   — liveness probe
//   - GET  /jwks      — public key for tenant backends
//   - POST /dev/mint  — dev-only token issuance, gated by X-Dev-Secret
//
// Real auth flows (WorkOS callback, /exchange, /signup) land in 2C.2.
func newHandler(cfg Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("GET /jwks", cfg.Signer.JWKSHandler())

	mux.HandleFunc("POST /dev/mint", devMintHandler(cfg))
	mux.HandleFunc("POST /dev/login", devLoginHandler(cfg))

	mux.HandleFunc("POST /oauth/gmail/init", gmailInitHandler(cfg))
	mux.HandleFunc("GET /oauth/gmail/callback", gmailCallbackHandler(cfg))

	return mux
}

// devMintHandler returns a token-issuing handler guarded by a shared secret.
// It is intentionally explicit: an unset DevSecret disables the endpoint
// entirely (503), so dev-only convenience can never accidentally ship as
// a "log in as anyone" backdoor in production.
func devMintHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.DevSecret == "" {
			http.Error(w, "dev mint disabled (set APEX_DEV_SECRET to enable)", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("X-Dev-Secret") != cfg.DevSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			Tenant     string `json:"tenant"`
			User       string `json:"user"`
			TTLSeconds int    `json:"ttl_seconds"`
			Scope      string `json:"scope"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.Tenant == "" {
			http.Error(w, "tenant is required", http.StatusBadRequest)
			return
		}
		if req.User == "" {
			req.User = "dev_user"
		}
		// Membership gate. When Memberships is populated (deployment opted
		// in via DEV_MEMBERSHIPS_JSON), the requested tenant must be one
		// the requested user belongs to — prevents a curious user from
		// minting themselves a token for any tenant slug they can guess.
		// nil/empty Memberships keeps the pre-#9 "accept any" behavior so
		// existing single-tenant deployments don't break.
		if len(cfg.Memberships) > 0 {
			if !slices.Contains(cfg.Memberships[req.User], req.Tenant) {
				http.Error(w, "tenant not in user memberships", http.StatusForbidden)
				return
			}
		}
		ttl := req.TTLSeconds
		if ttl <= 0 {
			ttl = defaultAccessTTLSeconds
		}
		scope := req.Scope
		if scope == "" {
			scope = "read write admin"
		}

		now := time.Now()
		tok, err := cfg.Signer.Sign(jwt.MapClaims{
			"iss":   cfg.Issuer,
			"aud":   req.Tenant,
			"sub":   req.User,
			"iat":   now.Unix(),
			"exp":   now.Add(time.Duration(ttl) * time.Second).Unix(),
			"scope": scope,
		})
		if err != nil {
			http.Error(w, "sign", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": tok,
			"expires_in":   ttl,
			"token_type":   "Bearer",
		})
	}
}

// devLoginHandler returns the list of tenant slugs a user_id can log
// in to. Same X-Dev-Secret gate as /dev/mint — we don't expose this
// publicly so anyone with a control-plane URL can enumerate users.
// Unknown users return an empty list (HTTP 200), not 404 — letting
// the SPA render "no workspaces yet" consistently and never leaking
// which user_ids are valid via status code.
func devLoginHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.DevSecret == "" {
			http.Error(w, "dev login disabled (set APEX_DEV_SECRET to enable)", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("X-Dev-Secret") != cfg.DevSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.UserID == "" {
			http.Error(w, "user_id is required", http.StatusBadRequest)
			return
		}
		memberships := cfg.Memberships[req.UserID]
		if memberships == nil {
			memberships = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"memberships": memberships,
		})
	}
}
