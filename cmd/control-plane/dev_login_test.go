package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/internal/authmint"
)

// newServerWithMemberships builds a control plane handler with the
// supplied dev memberships pre-loaded. Used by both dev-login and the
// extended dev-mint membership-gate tests.
func newServerWithMemberships(
	t *testing.T,
	devSecret string,
	memberships map[string][]string,
) *httptest.Server {
	t.Helper()
	s, err := authmint.NewSigner()
	if err != nil {
		t.Fatalf("authmint: %v", err)
	}
	srv := httptest.NewServer(newHandler(Config{
		Signer:      s,
		Issuer:      "http://control-plane.test",
		DevSecret:   devSecret,
		Memberships: memberships,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func postLogin(t *testing.T, srv *httptest.Server, secret string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/dev/login", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dev-Secret", secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return res
}

func TestDevLogin_NoSecretConfigured_503(t *testing.T) {
	// Mirror /dev/mint: when APEX_DEV_SECRET isn't set, the dev-login
	// endpoint must be unavailable. Same rationale — never accidentally
	// expose a "who is this user" probe in prod.
	srv := newServerWithMemberships(t, "", map[string][]string{"et": {"tony"}})
	res := postLogin(t, srv, "", map[string]string{"user_id": "et"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", res.StatusCode)
	}
}

func TestDevLogin_WrongSecret_401(t *testing.T) {
	srv := newServerWithMemberships(t, "shh", map[string][]string{"et": {"tony"}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/dev/login",
		strings.NewReader(`{"user_id":"et"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dev-Secret", "wrong")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", res.StatusCode)
	}
}

func TestDevLogin_MissingUser_400(t *testing.T) {
	srv := newServerWithMemberships(t, "shh", map[string][]string{})
	res := postLogin(t, srv, "shh", map[string]string{})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}

func TestDevLogin_KnownUser_ReturnsMemberships(t *testing.T) {
	srv := newServerWithMemberships(t, "shh", map[string][]string{
		"et":     {"tony", "sarah"},
		"marcel": {"marcel"},
	})
	res := postLogin(t, srv, "shh", map[string]string{"user_id": "et"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var got struct {
		Memberships []string `json:"memberships"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"tony", "sarah"}
	if len(got.Memberships) != len(want) {
		t.Fatalf("memberships=%v, want %v", got.Memberships, want)
	}
	// Order-insensitive: just check membership.
	have := map[string]bool{}
	for _, m := range got.Memberships {
		have[m] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing %q in %v", w, got.Memberships)
		}
	}
}

func TestDevLogin_UnknownUser_ReturnsEmptyMemberships(t *testing.T) {
	// An unknown user in dev-mint era isn't auth'd against anything —
	// we just say "no workspaces" and the SPA shows "no access". This
	// matches the locked-down posture: explicit allow via
	// DEV_MEMBERSHIPS_JSON, no implicit fallback.
	srv := newServerWithMemberships(t, "shh", map[string][]string{
		"et": {"tony"},
	})
	res := postLogin(t, srv, "shh", map[string]string{"user_id": "stranger"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got struct {
		Memberships []string `json:"memberships"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Memberships) != 0 {
		t.Errorf("unknown user got memberships=%v, want []", got.Memberships)
	}
}

func TestDevMint_RejectsTenantNotInMemberships_403(t *testing.T) {
	// The other half of the gate: someone with a valid X-Dev-Secret
	// (a logged-in user) can no longer mint a token for an arbitrary
	// tenant — only ones their user_id is a member of.
	srv := newServerWithMemberships(t, "shh", map[string][]string{
		"et": {"tony"},
	})
	body, _ := json.Marshal(map[string]any{
		"tenant": "sarah", // et is NOT in sarah
		"user":   "et",
	})
	req := mintRequest(t, srv.URL, "shh", body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d, want 403", res.StatusCode)
	}
}

func TestDevMint_AllowsTenantInMemberships(t *testing.T) {
	srv := newServerWithMemberships(t, "shh", map[string][]string{
		"et": {"tony", "sarah"},
	})
	body, _ := json.Marshal(map[string]any{
		"tenant": "sarah",
		"user":   "et",
	})
	req := mintRequest(t, srv.URL, "shh", body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", res.StatusCode)
	}
}

func TestDevMint_NoMembershipsConfigured_AllowsAny_Backcompat(t *testing.T) {
	// Backcompat: when Memberships is nil (the pre-#9 configuration),
	// /dev/mint must keep its current behavior of accepting any
	// (tenant, user). Otherwise this change breaks every existing
	// deployment that hasn't set DEV_MEMBERSHIPS_JSON yet.
	srv := newServerWithMemberships(t, "shh", nil)
	body, _ := json.Marshal(map[string]any{
		"tenant": "sarah",
		"user":   "et",
	})
	req := mintRequest(t, srv.URL, "shh", body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200 (backcompat: empty memberships = allow any)",
			res.StatusCode)
	}
}
