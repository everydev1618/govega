package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// helper: hits the wrapped handler with the given Origin header (empty string
// = no header sent) and returns the response. The downstream handler always
// 200s so we can isolate CORS behaviour.
func corsCall(t *testing.T, mw func(http.Handler) http.Handler, method, origin string) *http.Response {
	t.Helper()
	srv := httptest.NewServer(mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(method, srv.URL+"/api/v1/stats", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", "POST")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return res
}

func TestCORS_NoAllowlist_NoACAOHeaderEverSet(t *testing.T) {
	// Default: empty env → no cross-origin permitted. Even when an attacker's
	// site sends an Origin header, we don't echo it. The request still reaches
	// the downstream handler (browsers, not servers, enforce CORS); JS just
	// can't read the response.
	mw := corsMiddleware(nil) // nil/empty allowlist
	for _, origin := range []string{"", "https://evil.example", "https://app.apex.io"} {
		res := corsCall(t, mw, http.MethodGet, origin)
		got := res.Header.Get("Access-Control-Allow-Origin")
		if got != "" {
			t.Errorf("origin=%q: ACAO=%q, want empty (no allowlist)", origin, got)
		}
		res.Body.Close()
	}
}

func TestCORS_AllowedOrigin_EchoedNotWildcarded(t *testing.T) {
	allowed := map[string]bool{"https://app.apex.io": true}
	mw := corsMiddleware(allowed)

	res := corsCall(t, mw, http.MethodGet, "https://app.apex.io")
	defer res.Body.Close()

	got := res.Header.Get("Access-Control-Allow-Origin")
	if got != "https://app.apex.io" {
		t.Errorf("ACAO=%q, want \"https://app.apex.io\" (must echo origin, never wildcard)", got)
	}
	if res.Header.Get("Vary") != "Origin" {
		t.Errorf("Vary=%q, want \"Origin\" (caches must key on Origin)", res.Header.Get("Vary"))
	}
}

func TestCORS_DisallowedOrigin_NoACAO(t *testing.T) {
	allowed := map[string]bool{"https://app.apex.io": true}
	mw := corsMiddleware(allowed)

	res := corsCall(t, mw, http.MethodGet, "https://evil.example")
	defer res.Body.Close()

	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO=%q, want empty (origin not in allowlist)", got)
	}
}

func TestCORS_OPTIONSPreflight_FromAllowedOrigin_204WithHeaders(t *testing.T) {
	allowed := map[string]bool{"https://app.apex.io": true}
	mw := corsMiddleware(allowed)

	res := corsCall(t, mw, http.MethodOptions, "https://app.apex.io")
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Errorf("status=%d, want 204", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "https://app.apex.io" {
		t.Errorf("ACAO not set on preflight")
	}
	methods := res.Header.Get("Access-Control-Allow-Methods")
	if methods == "" {
		t.Errorf("Allow-Methods not set on preflight")
	}
	// Every HTTP verb the API actually uses must be advertised; missing
	// methods cause browsers to fail the preflight before the handler ever
	// runs (refs govega#64 — PATCH endpoints were unreachable from apex-host-mgmt).
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if !contains(methods, m) {
			t.Errorf("Allow-Methods missing %q; got %q", m, methods)
		}
	}
	headers := res.Header.Get("Access-Control-Allow-Headers")
	if headers == "" {
		t.Errorf("Allow-Headers not set on preflight")
	}
	// Decision 9: X-Auth-User is deleted; the JWT subject replaces it.
	// Allow-Headers must NOT advertise X-Auth-User any more.
	for _, h := range []string{"X-Auth-User", "x-auth-user"} {
		if contains(headers, h) {
			t.Errorf("Allow-Headers includes %q; should be removed (Decision 9)", h)
		}
	}
	// Authorization is the only auth-relevant header in the new world.
	if !contains(headers, "Authorization") {
		t.Errorf("Allow-Headers missing Authorization; %q", headers)
	}
}

func TestCORS_OPTIONSPreflight_FromDisallowedOrigin_204NoCORS(t *testing.T) {
	allowed := map[string]bool{"https://app.apex.io": true}
	mw := corsMiddleware(allowed)

	res := corsCall(t, mw, http.MethodOptions, "https://evil.example")
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Errorf("status=%d, want 204", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("ACAO leaked to disallowed origin")
	}
}

func TestCORS_NoOrigin_SameOriginRequest_PassThrough(t *testing.T) {
	// Same-origin requests don't include an Origin header (or do, depending
	// on browser/method, but never need CORS validation). Middleware must
	// not interfere.
	allowed := map[string]bool{"https://app.apex.io": true}
	mw := corsMiddleware(allowed)

	res := corsCall(t, mw, http.MethodGet, "")
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200 (same-origin must pass through)", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("ACAO set on same-origin request")
	}
}

func TestLoadCORSConfig_EnvParsing(t *testing.T) {
	cases := []struct {
		env  string
		want map[string]bool
	}{
		{"", nil},
		{"https://app.apex.io", map[string]bool{"https://app.apex.io": true}},
		{"https://app.apex.io,http://localhost:5173", map[string]bool{
			"https://app.apex.io":    true,
			"http://localhost:5173":  true,
		}},
		// Tolerate whitespace around commas.
		{" https://app.apex.io , http://localhost:5173 ", map[string]bool{
			"https://app.apex.io":   true,
			"http://localhost:5173": true,
		}},
		// Empty entries are ignored.
		{"https://app.apex.io,,", map[string]bool{"https://app.apex.io": true}},
	}
	for _, tc := range cases {
		t.Setenv("APEX_ALLOWED_ORIGINS", tc.env)
		got := LoadCORSConfig()
		if !mapEqual(got, tc.want) {
			t.Errorf("env=%q: got %v, want %v", tc.env, got, tc.want)
		}
	}
}

// contains reports whether sub is in s, case-insensitive.
func contains(s, sub string) bool {
	return indexFold(s, sub) >= 0
}

// indexFold is a tiny case-insensitive substring search to avoid pulling in
// strings.Contains + ToLower in the hot test path.
func indexFold(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				continue outer
			}
		}
		return i
	}
	return -1
}

func mapEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
