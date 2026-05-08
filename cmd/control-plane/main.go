// Command control-plane is the auth+tenant control plane for Apex's
// multi-tenant cloud deployment. Tenant backends point their APEX_JWKS_URL
// at this service; the SPA (eventually) hits it for login + tenant
// resolution + token exchange.
//
// Phase 2C skeleton — only /healthz, /jwks, and a guarded /dev/mint are
// implemented. Real flows (WorkOS callback, /exchange, /signup with
// invite codes, refresh-cookie management) land in 2C.2.
//
// Configuration (env vars):
//
//   CONTROL_PLANE_PORT          listen port, default 9001
//   CONTROL_PLANE_ISSUER        iss claim baked into tokens, default
//                               http://localhost:<port>
//   APEX_DEV_SECRET             shared secret for /dev/mint; if unset,
//                               the dev-mint endpoint returns 503
//
// Gmail OAuth (Phase 2E) — all optional; if unset the /oauth/gmail/*
// endpoints return 503 and Gmail integration is unavailable in cloud
// mode (manual paste flow on the tenant still works):
//
//   APEX_GOOGLE_CLIENT_ID       OAuth client id from Google Cloud Console
//   APEX_GOOGLE_CLIENT_SECRET   OAuth client secret
//   APEX_GOOGLE_REDIRECT_URI    redirect URI registered with Google;
//                               typically <CONTROL_PLANE_ISSUER>/oauth/gmail/callback
//   APEX_RETURN_URL_PATTERN     optional regex; return URLs in
//                               /oauth/gmail/init bodies must match.
//                               Production: ^https://[a-z0-9-]+\.apex\.io/.*$
//
// The signing key is generated fresh on every start (ephemeral). Tenant
// backends must therefore re-fetch JWKS after a control plane restart;
// the keyfunc/v3 cache refreshes hourly by default. Persistent keys are
// a 2C.2 follow-up.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"

	"github.com/everydev1618/govega/internal/authmint"
)

func main() {
	port := getenv("CONTROL_PLANE_PORT", "9001")
	issuer := os.Getenv("CONTROL_PLANE_ISSUER")
	if issuer == "" {
		issuer = "http://localhost:" + port
	}

	signer, err := authmint.NewSigner()
	if err != nil {
		slog.Error("authmint", "err", err)
		os.Exit(1)
	}

	cfg := Config{
		Signer:             signer,
		Issuer:             issuer,
		DevSecret:          os.Getenv("APEX_DEV_SECRET"),
		GoogleClientID:     os.Getenv("APEX_GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("APEX_GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:  os.Getenv("APEX_GOOGLE_REDIRECT_URI"),
		GoogleAuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		GoogleTokenURL:     "https://oauth2.googleapis.com/token",
	}
	if cfg.GoogleRedirectURI == "" && cfg.GoogleClientID != "" {
		cfg.GoogleRedirectURI = issuer + "/oauth/gmail/callback"
	}
	if pat := os.Getenv("APEX_RETURN_URL_PATTERN"); pat != "" {
		re, err := regexp.Compile(pat)
		if err != nil {
			slog.Error("APEX_RETURN_URL_PATTERN compile", "err", err)
			os.Exit(1)
		}
		cfg.ReturnURLPattern = re
	}

	if cfg.DevSecret == "" {
		slog.Warn("APEX_DEV_SECRET unset — /dev/mint endpoint will return 503")
	}
	if cfg.GoogleClientID == "" {
		slog.Warn("APEX_GOOGLE_CLIENT_ID unset — /oauth/gmail/* endpoints will return 503")
	}
	if cfg.ReturnURLPattern == nil {
		slog.Warn("APEX_RETURN_URL_PATTERN unset — /oauth/gmail/init accepts any return URL (dev-only behavior)")
	}

	addr := ":" + port
	slog.Info("control plane starting", "addr", addr, "issuer", issuer, "kid", signer.KeyID())
	fmt.Printf("JWKS:    http://localhost:%s/jwks\n", port)
	fmt.Printf("Healthz: http://localhost:%s/healthz\n", port)
	if cfg.DevSecret != "" {
		fmt.Printf("DevMint: http://localhost:%s/dev/mint  (X-Dev-Secret: ***)\n", port)
	}
	if cfg.GoogleClientID != "" {
		fmt.Printf("GmailInit:     POST http://localhost:%s/oauth/gmail/init\n", port)
		fmt.Printf("GmailCallback: GET  %s\n", cfg.GoogleRedirectURI)
	}

	cors := loadCORSConfig()
	if len(cors) == 0 {
		slog.Warn("CONTROL_PLANE_ALLOWED_ORIGINS unset — cross-origin SPA requests will be blocked by the browser")
	}

	if err := http.ListenAndServe(addr, corsMiddleware(cors)(newHandler(cfg))); err != nil {
		slog.Error("listen", "err", err)
		os.Exit(1)
	}
}

func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
