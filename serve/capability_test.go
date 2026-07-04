package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilityScopeOf(t *testing.T) {
	cases := map[string]string{
		"/workspace/pacman/index.html":     "/workspace/pacman/",
		"/workspace/pacman/assets/app.js":  "/workspace/pacman/",
		"/apps/pacman/":                    "/apps/pacman/",
		"/apps/pacman/sub/x.css":           "/apps/pacman/",
		"/workspace/loose.html":            "/workspace/loose.html", // no project dir → the file itself
	}
	for path, want := range cases {
		if got := capabilityScopeOf(path); got != want {
			t.Errorf("scopeOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCapabilitySignerRoundTrip(t *testing.T) {
	s := newCapabilitySigner("secret-key")
	if s == nil {
		t.Fatal("signer should be non-nil when key is set")
	}
	// Every path under the same scope shares one signature.
	sigIndex := s.sign(capabilityScopeOf("/workspace/pacman/index.html"))
	if !s.verify("/workspace/pacman/index.html", sigIndex) {
		t.Error("index.html should verify with its scope signature")
	}
	if !s.verify("/workspace/pacman/assets/app.js", sigIndex) {
		t.Error("asset under the same scope must verify with the same signature (so relative assets load)")
	}
	if s.verify("/workspace/other/index.html", sigIndex) {
		t.Error("a different scope must NOT verify")
	}
	if s.verify("/workspace/pacman/index.html", "garbage") {
		t.Error("garbage signature must not verify")
	}
}

func TestCapabilitySignerEmptyKeyIsOpenMode(t *testing.T) {
	if newCapabilitySigner("") != nil {
		t.Error("empty key ⇒ nil signer (open mode, no gating)")
	}
}

// The gate: open mode always allows; gated mode allows a valid ?sig=, a valid
// capability cookie, or a portal-authenticated request; otherwise denies.
func TestCapabilityGate(t *testing.T) {
	s := newCapabilitySigner("k")
	sig := s.sign(capabilityScopeOf("/workspace/pacman/index.html"))

	// Valid query sig → allow + set a scoped cookie.
	r := httptest.NewRequest("GET", "/workspace/pacman/index.html?sig="+sig, nil)
	allow, cookie := capabilityGate(s, r)
	if !allow {
		t.Fatal("valid ?sig= should allow")
	}
	if cookie == nil || cookie.Path != "/workspace/pacman/" {
		t.Errorf("expected a cookie scoped to /workspace/pacman/, got %+v", cookie)
	}

	// The cookie it set should then authorize an asset request with no sig.
	r2 := httptest.NewRequest("GET", "/workspace/pacman/assets/app.js", nil)
	r2.AddCookie(cookie)
	if allow2, _ := capabilityGate(s, r2); !allow2 {
		t.Error("a request carrying the capability cookie should be allowed")
	}

	// No sig, no cookie → deny.
	r3 := httptest.NewRequest("GET", "/workspace/pacman/index.html", nil)
	if allow3, _ := capabilityGate(s, r3); allow3 {
		t.Error("unauthenticated request should be denied in gated mode")
	}

	// nil signer (open mode) → always allow.
	r4 := httptest.NewRequest("GET", "/workspace/anything", nil)
	if allow4, _ := capabilityGate(nil, r4); !allow4 {
		t.Error("open mode should always allow")
	}
}

// A portal-authenticated request (bearer or portal cookie) bypasses the sig.
func TestCapabilityGateAllowsPortalAuth(t *testing.T) {
	s := newCapabilitySigner("k")
	r := httptest.NewRequest("GET", "/workspace/pacman/index.html", nil)
	r.Header.Set("Authorization", "Bearer sometoken")
	if allow, _ := capabilityGate(s, r); !allow {
		t.Error("portal-authenticated request should bypass capability gating")
	}
	r2 := httptest.NewRequest("GET", "/apps/pacman/", nil)
	r2.AddCookie(&http.Cookie{Name: "v39a_token", Value: "x"})
	if allow, _ := capabilityGate(s, r2); !allow {
		t.Error("portal cookie should bypass capability gating")
	}
}
