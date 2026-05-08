package main

import (
	"encoding/json"
	"net/http"
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
