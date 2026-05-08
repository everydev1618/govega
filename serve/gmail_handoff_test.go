package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// withAuth returns a context carrying validated AuthClaims, simulating
// what the auth middleware injects for /api/v1/* requests.
func withAuth(ctx context.Context, claims AuthClaims) context.Context {
	return context.WithValue(ctx, authContextKey{}, claims)
}

func newGmailServer(t *testing.T, tenantID string) (*Server, *testAuthFixture) {
	t.Helper()
	f := newTestAuth(t, tenantID)
	s := &Server{
		store:           newOAuthStore(t),
		authCfg:         f.cfg,
		controlPlaneURL: "", // tests set per-case
		consumedJTIs:    sync.Map{},
	}
	return s, f
}

// --- handleGmailIntegrationStart -----------------------------------------

func TestGmailIntegrationStart_ProxiesToControlPlane(t *testing.T) {
	s, _ := newGmailServer(t, "acme")

	// Stub the control plane: assert what the tenant POSTs to /oauth/gmail/init
	// and return a canned authorize_url response.
	var receivedAuthHeader, receivedTenant, receivedReturn string
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		var body struct {
			Tenant string `json:"tenant"`
			Return string `json:"return"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		receivedTenant = body.Tenant
		receivedReturn = body.Return
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"authorize_url":"https://accounts.google.com/o/oauth2/v2/auth?stub=1"}`)
	}))
	defer cp.Close()
	s.controlPlaneURL = cp.URL

	body, _ := json.Marshal(map[string]string{"return": "https://acme.apex.io/integrations"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/start", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer the-user-token")
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "user_1", TenantID: "acme"}))

	rec := httptest.NewRecorder()
	s.handleGmailIntegrationStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rec.Code, rec.Body.String())
	}
	if receivedAuthHeader != "Bearer the-user-token" {
		t.Errorf("forwarded auth=%q, want \"Bearer the-user-token\"", receivedAuthHeader)
	}
	if receivedTenant != "acme" {
		t.Errorf("tenant=%q, want acme", receivedTenant)
	}
	if receivedReturn != "https://acme.apex.io/integrations" {
		t.Errorf("return=%q", receivedReturn)
	}
	if !strings.Contains(rec.Body.String(), "authorize_url") {
		t.Errorf("response missing authorize_url: %s", rec.Body.String())
	}
}

func TestGmailIntegrationStart_ControlPlaneNotConfigured_503(t *testing.T) {
	s, _ := newGmailServer(t, "acme")
	// controlPlaneURL is empty.

	body, _ := json.Marshal(map[string]string{"return": "https://acme.apex.io/x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/start", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "u1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationStart(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rec.Code)
	}
}

func TestGmailIntegrationStart_ControlPlaneError_PropagatesStatus(t *testing.T) {
	s, _ := newGmailServer(t, "acme")
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"return URL not allowed"}`, http.StatusBadRequest)
	}))
	defer cp.Close()
	s.controlPlaneURL = cp.URL

	body, _ := json.Marshal(map[string]string{"return": "https://attacker.example/x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/start", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "u1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationStart(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (propagated from control plane)", rec.Code)
	}
}

func TestGmailIntegrationStart_BadJSON_400(t *testing.T) {
	s, _ := newGmailServer(t, "acme")
	s.controlPlaneURL = "http://does-not-matter"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/start", strings.NewReader("{not json"))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "u1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationStart(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

// --- handleGmailIntegrationHandoff ---------------------------------------

// mintHandoff produces a control-plane-style handoff JWT signed by the
// fixture's key. Caller can override claims by mutating the returned map
// before signing.
func mintHandoff(t *testing.T, f *testAuthFixture, claims jwt.MapClaims) string {
	t.Helper()
	defaults := jwt.MapClaims{
		"iss":                 "https://auth.test",
		"aud":                 "acme",
		"sub":                 "user_1",
		"purpose":             "gmail-handoff",
		"gmail_refresh_token": "fake-refresh",
		"gmail_access_token":  "fake-access",
		"gmail_expiry":        time.Now().Add(time.Hour).Unix(),
		"iat":                 time.Now().Unix(),
		"exp":                 time.Now().Add(time.Minute).Unix(),
		"jti":                 "j-default",
	}
	for k, v := range claims {
		defaults[k] = v
	}
	return f.mintToken(t, defaults)
}

func TestGmailIntegrationHandoff_PersistsRefreshToken(t *testing.T) {
	s, f := newGmailServer(t, "acme")
	handoff := mintHandoff(t, f, jwt.MapClaims{"sub": "user_1", "jti": "j1"})

	body, _ := json.Marshal(map[string]string{"handoff_token": handoff})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "user_1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rec.Code, rec.Body.String())
	}
	stored, err := s.store.GetSetting(gmailRefreshTokenSettingKey)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if stored == nil || stored.Value != "fake-refresh" {
		t.Errorf("refresh token not persisted: %+v", stored)
	}
}

func TestGmailIntegrationHandoff_BadSignature_400(t *testing.T) {
	s, _ := newGmailServer(t, "acme")
	// Token signed by a DIFFERENT key — should fail Verify.
	other := newTestAuth(t, "acme")
	bad := mintHandoff(t, other, nil)

	body, _ := json.Marshal(map[string]string{"handoff_token": bad})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "user_1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (foreign signature)", rec.Code)
	}
}

func TestGmailIntegrationHandoff_WrongPurpose_400(t *testing.T) {
	s, f := newGmailServer(t, "acme")
	bad := mintHandoff(t, f, jwt.MapClaims{"purpose": "user-access"})

	body, _ := json.Marshal(map[string]string{"handoff_token": bad})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "user_1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (wrong purpose)", rec.Code)
	}
}

func TestGmailIntegrationHandoff_SubjectMismatch_403(t *testing.T) {
	// Handoff was issued for user_2 but request is authenticated as user_1.
	// 403 distinct from 400 so logs distinguish auth violations.
	s, f := newGmailServer(t, "acme")
	tok := mintHandoff(t, f, jwt.MapClaims{"sub": "user_2"})

	body, _ := json.Marshal(map[string]string{"handoff_token": tok})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "user_1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", rec.Code)
	}
}

func TestGmailIntegrationHandoff_Replay_Rejected(t *testing.T) {
	s, f := newGmailServer(t, "acme")
	tok := mintHandoff(t, f, jwt.MapClaims{"jti": "j-replay"})

	body, _ := json.Marshal(map[string]string{"handoff_token": tok})
	auth := AuthClaims{UserID: "user_1", TenantID: "acme"}

	// First use succeeds.
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req1 = req1.WithContext(withAuth(req1.Context(), auth))
	rec1 := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first use status=%d", rec1.Code)
	}

	// Second use of the SAME jti must fail.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req2 = req2.WithContext(withAuth(req2.Context(), auth))
	rec2 := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("replay status=%d, want 400", rec2.Code)
	}
}

func TestGmailIntegrationHandoff_MissingRefreshToken_400(t *testing.T) {
	s, f := newGmailServer(t, "acme")
	tok := mintHandoff(t, f, jwt.MapClaims{"gmail_refresh_token": ""})
	body, _ := json.Marshal(map[string]string{"handoff_token": tok})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", bytes.NewReader(body))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "user_1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestGmailIntegrationHandoff_BadJSON_400(t *testing.T) {
	s, _ := newGmailServer(t, "acme")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/gmail/handoff", strings.NewReader("{not json"))
	req = req.WithContext(withAuth(req.Context(), AuthClaims{UserID: "u1", TenantID: "acme"}))
	rec := httptest.NewRecorder()
	s.handleGmailIntegrationHandoff(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}
