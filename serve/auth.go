// Package serve — auth.go
//
// JWT auth middleware for tenant backends. Validates access tokens issued by
// the WorkOS-backed control plane against this tenant's expected audience.
//
// Two modes, toggled by whether AuthConfig.TenantID is set:
//
//   - Self-hosted (TenantID empty): middleware is a pass-through. Preserves
//     the existing single-operator monolith experience.
//   - Cloud (TenantID set): every /api/v1/* request must carry a valid bearer
//     JWT signed by Issuer's JWKS, with aud == TenantID. Other paths
//     (SPA, /workspace, /healthz, /integrations/gmail/handoff) pass through.
//
// The middleware never calls the IdP per request — the caller wires up a
// JWKS-cached jwt.Keyfunc (production: github.com/MicahParks/keyfunc/v3;
// tests: an inline closure over a static RSA key).

package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/everydev1618/govega/internal/envcompat"
)

// LoadAuthConfig builds an AuthConfig from environment. VEGA_TENANT_ID empty
// => self-hosted pass-through (zero-valued config). Otherwise VEGA_JWT_ISSUER
// and VEGA_JWKS_URL are required.
//
// The legacy APEX_-prefixed names (APEX_TENANT_ID, APEX_JWT_ISSUER, APEX_JWKS_URL)
// are still honored as a fallback, with a one-time deprecation warning per
// process so existing apex deployments keep working while operators migrate.
//
// The Keyfunc is backed by MicahParks/keyfunc/v3, which auto-refreshes the
// JWKS in the background; pass ctx to bound its lifetime to the server.
func LoadAuthConfig(ctx context.Context) (AuthConfig, error) {
	tenantID := envcompat.Get("VEGA_TENANT_ID")
	if tenantID == "" {
		return AuthConfig{}, nil
	}
	issuer := envcompat.Get("VEGA_JWT_ISSUER")
	jwksURL := envcompat.Get("VEGA_JWKS_URL")
	if issuer == "" || jwksURL == "" {
		return AuthConfig{}, fmt.Errorf("VEGA_TENANT_ID is set; VEGA_JWT_ISSUER and VEGA_JWKS_URL are required")
	}
	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return AuthConfig{}, fmt.Errorf("init JWKS: %w", err)
	}
	return AuthConfig{
		Issuer:   issuer,
		TenantID: tenantID,
		Keyfunc:  k.Keyfunc,
	}, nil
}

// AuthConfig is consumed by authMiddleware. Issuer + TenantID match the iss
// and aud claims; Keyfunc returns the public key for signature verification.
type AuthConfig struct {
	// Issuer is the expected `iss` claim (WorkOS issuer URL in production).
	Issuer string
	// TenantID is the expected `aud` claim. Empty disables auth (self-hosted).
	TenantID string
	// Keyfunc supplies the public key for signature verification. In
	// production this is a JWKS-backed Keyfunc; in tests it can return a
	// fixed *rsa.PublicKey.
	Keyfunc jwt.Keyfunc
}

// AuthClaims is the validated identity injected into request context.
// Handlers retrieve it via ClaimsFrom(r.Context()).
type AuthClaims struct {
	UserID   string
	TenantID string
	Scopes   []string
}

type authContextKey struct{}

// ClaimsFrom returns the validated claims attached to ctx, if any. ok==false
// means the request did not pass through authMiddleware (or the middleware
// was in self-hosted no-auth mode).
func ClaimsFrom(ctx context.Context) (AuthClaims, bool) {
	c, ok := ctx.Value(authContextKey{}).(AuthClaims)
	return c, ok
}

// WithClaims attaches AuthClaims to ctx using the same key authMiddleware
// uses. Provided so products that authenticate users outside the JWT
// middleware (e.g. a trusted reverse proxy that has already verified a
// session cookie) can inject identity that downstream handlers consume
// uniformly via ClaimsFrom.
//
// Callers are responsible for verifying the claim — govega does no
// additional validation here.
func WithClaims(ctx context.Context, c AuthClaims) context.Context {
	return context.WithValue(ctx, authContextKey{}, c)
}

