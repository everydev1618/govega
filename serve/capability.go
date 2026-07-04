package serve

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// capabilitySigner mints and verifies path-scoped capability tokens for
// deliverable URLs (/workspace/… and /apps/…). A token grants access to a
// whole deliverable "scope" (a project/app directory) rather than a single
// file, so relative assets under it load without their own token. Keyed by
// VEGA_WORKSPACE_SIGNING_KEY; when the key is empty the signer is nil and the
// server runs in open mode (self-hosted / local single-user — no gating).
//
// See docs/app-hosting-design.md §6.
type capabilitySigner struct {
	key []byte
}

// newCapabilitySigner returns nil (open mode) when key is empty.
func newCapabilitySigner(key string) *capabilitySigner {
	if key == "" {
		return nil
	}
	return &capabilitySigner{key: []byte(key)}
}

// capabilityScopeOf reduces a request path to the deliverable it belongs to:
// the first two segments (/workspace/<project>/ or /apps/<app>/) so every file
// under a deliverable shares one signature. A bare top-level file is its own
// scope.
func capabilityScopeOf(path string) string {
	trailing := strings.HasSuffix(path, "/")
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	if len(segs) >= 3 || (len(segs) == 2 && trailing) {
		return "/" + segs[0] + "/" + segs[1] + "/"
	}
	return path
}

// sign returns the base64url HMAC of a scope.
func (c *capabilitySigner) sign(scope string) string {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(scope))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SignURLPath returns the ?sig= value that grants access to path's scope.
func (c *capabilitySigner) SignURLPath(path string) string {
	return c.sign(capabilityScopeOf(path))
}

// verify reports whether sig authorizes path (constant-time).
func (c *capabilitySigner) verify(path, sig string) bool {
	if sig == "" {
		return false
	}
	want := c.sign(capabilityScopeOf(path))
	return subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1
}

// capabilityCookieName is the cookie a valid ?sig= sets so a deliverable's
// relative asset requests (which drop the query string) stay authorized.
const capabilityCookieName = "vega_cap"

// capabilityGate decides whether a /workspace/ or /apps/ request may be served.
// A nil signer is open mode (always allow). Otherwise: a portal-authenticated
// request always passes; a valid ?sig= passes and returns a scoped cookie to
// set; a valid capability cookie passes; everything else is denied.
func capabilityGate(s *capabilitySigner, r *http.Request) (allow bool, setCookie *http.Cookie) {
	if s == nil {
		return true, nil // open mode
	}
	if isPortalAuthenticated(r) {
		return true, nil
	}
	if sig := r.URL.Query().Get("sig"); sig != "" && s.verify(r.URL.Path, sig) {
		return true, &http.Cookie{
			Name:     capabilityCookieName,
			Value:    sig,
			Path:     capabilityScopeOf(r.URL.Path),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		}
	}
	for _, c := range r.Cookies() {
		if c.Name == capabilityCookieName && s.verify(r.URL.Path, c.Value) {
			return true, nil
		}
	}
	return false, nil
}

// isPortalAuthenticated reports whether the request already carries portal
// identity (a bearer token or a portal/customer session cookie), in which case
// capability gating is unnecessary.
func isPortalAuthenticated(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	for _, name := range []string{"v39a_token", "customer_session"} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return true
		}
	}
	return false
}
