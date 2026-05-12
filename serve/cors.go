// Package serve — cors.go
//
// Reflective CORS allowlist driven by VEGA_ALLOWED_ORIGINS (legacy:
// APEX_ALLOWED_ORIGINS). Default-deny: when the env is unset or empty,
// no cross-origin response carries an Access-Control-Allow-Origin
// header, and browsers therefore reject every cross-origin request.
//
// Decision 4 of the Phase 2 RFC means production cloud traffic is always
// same-origin (Cloudflare path-routes /api/v1/* to the tenant backend on
// the tenant subdomain), so the middleware is dead code on the hot path.
// It exists for defense-in-depth and to support legitimate cross-origin
// callers (dev tooling, future admin UIs) opted in via env.

package serve

import (
	"net/http"
	"strings"

	"github.com/everydev1618/govega/internal/envcompat"
)

// LoadCORSConfig reads VEGA_ALLOWED_ORIGINS (comma-separated exact-match list),
// falling back to the legacy APEX_ALLOWED_ORIGINS with a deprecation warning.
// Empty/unset → nil → no cross-origin allowed.
func LoadCORSConfig() map[string]bool {
	raw := envcompat.Get("VEGA_ALLOWED_ORIGINS")
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out[o] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// corsMiddleware echoes Origin if it is in the allowlist, omits all CORS
// response headers otherwise. Same-origin requests (no Origin header) pass
// through untouched. Disallowed preflights still return 204 — but without
// any Access-Control-* headers, so browsers treat the request as denied.
func corsMiddleware(allowed map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