type bearerContextKey struct{}

// BearerTokenFrom returns the raw Authorization bearer token the
// auth middleware validated for this request, if any. Tools that
// need to call back into the user's own product as that user (e.g.
// a save_chapter MCP tool calling the product's REST API) read the
// token here and pass it through, so identity remains a single JWT
// for the duration of the chat turn. Returns "" when no middleware
// stashed a token (self-hosted mode, or a code path that never
// touched authMiddleware).
func BearerTokenFrom(ctx context.Context) string {
	v, _ := ctx.Value(bearerContextKey{}).(string)
	return v
}

// WithBearerToken attaches a raw bearer token to ctx. Exposed for
// product middleware that authenticates by means other than the
// bundled JWT path but still wants downstream tools to be able to
// call back with the same credential.
func WithBearerToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerContextKey{}, token)
}

// Verify validates a JWT against this AuthConfig's issuer/audience/key
// expectations and returns its claims. It is the seam for handlers that
// live outside the /api/v1 middleware (e.g. the Gmail OAuth handoff,
// which arrives via redirect with a query-param token).
//
// requirePurpose, if non-empty, additionally requires the token's
// "purpose" claim to match exactly. Pass "" to skip the check (e.g. for
// plain access-token validation).
//
// Returns an error on any validation failure, including when called on a
// zero-valued AuthConfig (self-hosted mode) — callers must not treat an
// unconfigured Verify as success.
func (cfg AuthConfig) Verify(token, requirePurpose string) (jwt.MapClaims, error) {
	if cfg.TenantID == "" || cfg.Keyfunc == nil {
		return nil, errors.New("auth not configured")
	}
	parser := jwt.NewParser(
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.TenantID),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
	)
	parsed, err := parser.Parse(token, cfg.Keyfunc)
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid claims")
	}
	if requirePurpose != "" {
		got, _ := claims["purpose"].(string)
		if got != requirePurpose {
			return nil, fmt.Errorf("purpose=%q, want %q", got, requirePurpose)
		}
	}
	return claims, nil
}

// authMiddleware enforces JWT auth on /api/v1/* paths. Other paths pass
// through unauthenticated. If cfg.TenantID is empty, the middleware is a
// no-op pass-through — used for self-hosted deployments.
func authMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
	if cfg.TenantID == "" {
		return func(next http.Handler) http.Handler { return next }
	}

	parser := jwt.NewParser(
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.TenantID),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			raw := bearerFrom(r.Header.Get("Authorization"))
			if raw == "" {
				// EventSource can't send custom headers; allow the token via
				// ?access_token=... per RFC 6750 §2.3. Header-bearer is preferred
				// when present.
				raw = r.URL.Query().Get("access_token")
			}
			if raw == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}

			parsed, err := parser.Parse(raw, cfg.Keyfunc)
			if err != nil {
				if errors.Is(err, jwt.ErrTokenInvalidAudience) {
					http.Error(w, "wrong tenant", http.StatusForbidden)
					return
				}
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			claims, ok := parsed.Claims.(jwt.MapClaims)
			if !ok || !parsed.Valid {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			ac := AuthClaims{
				UserID:   claimString(claims, "sub"),
				TenantID: cfg.TenantID,
				Scopes:   claimScopes(claims),
			}
			ctx := context.WithValue(r.Context(), authContextKey{}, ac)
			ctx = context.WithValue(ctx, bearerContextKey{}, raw)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerFrom extracts the token from an "Authorization: Bearer <token>"
// header, case-insensitive on the scheme. Returns empty if the header is
// missing, malformed, or uses a different scheme.
func bearerFrom(header string) string {
	const scheme = "bearer "
	if len(header) < len(scheme) {
		return ""
	}
	if !strings.EqualFold(header[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(header[len(scheme):])
}

func claimString(m jwt.MapClaims, k string) string {
	v, _ := m[k].(string)
	return v
}

// claimScopes parses an OAuth-style space-separated `scope` claim. WorkOS
// emits this for role/permission claims.
func claimScopes(m jwt.MapClaims) []string {
	s, _ := m["scope"].(string)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}
