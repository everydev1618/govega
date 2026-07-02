package serve

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAutoPortAllocation(t *testing.T) {
	// When Addr is empty, resolveAddr should bind to a random free port.
	addr := ""
	ln, resolvedAddr, err := resolveAddr(addr)
	if err != nil {
		t.Fatalf("resolveAddr(%q) error: %v", addr, err)
	}
	defer ln.Close()

	if resolvedAddr == "" || resolvedAddr == ":0" {
		t.Fatalf("expected a resolved address with a port, got %q", resolvedAddr)
	}

	// Verify we got a valid port.
	_, port, err := net.SplitHostPort(resolvedAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error: %v", resolvedAddr, err)
	}
	if port == "0" || port == "" {
		t.Fatalf("expected a non-zero port, got %q", port)
	}
}

func TestEmptyAddrBindsLoopback(t *testing.T) {
	// Security default (P2-1): an unspecified address must bind loopback only,
	// never all interfaces, so self-hosted instances aren't LAN-reachable.
	ln, resolvedAddr, err := resolveAddr("")
	if err != nil {
		t.Fatalf("resolveAddr(\"\") error: %v", err)
	}
	defer ln.Close()

	host, _, err := net.SplitHostPort(resolvedAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", resolvedAddr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Errorf("empty addr bound to %q, want a loopback address", resolvedAddr)
	}
}

func TestExplicitAddr(t *testing.T) {
	// When Addr is provided, resolveAddr should bind to that exact address.
	ln, resolvedAddr, err := resolveAddr(":0") // use :0 so test doesn't clash
	if err != nil {
		t.Fatalf("resolveAddr(%q) error: %v", ":0", err)
	}
	defer ln.Close()

	_, port, err := net.SplitHostPort(resolvedAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error: %v", resolvedAddr, err)
	}
	if port == "0" || port == "" {
		t.Fatalf("expected a real port, got %q", port)
	}
}

// composeMiddleware is what Server.Start uses to stack Config.Middleware
// in front of the mux. The order matters: mw[0] is the outermost wrapper
// so it sees the request first.
func TestComposeMiddleware_EmptyReturnsInner(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("inner"))
	})
	h := composeMiddleware(nil, inner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Body.String(); got != "inner" {
		t.Errorf("body = %q, want %q", got, "inner")
	}
}

func TestComposeMiddleware_OrderIsLeftToRight(t *testing.T) {
	// mw[0] runs first (outermost), so the request appears to be processed
	// as A → B → inner, and the response body unwinds inner → B → A.
	tag := func(label string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("<" + label + ">"))
				next.ServeHTTP(w, r)
				_, _ = w.Write([]byte("</" + label + ">"))
			})
		}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("(inner)"))
	})
	h := composeMiddleware([]func(http.Handler) http.Handler{tag("A"), tag("B")}, inner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := "<A><B>(inner)</B></A>"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q (mw[0]=A should be outermost)", got, want)
	}
}

// Validates the trusted-proxy recipe documented in BUILDING_ON_VEGA.md
// §7 — middleware calls serve.WithClaims, downstream handlers see the
// injected identity via ClaimsFrom.
func TestComposeMiddleware_InjectsClaimsForDownstream(t *testing.T) {
	injectClaims := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithClaims(r.Context(), AuthClaims{UserID: "alice"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClaimsFrom(r.Context())
		if !ok {
			_, _ = w.Write([]byte("no-claims"))
			return
		}
		_, _ = w.Write([]byte(c.UserID))
	})

	h := composeMiddleware([]func(http.Handler) http.Handler{injectClaims}, inner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil))

	if got := rec.Body.String(); got != "alice" {
		t.Errorf("downstream saw %q, want %q", got, "alice")
	}
}
