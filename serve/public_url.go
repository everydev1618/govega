// Package serve — public_url.go
//
// What base URL do agents put in the links they hand the user?
//
// A self-hosted Vega has no way to know its own public name. Bound to
// 0.0.0.0:8822 on a box reachable as http://vega.const, everything it knows
// locally says "localhost:8822" — so that is what write_file reported, and
// the agent, correctly following its instructions to quote that line
// verbatim, handed the user a dead link.
//
// Four sources, in descending authority:
//
//	config    PublicURL / PUBLIC_URL — an operator stated it; nothing overrides it
//	explicit  the onboarding answer, persisted in settings
//	observed  learned from the Host header of the SPA's own API calls
//	fallback  http://localhost:<port> — a guess, and the thing we warn about
//
// The observed layer is what makes this work with zero configuration: the
// browser reaching the dashboard is itself the evidence. It is persisted so
// that turns with no request to learn from — Telegram, cron, a dispatched
// sub-agent — inherit it too, including across restarts. Onboarding asks only
// when there is nothing to deduce (see onboarding.go).

package serve

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Settings keys for the persisted layers.
const (
	settingKeyPublicURL           = "public_url"          // the onboarding answer
	settingKeyPublicURLObserved   = "public_url.observed" // learned from requests
	settingKeyOnboardingCompleted = "onboarding.completed"
)

// Which layer supplied the current base URL. Reported to the UI so the
// settings screen can say "detected" vs "you set this".
const (
	publicURLSourceConfig   = "config"
	publicURLSourceExplicit = "explicit"
	publicURLSourceObserved = "observed"
	publicURLSourceFallback = "fallback"
)

// publicURLResolver layers the four sources and notifies on change.
type publicURLResolver struct {
	mu         sync.RWMutex
	configured string
	explicit   string
	observed   string
	fallback   string
	exposed    bool

	onChange         func(observed string)
	onExplicitChange func(explicit string)
}

func newPublicURLResolver(configured, fallback string) *publicURLResolver {
	return &publicURLResolver{
		configured: strings.TrimRight(strings.TrimSpace(configured), "/"),
		fallback:   strings.TrimRight(strings.TrimSpace(fallback), "/"),
	}
}

// OnChange registers the hook called when an observed origin changes —
// persistence plus live application of the new base URL.
func (p *publicURLResolver) OnChange(fn func(string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onChange = fn
}

// OnExplicitChange registers the hook called when the onboarding answer is set.
func (p *publicURLResolver) OnExplicitChange(fn func(string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onExplicitChange = fn
}

// SetExposed records whether the listener is bound beyond loopback — the
// difference between "localhost is the right answer" and "localhost is
// definitely wrong for somebody".
func (p *publicURLResolver) SetExposed(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exposed = v
}

// Base returns the base URL to put in deliverable links.
func (p *publicURLResolver) Base() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.baseLocked()
}

func (p *publicURLResolver) baseLocked() string {
	switch {
	case p.configured != "":
		return p.configured
	case p.explicit != "":
		return p.explicit
	case p.observed != "":
		return p.observed
	default:
		return p.fallback
	}
}

// Source names the layer Base came from.
func (p *publicURLResolver) Source() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	switch {
	case p.configured != "":
		return publicURLSourceConfig
	case p.explicit != "":
		return publicURLSourceExplicit
	case p.observed != "":
		return publicURLSourceObserved
	default:
		return publicURLSourceFallback
	}
}

// Observe records an origin learned from a request. No-op when an operator
// has pinned the URL (nothing to learn) or when the value is unchanged —
// this runs on every API request, and a store write per request would be
// absurd.
func (p *publicURLResolver) Observe(origin string) {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")

	p.mu.Lock()
	if origin == "" || p.configured != "" || origin == p.observed {
		p.mu.Unlock()
		return
	}
	p.observed = origin
	fn := p.onChange
	p.mu.Unlock()

	if fn != nil {
		fn(origin)
	}
}

// SetExplicit records the onboarding answer. It outranks anything observed
// later, so opening the dashboard through a second hostname can't silently
// retarget a deliberately configured instance.
func (p *publicURLResolver) SetExplicit(u string) {
	u = strings.TrimRight(strings.TrimSpace(u), "/")

	p.mu.Lock()
	p.explicit = u
	fn := p.onExplicitChange
	p.mu.Unlock()

	if fn != nil {
		fn(u)
	}
}

// Restore seeds the persisted layers at boot without firing the hooks that
// would write them straight back to the store.
func (p *publicURLResolver) Restore(explicit, observed string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.explicit = strings.TrimRight(strings.TrimSpace(explicit), "/")
	p.observed = strings.TrimRight(strings.TrimSpace(observed), "/")
}

// NeedsSetup reports whether onboarding must ask for the public URL: only
// when the instance is reachable beyond loopback and no layer but the
// localhost guess has anything to say.
func (p *publicURLResolver) NeedsSetup() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.exposed {
		return false
	}
	return p.configured == "" && p.explicit == "" && p.observed == ""
}

// originFromRequest recovers the origin the client used to reach us —
// scheme://host[:port], no trailing slash — or "" when the request teaches us
// nothing usable.
//
// The Host header is attacker-controllable, so this is deliberately strict:
// anything that doesn't parse as a bare host[:port] is dropped rather than
// echoed into a link. Loopback is dropped too — it's what we already assume.
func originFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xfp := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); xfp != "" {
		if s := strings.ToLower(xfp); s == "http" || s == "https" {
			scheme = s
		}
	}

	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if !validHost(host) || isLoopbackHost(host) {
		return ""
	}
	return scheme + "://" + host
}

// firstHeaderValue takes the leftmost entry of a comma-separated forwarding
// header — the value the original client presented.
func firstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// validHost reports whether host is a bare host or host:port and nothing
// else — no scheme, path, credentials, or whitespace.
func validHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/@ \t") {
		return false
	}
	u, err := url.Parse("http://" + host)
	return err == nil && u.Host == host && u.Path == "" && u.User == nil
}

// isLoopbackHost reports whether host (with or without a port) names this
// machine's loopback interface.
func isLoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(strings.ToLower(strings.TrimSpace(h)), "[]")
	if h == "" || h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// addrIsExposed reports whether a resolved listen address is reachable from
// somewhere other than this machine — i.e. whether a localhost link is
// definitely broken for somebody.
func addrIsExposed(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	h := strings.Trim(host, "[]")
	if h == "" {
		return true // ":8822" — all interfaces
	}
	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsLoopback()
	}
	return !isLoopbackHost(h)
}

// normalizePublicURL turns what a human types into a base URL, or explains
// why it isn't one. A base URL is scheme://host[:port] with no path: anything
// more and the /workspace/… suffix we append would land in the wrong place.
func normalizePublicURL(in string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", fmt.Errorf("URL is required")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s // "vega.const" and "vega.const:8822" are both hosts
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL must start with http:// or https://")
	}
	if !validHost(u.Host) {
		return "", fmt.Errorf("URL must contain a hostname, e.g. http://vega.const")
	}
	if strings.TrimRight(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("URL must be a base address with no path, e.g. http://vega.const")
	}
	return u.Scheme + "://" + u.Host, nil
}

// publicURLSuggestions offers plausible answers for the onboarding form:
// whatever we already observed, then this machine's hostname.
func publicURLSuggestions(hostname, port, observed string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	add(strings.TrimRight(observed, "/"))
	if h := strings.TrimSpace(hostname); h != "" && !isLoopbackHost(h) {
		add("http://" + net.JoinHostPort(h, port))
	}
	return out
}
