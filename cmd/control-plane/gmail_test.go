package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/everydev1618/govega/internal/authmint"
	"github.com/golang-jwt/jwt/v5"
)

// fakeGoogle returns a httptest.Server that mocks Google's OAuth token
// endpoint. By default it returns a successful refresh-token response;
// tests can mutate respStatus / respBody to simulate failures.
type fakeGoogle struct {
	*httptest.Server
	receivedForm url.Values
	respStatus   int
	respBody     map[string]any
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{
		respStatus: http.StatusOK,
		respBody: map[string]any{
			"access_token":  "fake-access",
			"refresh_token": "fake-refresh",
			"expires_in":    3600,
			"token_type":    "Bearer",
			"scope":         "https://www.googleapis.com/auth/gmail.modify",
		},
	}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.receivedForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(g.respStatus)
		_ = json.NewEncoder(w).Encode(g.respBody)
	}))
	t.Cleanup(g.Server.Close)
	return g
}

// newGmailTestServer wires a control plane configured for Gmail OAuth
// against a fake Google. Returns the control plane URL, the signer (so
// tests can mint tokens), and the fake Google so tests can flip its
// response.
func newGmailTestServer(t *testing.T) (*httptest.Server, *authmint.Signer, *fakeGoogle) {
	t.Helper()
	signer, err := authmint.NewSigner()
	if err != nil {
		t.Fatalf("authmint: %v", err)
	}
	g := newFakeGoogle(t)
	cfg := Config{
		Signer:                signer,
		Issuer:                "http://control-plane.test",
		GoogleClientID:        "client-id-fake",
		GoogleClientSecret:    "client-secret-fake",
		GoogleRedirectURI:     "http://control-plane.test/oauth/gmail/callback",
		GoogleAuthURL:         "http://google.test/o/oauth2/v2/auth",
		GoogleTokenURL:        g.URL,
		ReturnURLPattern:      regexp.MustCompile(`^http://acme\.tenant\.test/.*$`),
	}
	srv := httptest.NewServer(newHandler(cfg))
	t.Cleanup(srv.Close)
	return srv, signer, g
}

// mintUserToken mints a control-plane-signed access token for the given
// tenant + user, simulating what the SPA would carry.
func mintUserToken(t *testing.T, signer *authmint.Signer, issuer, tenant, user string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.MapClaims{
		"iss":   issuer,
		"aud":   tenant,
		"sub":   user,
		"exp":   time.Now().Add(time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"scope": "read write",
	})
	if err != nil {
		t.Fatalf("mint user token: %v", err)
	}
	return tok
}

// mustDo is a tiny helper so test bodies don't shadow err for every
// http.DefaultClient.Do call. t.Fatal on transport errors.
func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	return res
}

func mustGet(t *testing.T, cli *http.Client, url string) *http.Response {
	t.Helper()
	res, err := cli.Get(url)
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	return res
}

// initRequest builds an /oauth/gmail/init request body with the given
// tenant/return + Authorization header.
func initRequest(t *testing.T, baseURL, userTok string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/gmail/init", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userTok)
	return req
}

