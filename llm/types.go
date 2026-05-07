package llm

import "context"

// LLM is the interface for language model backends.
type LLM interface {
	// Generate sends a request and returns the complete response.
	Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error)

	// GenerateStream sends a request and returns a channel of streaming events.
	GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error)
}

// Message represents a conversation message.
type Message struct {
	Role    Role
	Content string
}

// Role identifies the message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// LLMResponse is the response from an LLM call.
type LLMResponse struct {
	// Content is the text response
	Content string

	// ToolCalls are any tool calls the model wants to make
	ToolCalls []ToolCall

	// Token counts
	InputTokens  int
	OutputTokens int

	// Cache token counts (Anthropic prompt caching)
	CacheCreationInputTokens int
	CacheReadInputTokens     int

	// Cost in USD
	CostUSD float64

	// Latency in milliseconds
	LatencyMs int64

	// StopReason indicates why generation stopped
	StopReason StopReason
}

// ToolCall represents a tool call from the LLM.
type ToolCall struct {
	// ID is the unique identifier for this tool call
	ID string

	// Name is the tool being called
	Name string

	// Arguments are the parameters passed to the tool
	Arguments map[string]any
}

// StopReason indicates why the LLM stopped generating.
type StopReason string

const (
	StopReasonEnd      StopReason = "end_turn"
	StopReasonToolUse  StopReason = "tool_use"
	StopReasonLength   StopReason = "max_tokens"
	StopReasonStop     StopReason = "stop_sequence"
	StopReasonFiltered StopReason = "content_filter"

	// StopReasonPause is set when a server-side tool sampling loop hit
	// its iteration limit. Caller should re-send the assistant turn
	// unchanged; the API resumes automatically.
	StopReasonPause StopReason = "pause_turn"

	// StopReasonRefusal is set when Claude declined to respond for
	// safety reasons. Output may not match an expected schema. Do
	// NOT retry the same prompt — surface to the user.
	StopReasonRefusal StopReason = "refusal"

	// StopReasonContextExceeded is set when the model's context
	// window was exhausted (distinct from max_tokens, which is the
	// per-response output cap). Caller should compact or split the
	// conversation.
	StopReasonContextExceeded StopReason = "model_context_window_exceeded"
)

// StreamEvent is an event from streaming generation.
type StreamEvent struct {
	// Type of event
	Type StreamEventType

	// Delta is new content for ContentDelta events
	Delta string

	// ToolCall for ToolCallStart events
	ToolCall *ToolCall

	// Error if something went wrong
	Error error

	// InputTokens after message start
	InputTokens int

	// OutputTokens after message end
	OutputTokens int

	// Cache token counts (Anthropic prompt caching)
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// StreamEventType categorizes stream events.
type StreamEventType string

const (
	StreamEventMessageStart StreamEventType = "message_start"
	StreamEventContentStart StreamEventType = "content_start"
	StreamEventContentDelta StreamEventType = "content_delta"
	StreamEventContentEnd   StreamEventType = "content_end"
	StreamEventToolStart    StreamEventType = "tool_start"
	StreamEventToolDelta    StreamEventType = "tool_delta"
	StreamEventToolEnd      StreamEventType = "tool_end"
	StreamEventMessageEnd   StreamEventType = "message_end"
	StreamEventError        StreamEventType = "error"
)

// ToolSchema describes a tool for the LLM.
type ToolSchema struct {
	// Name of the tool
	Name string `json:"name"`

	// Description of what the tool does
	Description string `json:"description"`

	// InputSchema is the JSON Schema for parameters
	InputSchema map[string]any `json:"input_schema"`
}

// Model pricing for cost calculation (USD per 1M tokens)
var modelPricing = map[string]struct {
	InputPer1M  float64
	OutputPer1M float64
}{
	// Current generation
	"claude-opus-4-7":     {5.00, 25.00},
	"claude-opus-4-6":     {5.00, 25.00},
	"claude-opus-4-5":     {5.00, 25.00},
	"claude-sonnet-4-6":   {3.00, 15.00},
	"claude-sonnet-4-5":   {3.00, 15.00},
	"claude-haiku-4-5":    {1.00, 5.00},

	// Dated aliases
	"claude-haiku-4-5-20251001": {1.00, 5.00},

	// Original 4.0 launch (May 2025) — kept at launch pricing for historical traffic
	"claude-sonnet-4-20250514": {3.00, 15.00},
	"claude-opus-4-20250514":   {15.00, 75.00},

	// Claude 3 family (legacy)
	"claude-haiku-3-20240307":    {0.25, 1.25},
	"claude-3-5-sonnet-20241022": {3.00, 15.00},
	"claude-3-opus-20240229":     {15.00, 75.00},
	"claude-3-sonnet-20240229":   {3.00, 15.00},
	"claude-3-haiku-20240307":    {0.25, 1.25},
}

// ModelCapabilities describes which API features a model supports. Used to
// gate per-request fields so we don't send shapes that 400 on the model.
type ModelCapabilities struct {
	// AdaptiveThinking — model supports {type: "adaptive"} thinking.
	// Older models use the legacy budget_tokens form, which we don't emit.
	AdaptiveThinking bool

	// SupportsEffort — model accepts output_config.effort.
	// Sonnet 4.5 and Haiku 4.5 return 400 if it's sent.
	SupportsEffort bool

	// MaxOutputTokens is the streaming output ceiling for this model.
	MaxOutputTokens int
}

// modelCapabilities maps model IDs to their capabilities. Unknown models
// resolve to the zero value (no thinking, no effort, conservative max
// tokens) — safe default.
var modelCapabilities = map[string]ModelCapabilities{
	"claude-opus-4-7":   {AdaptiveThinking: true, SupportsEffort: true, MaxOutputTokens: 128000},
	"claude-opus-4-6":   {AdaptiveThinking: true, SupportsEffort: true, MaxOutputTokens: 128000},
	"claude-opus-4-5":   {AdaptiveThinking: true, SupportsEffort: true, MaxOutputTokens: 64000},
	"claude-sonnet-4-6": {AdaptiveThinking: true, SupportsEffort: true, MaxOutputTokens: 64000},

	// Adaptive thinking + effort were introduced in 4.6. Older models
	// don't support them — leave at zero value.
}

// CapabilitiesFor returns the capabilities for the given model. Unknown
// models return the zero value (everything disabled).
func CapabilitiesFor(model string) ModelCapabilities {
	return modelCapabilities[model]
}

// CalculateCost calculates the cost of a request including prompt cache tokens.
// Cache writes cost 125% of base input price; cache reads cost 10%.
func CalculateCost(model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int) float64 {
	pricing, ok := modelPricing[model]
	if !ok {
		// Default pricing if model not found
		pricing = modelPricing["claude-sonnet-4-20250514"]
	}

	inputCost := float64(inputTokens) / 1_000_000 * pricing.InputPer1M
	outputCost := float64(outputTokens) / 1_000_000 * pricing.OutputPer1M
	cacheWriteCost := float64(cacheCreationTokens) / 1_000_000 * pricing.InputPer1M * 1.25
	cacheReadCost := float64(cacheReadTokens) / 1_000_000 * pricing.InputPer1M * 0.10

	return inputCost + outputCost + cacheWriteCost + cacheReadCost
}
