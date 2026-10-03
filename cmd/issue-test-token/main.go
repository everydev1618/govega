// Command issue-test-token mints a signed JWT and serves a matching JWKS so
// govega's auth middleware can be exercised end-to-end without WorkOS or
// the full control plane.
//
// Workflow:
//
//  1. Run this command in one terminal:
//     go run ./cmd/issue-test-token
//     It prints env vars + the token, then keeps a JWKS server running.
//
//  2. In another terminal, source the env and start your apex backend:
//     eval "$(go run ./cmd/issue-test-token --quiet | head -3)"
//     export TEST_TOKEN=...    # paste from the helper's stderr output
//     APEX_TENANT_ID=$APEX_TENANT_ID make run -C ../apexvega
//
//  3. Curl with the token:
//     curl -H "Authorization: Bearer $TEST_TOKEN" http://localhost:8080/api/v1/stats
//
// Use --tenant, --user, --ttl, --port to vary claims and listen address.
//
// This is a development aid, not a production tool. The signing key is
// generated fresh on every run and discarded on shutdown. For a long-
// running issuer, see cmd/control-plane.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/everydev1618/govega/internal/authmint"
	"github.com/golang-jwt/jwt/v5"
)

func main() {
	var (
		tenant = flag.String("tenant", "test-tenant", "tenant id (becomes JWT aud and APEX_TENANT_ID)")
		user   = flag.String("user", "user_test", "user id (becomes JWT sub)")
		port   = flag.Int("port", 9999, "JWKS server listen port")
		ttl    = flag.Duration("ttl", time.Hour, "token expiry from now")
		scope  = flag.String("scope", "read write admin", "OAuth-style scope claim, space-separated")
		quiet  = flag.Bool("quiet", false, "emit only export lines on stdout (suitable for `eval`)")
	)
	flag.Parse()

	signer, err := authmint.NewSigner()
	if err != nil {
		log.Fatalf("authmint: %v", err)
	}

	issuer := fmt.Sprintf("http://localhost:%d", *port)
	jwksURL := issuer + "/jwks"

	tok, err := signer.Sign(jwt.MapClaims{
		"iss":   issuer,
		"aud":   *tenant,
		"sub":   *user,
		"exp":   time.Now().Add(*ttl).Unix(),
		"iat":   time.Now().Unix(),
		"scope": *scope,
	})
	if err != nil {
		log.Fatalf("sign: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/jwks", signer.JWKSHandler())

	emitEnv(*quiet, *tenant, issuer, jwksURL, tok, *port)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func emitEnv(quiet bool, tenant, issuer, jwksURL, token string, port int) {
	exports := []string{
		fmt.Sprintf("export APEX_TENANT_ID=%q", tenant),
		fmt.Sprintf("export APEX_JWT_ISSUER=%q", issuer),
		fmt.Sprintf("export APEX_JWKS_URL=%q", jwksURL),
		fmt.Sprintf("export TEST_TOKEN=%q", token),
	}
	if quiet {
		for _, l := range exports {
			fmt.Println(l)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "\n# Source these into your shell:\n")
	for _, l := range exports {
		fmt.Fprintln(os.Stderr, l)
	}
	fmt.Fprintf(os.Stderr, `
# Or curl directly:
curl -H "Authorization: Bearer $TEST_TOKEN" http://localhost:8080/api/v1/stats

JWKS server listening on :%d (Ctrl-C to stop)
`, port)
}
