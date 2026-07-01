package events

import "context"

type ctxKey int

const originKey ctxKey = iota

// ContextWithOrigin attaches the causation an event should carry if it is
// emitted during work done on ctx's behalf. The reactive router sets this on a
// wake's context so any events the wake emits inherit its chain depth.
func ContextWithOrigin(ctx context.Context, o *Origin) context.Context {
	return context.WithValue(ctx, originKey, o)
}

// OriginFromContext returns the causation attached to ctx, or nil for a root
// context (a human message, a cron fire, an external sensor).
func OriginFromContext(ctx context.Context) *Origin {
	o, _ := ctx.Value(originKey).(*Origin)
	return o
}

// Depth returns an event's reactive-chain depth: its Origin's Depth, or 0 for a
// root event with no Origin.
func Depth(e Event) int {
	if e.Origin != nil {
		return e.Origin.Depth
	}
	return 0
}
