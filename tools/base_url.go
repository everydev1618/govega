package tools

import (
	"context"
	"strings"
)

// A self-hosted Vega cannot know its own public name. The base URL wired in
// at boot is a guess — PUBLIC_URL when an operator set one, otherwise
// http://localhost:<port>, which is wrong for everybody who reaches the
// instance by any other name. Every browser request, however, carries the
// answer in its Host header, so the serve layer attaches that origin to the
// request context and the tools that mint deliverable links prefer it.
//
// Agents are instructed to quote write_file's `Accessible at:` line verbatim,
// so whatever is wrong here is what the user is told to click.

type baseURLKey struct{}

// ContextWithBaseURL attaches the origin this turn's request arrived on
// (scheme://host[:port], no trailing slash). An empty url attaches nothing,
// so a caller with no origin to offer can't blank out the configured value.
func ContextWithBaseURL(ctx context.Context, url string) context.Context {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if url == "" {
		return ctx
	}
	return context.WithValue(ctx, baseURLKey{}, url)
}

// BaseURLFrom returns the per-request origin, or "" when the turn did not
// come from an HTTP request (Telegram, cron, a dispatched sub-agent).
func BaseURLFrom(ctx context.Context) string {
	v, _ := ctx.Value(baseURLKey{}).(string)
	return v
}

// baseURLFor resolves the base URL for one tool call: the request origin when
// there is one, else the configured default.
func (t *Tools) baseURLFor(ctx context.Context) string {
	if u := BaseURLFrom(ctx); u != "" {
		return u
	}
	return t.BaseURL()
}
