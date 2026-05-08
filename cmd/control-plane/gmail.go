package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Gmail OAuth handlers for the control plane.
//
// Two endpoints, both return 503 when GoogleClientID is unset (the
// control plane is happily running without Gmail integration configured):
//
//   POST /oauth/gmail/init        called by tenant backend; returns the
//                                 Google authorize URL with a signed
//                                 state token.
//   GET  /oauth/gmail/callback    Google's redirect target; exchanges the
//                                 code, mints a 60-second handoff JWT,
//                                 302s the user to <return>?gmail_handoff=<jwt>.
//
// State JWT (purpose=gmail-state) carries tenant + user + return URL
// across the Google round-trip. Handoff JWT (purpose=gmail-handoff)
// carries the refresh token from control plane to tenant backend with a
// jti so the tenant can refuse replays.

const (
	gmailScope        = "https://www.googleapis.com/auth/gmail.modify"
	gmailStateTTL     = 5 * time.Minute
	gmailHandoffTTL   = 60 * time.Second
	gmailStatePurpose = "gmail-state"
)

// gmailInitHandler validates the user's access JWT, validates the body's
// return URL against the configured allowlist, and signs a state JWT
// that the callback can later verify.
func gmailInitHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" {
			http.Error(w, "gmail oauth not configured (set APEX_GOOGLE_CLIENT_ID + APEX_GOOGLE_CLIENT_SECRET)", http.StatusServiceUnavailable)
			return
		}

		userClaims, err := verifyUserToken(cfg, r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			Tenant string `json:"tenant"`
			Return string `json:"return"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.Tenant == "" || req.Return == "" {
			http.Error(w, "tenant and return are required", http.StatusBadRequest)
			return
		}

		// Token's aud must match the requested tenant — defends against a
		// caller forging a body for a tenant they don't control.
		if claimsString(userClaims, "aud") != req.Tenant {
			http.Error(w, "tenant mismatch", http.StatusForbidden)
			return
		}

		if cfg.ReturnURLPattern != nil && !cfg.ReturnURLPattern.MatchString(req.Return) {
			http.Error(w, "return URL not allowed", http.StatusBadRequest)
			return
		}

		userID := claimsString(userClaims, "sub")
		state, err := cfg.Signer.Sign(jwt.MapClaims{
			"iss":     cfg.Issuer,
			"aud":     req.Tenant,
			"sub":     userID,
			"purpose": gmailStatePurpose,
			"return":  req.Return,
			"iat":     time.Now().Unix(),
			"exp":     time.Now().Add(gmailStateTTL).Unix(),
			"jti":     randomID(),
		})
		if err != nil {
			http.Error(w, "sign state", http.StatusInternalServerError)
			return
		}

		q := url.Values{
			"client_id":     {cfg.GoogleClientID},
			"redirect_uri":  {cfg.GoogleRedirectURI},
			"response_type": {"code"},
			"scope":         {gmailScope},
			"access_type":   {"offline"},
			"prompt":        {"consent"},
			"state":         {state},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorize_url": cfg.GoogleAuthURL + "?" + q.Encode(),
		})
	}
}

// gmailCallbackHandler validates the state, exchanges the code with
// Google, and 302s the user back to the return URL with a handoff JWT.
func gmailCallbackHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" {
			http.Error(w, "gmail oauth not configured", http.StatusServiceUnavailable)
			return
		}

		code := r.URL.Query().Get("code")
		stateRaw := r.URL.Query().Get("state")
		if code == "" || stateRaw == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}

		stateClaims, err := verifyStateToken(cfg, stateRaw)
		if err != nil {
			http.Error(w, "invalid or expired state", http.StatusBadRequest)
			return
		}

		tenant := claimsString(stateClaims, "aud")
		user := claimsString(stateClaims, "sub")
		returnURL := claimsString(stateClaims, "return")

		tok, err := exchangeCodeWithGoogle(cfg, r, code)
		if err != nil {
			slog.Error("gmail oauth: token exchange", "err", err)
			http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if tok.RefreshToken == "" {
			http.Error(w, "Google did not return a refresh_token; the user may have a prior grant — revoke at https://myaccount.google.com/permissions and retry", http.StatusBadGateway)
			return
		}

		handoff, err := cfg.Signer.Sign(jwt.MapClaims{
			"iss":                 cfg.Issuer,
			"aud":                 tenant,
			"sub":                 user,
			"purpose":             "gmail-handoff",
			"gmail_refresh_token": tok.RefreshToken,
			"gmail_access_token":  tok.AccessToken,
			"gmail_expiry":        time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
			"iat":                 time.Now().Unix(),
			"exp":                 time.Now().Add(gmailHandoffTTL).Unix(),
			"jti":                 randomID(),
		})
		if err != nil {
			http.Error(w, "sign handoff", http.StatusInternalServerError)
			return
		}

		dest, err := url.Parse(returnURL)
		if err != nil {
			http.Error(w, "bad return URL in state", http.StatusInternalServerError)
			return
		}
		q := dest.Query()
		q.Set("gmail_handoff", handoff)
		dest.RawQuery = q.Encode()

		http.Redirect(w, r, dest.String(), http.StatusFound)
	}
}

// googleTokenResponse is the subset of fields we care about from the OAuth
// token endpoint.
type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

func exchangeCodeWithGoogle(cfg Config, r *http.Request, code string) (*googleTokenResponse, error) {
	form := url.Values{
		"client_id":     {cfg.GoogleClientID},
		"client_secret": {cfg.GoogleClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.GoogleRedirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, cfg.GoogleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Google returned %d: %s", resp.StatusCode, string(body))
	}
	var tok googleTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &tok, nil
}

// verifyUserToken extracts and validates the user's access JWT from the
// Authorization header. iss must match cfg.Issuer; sig is checked against
// the signer's own public key (the control plane is the issuer of these
// tokens).
func verifyUserToken(cfg Config, r *http.Request) (jwt.MapClaims, error) {
	const scheme = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return nil, fmt.Errorf("missing or malformed Authorization header")
	}
	raw := strings.TrimSpace(h[len(scheme):])
	parser := jwt.NewParser(
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
	)
	parsed, err := parser.Parse(raw, cfg.Signer.Keyfunc())
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid claims")
	}
	return claims, nil
}

// verifyStateToken validates a state JWT — same signer, with
// purpose=gmail-state required. Audience is the tenant id (set by init);
// we don't pin it here because the callback doesn't know the tenant
// up-front (it's recovered from the validated claims).
func verifyStateToken(cfg Config, raw string) (jwt.MapClaims, error) {
	parser := jwt.NewParser(
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
	)
	parsed, err := parser.Parse(raw, cfg.Signer.Keyfunc())
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid claims")
	}
	if claimsString(claims, "purpose") != gmailStatePurpose {
		return nil, fmt.Errorf("wrong purpose")
	}
	return claims, nil
}

func claimsString(c jwt.MapClaims, k string) string {
	v, _ := c[k].(string)
	return v
}

// randomID returns a URL-safe random identifier suitable for jti claims.
func randomID() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}
