package serve

import "context"

// CallerResolver enriches a context with the identity of the user a piece
// of background work is being run on behalf of, so downstream code (LLM
// clients, tools) can look up per-user credentials.
//
// Background paths — the scheduler tick, Telegram inbound, peering
// inbound — don't pass through HTTP auth middleware, so they have no
// AuthClaims or BYOK key attached to ctx. They call a registered
// CallerResolver to get an enriched context with the same shape an
// authenticated HTTP request would carry.
//
// userID names the apex/govega user the work belongs to. A resolver MAY
// ignore the argument and use a configured default — useful for
// single-user-per-tenant deployments where every background op is run
// as the tenant owner.
//
// A nil resolver is a no-op: callers MUST treat nil as "leave ctx
// unchanged" rather than calling. This keeps self-hosted deployments
// (no BYOK, no per-user keys) working without any resolver registered.
type CallerResolver func(ctx context.Context, userID string) context.Context

// applyResolver is the trivial helper every entry point uses: call the
// resolver if set, otherwise return ctx unchanged. Centralised so the
// nil check isn't duplicated at every call site.
func applyResolver(ctx context.Context, r CallerResolver, userID string) context.Context {
	if r == nil {
		return ctx
	}
	return r(ctx, userID)
}

// WithCallerResolver registers a CallerResolver on s. The resolver is
// applied to background-work dispatch contexts (scheduler tick, Telegram
// inbound, peering inbound) so they carry caller identity and per-user
// credentials downstream. MUST be called before Start — registration is
// not propagated to already-running subsystems.
//
// Pass nil to clear. A nil resolver leaves background ctx unchanged.
func (s *Server) WithCallerResolver(r CallerResolver) *Server {
	s.callerResolver = r
	return s
}