func TestGmailInit_Success_ReturnsAuthorizeURL(t *testing.T) {
	srv, signer, _ := newGmailTestServer(t)
	tok := mintUserToken(t, signer, "http://control-plane.test", "acme", "user_1")

	body, _ := json.Marshal(map[string]any{
		"tenant": "acme",
		"return": "http://acme.tenant.test/integrations/gmail",
	})
	req := initRequest(t, srv.URL, tok, body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d, body=%s", res.StatusCode, string(respBody))
	}
	var got struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(got.AuthorizeURL, "http://google.test/o/oauth2/v2/auth?") {
		t.Errorf("authorize_url=%q does not start with Google auth URL", got.AuthorizeURL)
	}
	u, _ := url.Parse(got.AuthorizeURL)
	q := u.Query()
	if q.Get("client_id") != "client-id-fake" {
		t.Errorf("client_id=%q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://control-plane.test/oauth/gmail/callback" {
		t.Errorf("redirect_uri=%q", q.Get("redirect_uri"))
	}
	if q.Get("scope") == "" || !strings.Contains(q.Get("scope"), "gmail.modify") {
		t.Errorf("scope missing gmail.modify: %q", q.Get("scope"))
	}
	if q.Get("state") == "" {
		t.Errorf("state missing")
	}
	if q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
		t.Errorf("missing access_type=offline or prompt=consent")
	}
}

func TestGmailInit_NoGoogleConfig_503(t *testing.T) {
	signer, _ := authmint.NewSigner()
	cfg := Config{Signer: signer, Issuer: "http://cp.test"} // no Google fields
	srv := httptest.NewServer(newHandler(cfg))
	defer srv.Close()
	tok := mintUserToken(t, signer, "http://cp.test", "acme", "u1")
	body, _ := json.Marshal(map[string]any{"tenant": "acme", "return": "http://acme.tenant.test/x"})
	req := initRequest(t, srv.URL, tok, body)
	res := mustDo(t, req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", res.StatusCode)
	}
}

func TestGmailInit_MissingAuth_401(t *testing.T) {
	srv, _, _ := newGmailTestServer(t)
	body, _ := json.Marshal(map[string]any{"tenant": "acme", "return": "http://acme.tenant.test/x"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/oauth/gmail/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := mustDo(t, req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", res.StatusCode)
	}
}

func TestGmailInit_TenantMismatch_403(t *testing.T) {
	// Token is for tenant "acme" but request asks for "globex". Defense
	// against tenant impersonation via crafted body.
	srv, signer, _ := newGmailTestServer(t)
	tok := mintUserToken(t, signer, "http://control-plane.test", "acme", "u1")
	body, _ := json.Marshal(map[string]any{"tenant": "globex", "return": "http://acme.tenant.test/x"})
	req := initRequest(t, srv.URL, tok, body)
	res := mustDo(t, req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d, want 403", res.StatusCode)
	}
}

func TestGmailInit_BadReturnURL_400(t *testing.T) {
	srv, signer, _ := newGmailTestServer(t)
	tok := mintUserToken(t, signer, "http://control-plane.test", "acme", "u1")
	body, _ := json.Marshal(map[string]any{"tenant": "acme", "return": "https://attacker.example/steal"})
	req := initRequest(t, srv.URL, tok, body)
	res := mustDo(t, req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (return URL not allowlisted)", res.StatusCode)
	}
}

func TestGmailInit_BadJSON_400(t *testing.T) {
	srv, signer, _ := newGmailTestServer(t)
	tok := mintUserToken(t, signer, "http://control-plane.test", "acme", "u1")
	req := initRequest(t, srv.URL, tok, []byte("{not json"))
	res := mustDo(t, req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}

// callbackFlow runs init→extract state→callback in one go and returns the
// 302 response from the callback. Used by happy-path and failure tests.
func callbackFlow(t *testing.T, srv *httptest.Server, signer *authmint.Signer) (state string) {
	t.Helper()
	tok := mintUserToken(t, signer, "http://control-plane.test", "acme", "user_42")
	body, _ := json.Marshal(map[string]any{
		"tenant": "acme",
		"return": "http://acme.tenant.test/integrations/gmail",
	})
	req := initRequest(t, srv.URL, tok, body)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("init status=%d", res.StatusCode)
	}
	var got struct{ AuthorizeURL string `json:"authorize_url"` }
	_ = json.NewDecoder(res.Body).Decode(&got)
	u, _ := url.Parse(got.AuthorizeURL)
	return u.Query().Get("state")
}

func TestGmailCallback_Success_RedirectsWithHandoffToken(t *testing.T) {
	srv, signer, _ := newGmailTestServer(t)
	state := callbackFlow(t, srv, signer)

	// Browser hits callback with code + state
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // don't follow
	}}
	cbURL := srv.URL + "/oauth/gmail/callback?code=fake-auth-code&state=" + url.QueryEscape(state)
	res, err := cli.Get(cbURL)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d, want 302; body=%s", res.StatusCode, string(body))
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "http://acme.tenant.test/integrations/gmail") {
		t.Fatalf("Location=%q does not start with return URL", loc)
	}
	u, _ := url.Parse(loc)
	handoff := u.Query().Get("gmail_handoff")
	if handoff == "" {
		t.Fatalf("Location missing gmail_handoff query param: %q", loc)
	}
	// Verify the handoff JWT is well-formed and contains the expected claims.
	parsed, err := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"})).Parse(handoff, signer.Keyfunc())
	if err != nil {
		t.Fatalf("parse handoff: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["aud"] != "acme" || claims["sub"] != "user_42" {
		t.Errorf("handoff claims: %v", claims)
	}
	if claims["purpose"] != "gmail-handoff" {
		t.Errorf("purpose=%v, want gmail-handoff", claims["purpose"])
	}
	if claims["gmail_refresh_token"] != "fake-refresh" {
		t.Errorf("gmail_refresh_token=%v, want fake-refresh", claims["gmail_refresh_token"])
	}
	if claims["jti"] == "" {
		t.Errorf("jti missing")
	}
}

func TestGmailCallback_BadState_400(t *testing.T) {
	srv, _, _ := newGmailTestServer(t)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res := mustGet(t, cli, srv.URL+"/oauth/gmail/callback?code=x&state=not-a-jwt")
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}

func TestGmailCallback_TokenExchangeFails_502(t *testing.T) {
	srv, signer, fake := newGmailTestServer(t)
	fake.respStatus = http.StatusBadRequest
	fake.respBody = map[string]any{"error": "invalid_grant"}
	state := callbackFlow(t, srv, signer)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res := mustGet(t, cli, srv.URL+"/oauth/gmail/callback?code=x&state="+url.QueryEscape(state))
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("status=%d, want 502 (Google rejection)", res.StatusCode)
	}
}

func TestGmailCallback_NoRefreshToken_502(t *testing.T) {
	srv, signer, fake := newGmailTestServer(t)
	fake.respBody = map[string]any{
		"access_token": "fake-access",
		// refresh_token deliberately omitted
		"expires_in": 3600,
	}
	state := callbackFlow(t, srv, signer)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res := mustGet(t, cli, srv.URL+"/oauth/gmail/callback?code=x&state="+url.QueryEscape(state))
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("status=%d, want 502 (no refresh token)", res.StatusCode)
	}
}

func TestGmailCallback_MissingParams_400(t *testing.T) {
	srv, _, _ := newGmailTestServer(t)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res := mustGet(t, cli, srv.URL+"/oauth/gmail/callback")
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}
