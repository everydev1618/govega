package llm

import "context"

// Options carries per-call overrides for an LLM request. They flow
// through context.Context so callers can inject per-Agent values
// without changing the LLM interface.
//
// Empty / zero fields fall back to the LLM client's defaults.
type Options struct {
	// Model overrides the client's default model for this call.
	Model string

	// Temperature is a float pointer because zero is a valid value;
	// nil means "do not send" (use model default). Backends that
	// don't support sampling parameters drop this silently.
	Temperature *float64

	// MaxTokens caps response output. 0 means use the capability default.
	MaxTokens int

	// Effort sets output_config.effort on supported models.
	// Empty defaults to "high" if the model supports effort.
	Effort string

	// OutputSchema is a JSON Schema enforced on the response via
	// output_config.format. When set on a model that supports
	// structured outputs, the API returns valid JSON conforming to
	// the schema (no markdown fences, no invented fields). Silently
	// dropped on unsupported models — caller falls back to ad-hoc
	// JSON parsing in that case.
	OutputSchema map[string]any
}

type optionsKey struct{}

// ContextWithOptions returns a derived context carrying opts.
func ContextWithOptions(ctx context.Context, opts Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

// OptionsFromContext returns the Options previously installed on ctx,
// or the zero value if none.
func OptionsFromContext(ctx context.Context) Options {
	v, _ := ctx.Value(optionsKey{}).(Options)
	return v
}
