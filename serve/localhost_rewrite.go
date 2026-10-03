package serve

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/everydev1618/govega/tools"
)

// Last line of defence for deliverable links. With the tool layer fixed, the
// URLs agents are told to quote are already correct — but a model can still
// type a localhost URL from memory, or carry one forward from an older
// message in its own history. Any reference to *our own* port is rewritten to
// the public base on the way out.
//
// Only our port. An agent describing a dev server on :3000 is making a
// different, legitimate statement, and rewriting that would be a lie.

var localhostPatternCache sync.Map // port string -> *regexp.Regexp

func localhostPattern(port string) *regexp.Regexp {
	if v, ok := localhostPatternCache.Load(port); ok {
		return v.(*regexp.Regexp)
	}
	// Go's regexp has no lookahead, so the "not another digit" guard is a
	// captured trailing character that the replacement puts back. This keeps
	// :8822 from matching inside :88220.
	re := regexp.MustCompile(`(?i)https?://(?:localhost|127\.0\.0\.1|\[::1\]):` +
		regexp.QuoteMeta(port) + `([^0-9]|$)`)
	localhostPatternCache.Store(port, re)
	return re
}

// rewriteLocalhostURLs replaces references to this server's own loopback
// address with its public base URL. No-op when there is nothing better to
// offer than localhost itself.
func rewriteLocalhostURLs(text, base, port string) string {
	if text == "" || base == "" || port == "" {
		return text
	}
	if !strings.Contains(text, port) {
		return text
	}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if isLoopbackHost(host) {
		return text // nothing better to point at
	}
	return localhostPattern(port).ReplaceAllString(text, base+"$1")
}

// sanitizeOutbound rewrites our own localhost links in text an agent is about
// to show a user, preferring the origin this turn arrived on.
func (s *Server) sanitizeOutbound(ctx context.Context, text string) string {
	base := tools.BaseURLFrom(ctx)
	if base == "" {
		base = s.currentPublicBase()
	}
	return rewriteLocalhostURLs(text, base, s.publicPort)
}
