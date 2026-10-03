package serve

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

// originFromRequest recovers the origin the browser actually used to reach us
// — the one piece of evidence a self-hosted instance has about its own public
// name. Loopback and malformed hosts are rejected: they teach us nothing, and
// a Host header is attacker-controllable, so anything we can't parse into a
// plain host[:port] is dropped rather than echoed into a link.
func TestOriginFromRequest(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		tls     bool
		headers map[string]string
		want    string
	}{
		{"plain host", "vega.const", false, nil, "http://vega.const"},
		{"host with port", "et-m1:8822", false, nil, "http://et-m1:8822"},
		{"tls implies https", "vega.example.com", true, nil, "https://vega.example.com"},
		{"x-forwarded-proto honored", "vega.example.com", false,
			map[string]string{"X-Forwarded-Proto": "https"}, "https://vega.example.com"},
		{"x-forwarded-proto list takes first", "vega.example.com", false,
			map[string]string{"X-Forwarded-Proto": "https, http"}, "https://vega.example.com"},
		{"x-forwarded-host wins over host", "internal:8822", false,
			map[string]string{"X-Forwarded-Host": "vega.example.com"}, "http://vega.example.com"},
		{"x-forwarded-host list takes first", "internal:8822", false,
			map[string]string{"X-Forwarded-Host": "vega.example.com, internal"}, "http://vega.example.com"},

		// Rejected — nothing learned.
		{"localhost rejected", "localhost:8822", false, nil, ""},
		{"127.0.0.1 rejected", "127.0.0.1:8822", false, nil, ""},
		{"127.x loopback rejected", "127.1.2.3:8822", false, nil, ""},
		{"ipv6 loopback rejected", "[::1]:8822", false, nil, ""},
		{"empty host rejected", "", false, nil, ""},
		{"host with path rejected", "vega.const/evil", false, nil, ""},
		{"host with credentials rejected", "user@vega.const", false, nil, ""},
		{"host with space rejected", "vega const", false, nil, ""},
		{"host with scheme rejected", "http://vega.const", false, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/stats", nil)
			r.Host = tc.host
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := originFromRequest(r); got != tc.want {
				t.Errorf("originFromRequest(host=%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

// The resolver layers four sources. Explicit operator configuration always
// beats anything learned at runtime, so opening the dashboard through some
// other hostname can never silently retarget a deliberately configured
// instance.
func TestPublicURLResolverPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		explicit   string
		observed   string
		want       string
		wantSource string
	}{
		{"all empty falls back", "", "", "", "http://localhost:8822", publicURLSourceFallback},
		{"observed beats fallback", "", "", "http://vega.const", "http://vega.const", publicURLSourceObserved},
		{"explicit beats observed", "", "http://vega.example.com", "http://vega.const", "http://vega.example.com", publicURLSourceExplicit},
		{"configured beats explicit", "https://cfg.example.com", "http://vega.example.com", "http://vega.const", "https://cfg.example.com", publicURLSourceConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPublicURLResolver(tc.configured, "http://localhost:8822")
			if tc.explicit != "" {
				r.SetExplicit(tc.explicit)
			}
			if tc.observed != "" {
				r.Observe(tc.observed)
			}
			if got := r.Base(); got != tc.want {
				t.Errorf("Base() = %q, want %q", got, tc.want)
			}
			if got := r.Source(); got != tc.wantSource {
				t.Errorf("Source() = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

// Observe persists through the injected writer, but only when the value
// actually changes — every authenticated API request calls it, and a store
// write per request would be absurd.
func TestPublicURLResolverObservePersistsOnChange(t *testing.T) {
	var writes []string
	r := newPublicURLResolver("", "http://localhost:8822")
	r.OnChange(func(origin string) { writes = append(writes, origin) })

	r.Observe("http://vega.const")
	r.Observe("http://vega.const")
	r.Observe("")
	r.Observe("http://et-m1:8822")

	want := []string{"http://vega.const", "http://et-m1:8822"}
	if len(writes) != len(want) {
		t.Fatalf("writes = %v, want %v", writes, want)
	}
	for i := range want {
		if writes[i] != want[i] {
			t.Fatalf("writes = %v, want %v", writes, want)
		}
	}
}

// When an operator has pinned PublicURL, there is nothing to learn: skip the
// bookkeeping entirely rather than churn the store with origins we'll ignore.
func TestPublicURLResolverObserveIgnoredWhenConfigured(t *testing.T) {
	var writes []string
	r := newPublicURLResolver("https://cfg.example.com", "http://localhost:8822")
	r.OnChange(func(origin string) { writes = append(writes, origin) })

	r.Observe("http://vega.const")

	if len(writes) != 0 {
		t.Fatalf("writes = %v, want none", writes)
	}
	if got := r.Base(); got != "https://cfg.example.com" {
		t.Errorf("Base() = %q, want the configured URL", got)
	}
}

// SetExplicit is the onboarding answer. It persists via its own hook and
// takes effect immediately.
func TestPublicURLResolverSetExplicitNotifies(t *testing.T) {
	var got string
	r := newPublicURLResolver("", "http://localhost:8822")
	r.OnExplicitChange(func(u string) { got = u })

	r.SetExplicit("http://vega.const/")

	if got != "http://vega.const" {
		t.Errorf("explicit hook got %q, want trailing slash trimmed", got)
	}
	if r.Base() != "http://vega.const" {
		t.Errorf("Base() = %q", r.Base())
	}
}

// NeedsPublicURL drives the onboarding prompt: ask only when we are still
// handing out localhost links AND the server is bound somewhere a localhost
// link cannot possibly work. A laptop bound to loopback is correctly served
// by localhost and must never be nagged.
func TestPublicURLResolverNeedsSetup(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		observed   string
		exposed    bool
		want       bool
	}{
		{"loopback-bound laptop: localhost is right", "", "", false, false},
		{"exposed and still on localhost: ask", "", "", true, true},
		{"exposed but origin learned: deduced", "", "http://vega.const", true, false},
		{"exposed but operator configured: done", "https://cfg.example.com", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPublicURLResolver(tc.configured, "http://localhost:8822")
			r.SetExposed(tc.exposed)
			if tc.observed != "" {
				r.Observe(tc.observed)
			}
			if got := r.NeedsSetup(); got != tc.want {
				t.Errorf("NeedsSetup() = %v, want %v", got, tc.want)
			}
		})
	}
}

// addrIsExposed distinguishes "bound to loopback, localhost links are
// correct" from "bound to the world, localhost links are broken for someone".
func TestAddrIsExposed(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8822", false},
		{"[::1]:8822", false},
		{"0.0.0.0:8822", true},
		{"[::]:8822", true},
		{"192.168.1.20:8822", true},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := addrIsExposed(tc.addr); got != tc.want {
				t.Errorf("addrIsExposed(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// normalizePublicURL is what the onboarding form's answer runs through. It
// accepts what a human types and rejects what cannot be a base URL.
func TestNormalizePublicURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"http://vega.const", "http://vega.const", false},
		{"http://vega.const/", "http://vega.const", false},
		{"https://vega.example.com:8443/", "https://vega.example.com:8443", false},
		{"  http://vega.const  ", "http://vega.const", false},
		{"vega.const", "http://vega.const", false},           // scheme inferred
		{"vega.const:8822", "http://vega.const:8822", false}, // host:port, not scheme:path
		{"http://vega.const/sub/path", "", true},             // a base URL has no path
		{"ftp://vega.const", "", true},
		{"http://", "", true},
		{"", "", true},
		{"not a url", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := normalizePublicURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizePublicURL(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePublicURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("normalizePublicURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
