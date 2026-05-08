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
//   CONTROL_PLANE_PORT      listen port, default 9001
//   CONTROL_PLANE_ISSUER    iss claim baked into tokens, default
//                           http://localhost:<port>
//   APEX_DEV_SECRET         shared secret for /dev/mint; if unset, the
//                           dev-mint endpoint returns 503
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
		Signer:    signer,
		Issuer:    issuer,
		DevSecret: os.Getenv("APEX_DEV_SECRET"),
	}

	if cfg.DevSecret == "" {
		slog.Warn("APEX_DEV_SECRET unset — /dev/mint endpoint will return 503")
	}

	addr := ":" + port
	slog.Info("control plane starting", "addr", addr, "issuer", issuer, "kid", signer.KeyID())
	fmt.Printf("JWKS:    http://localhost:%s/jwks\n", port)
	fmt.Printf("Healthz: http://localhost:%s/healthz\n", port)
	if cfg.DevSecret != "" {
		fmt.Printf("DevMint: http://localhost:%s/dev/mint  (X-Dev-Secret: ***)\n", port)
	}

	if err := http.ListenAndServe(addr, newHandler(cfg)); err != nil {
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
