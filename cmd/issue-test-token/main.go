// Command issue-test-token mints a signed JWT and serves a matching JWKS so
// govega's auth middleware can be exercised end-to-end without WorkOS.
//
// Workflow:
//
//   1. Run this command in one terminal:
//        go run ./cmd/issue-test-token
//      It prints env vars + the token, then keeps a JWKS server running.
//
//   2. In another terminal, source the env and start your apex backend:
//        eval "$(go run ./cmd/issue-test-token --quiet)"   # or copy/paste
//        APEX_TENANT_ID=$APEX_TENANT_ID make run
//
//   3. Curl with the token:
//        curl -H "Authorization: Bearer $TEST_TOKEN" http://localhost:8080/api/v1/stats
//
// Use --tenant, --user, --ttl, --port to vary claims and listen address.
//
// This is a development aid, not a production tool. The signing key is
// generated fresh on every run and discarded on shutdown.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const kid = "test-key"

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

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("rsa keygen: %v", err)
	}

	issuer := fmt.Sprintf("http://localhost:%d", *port)
	jwksURL := issuer + "/jwks"

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   issuer,
		"aud":   *tenant,
		"sub":   *user,
		"exp":   time.Now().Add(*ttl).Unix(),
		"iat":   time.Now().Unix(),
		"scope": *scope,
	})
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(priv)
	if err != nil {
		log.Fatalf("sign: %v", err)
	}

	jwks, err := buildJWKS(&priv.PublicKey, kid)
	if err != nil {
		log.Fatalf("build jwks: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	})

	emitEnv(*quiet, *tenant, issuer, jwksURL, signed, *port)

	addr := fmt.Sprintf(":%d", *port)
	if err := http.ListenAndServe(addr, mux); err != nil {
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

// buildJWKS encodes an RSA public key into a single-entry JWKS document
// (RFC 7517). Modulus and exponent are base64url-encoded without padding.
func buildJWKS(pub *rsa.PublicKey, kid string) ([]byte, error) {
	doc := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	return json.MarshalIndent(doc, "", "  ")
}
