package llm

import "context"

type apiKeyCtxKey struct{}

// WithAPIKeyContext returns ctx carrying a per-request Anthropic API
// key. When set, outbound calls use this key instead of the one passed
// to NewAnthropic. Empty values are stored verbatim — callers should
// pass a real key.
func WithAPIKeyContext(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, apiKeyCtxKey{}, key)
}

// APIKeyFromContext returns the per-request key set by WithAPIKeyContext,
// or "" when none is attached.
func APIKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(apiKeyCtxKey{}).(string)
	return v
}

// resolveAPIKey picks the per-request key when present, otherwise the
// static key. Used by the Anthropic client at the outbound-HTTP seam.
func (a *AnthropicLLM) resolveAPIKey(ctx context.Context) string {
	if k := APIKeyFromContext(ctx); k != "" {
		return k
	}
	return a.apiKey
}
