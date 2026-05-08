package main

import (
	"net/http"
	"os"
	"strings"
)

// CORS for the control plane.
//
// The control plane is hit cross-origin by SPAs (in dev: localhost:5173;
// in prod: tenant subdomains like https://acme.apex.io that POST to
// /exchange or /dev/mint when minting tokens for cloud-mode flows).
// Default-deny semantics match the tenant backend (govega/serve/cors.go):
// empty CONTROL_PLANE_ALLOWED_ORIGINS → no cross-origin response carries
// an Access-Control-Allow-Origin header.

// loadCORSConfig reads CONTROL_PLANE_ALLOWED_ORIGINS (comma-separated
// exact-match list of origins). Whitespace tolerated, empty entries
// dropped. Empty/unset returns nil → no cross-origin allowed.
func loadCORSConfig() map[string]bool {
	raw := os.Getenv("CONTROL_PLANE_ALLOWED_ORIGINS")
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
// response headers otherwise. Same-origin requests (no Origin header)
// pass through untouched. Disallowed preflights still 204 — but without
// Access-Control-* headers, so browsers treat them as denied.
//
// X-Dev-Secret is in the allowed-headers list because the dev /dev/mint
// flow uses it; cookies aren't allowed (we use bearer tokens, not
// session cookies).
func corsMiddleware(allowed map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Dev-Secret")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
