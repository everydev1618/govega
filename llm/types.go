package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
)

// LLM is the interface for language model backends.
type LLM interface {
	// Generate sends a request and returns the complete response.
	Generate(ctx context.Context, messages []Message, tools []ToolSchema) (*LLMResponse, error)

	// GenerateStream sends a request and returns a channel of streaming events.
	GenerateStream(ctx context.Context, messages []Message, tools []ToolSchema) (<-chan StreamEvent, error)
}

// Message represents a conversation message.
//
// Content carries plain text. Blocks, when non-empty, carries the full
// typed structure of the turn (text, thinking, tool_use, tool_result) and
// takes precedence over Content when building API requests — backends
// convert Blocks directly instead of round-tripping tool activity through
// XML markup in Content.
type Message struct {
	Role    Role
	Content string
	Blocks  []ContentBlock `json:"Blocks,omitempty"`

	// Volatile is per-conversation system content — who the agent is talking
	// to, what it remembers about them — that belongs in the system prompt but
	// is byte-unique per conversation. Meaningful only on a RoleSystem
	// message. Providers that support prompt caching render it as a second,
	// uncached system block after Content, so every conversation shares one
	// cached prefix instead of writing its own. Empty ⇒ one block, as before.
	Volatile string `json:"Volatile,omitempty"`
}

// SystemText returns the whole system prompt as the model sees it: the
// cacheable Content followed by the per-conversation Volatile tail. Use it
// anywhere the split is an implementation detail — assertions, logging,
// estimates — rather than reading Content and silently missing half of it.
func (m Message) SystemText() string {
	if m.Volatile == "" {
		return m.Content
	}
	if m.Content == "" {
		return m.Volatile
	}
	return m.Content + "\n\n" + m.Volatile
}

// Content block types.
const (
	BlockText       = "text"
	BlockThinking   = "thinking"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
	BlockImage      = "image"
	// BlockOpaque carries a content block govega does not model — server-side
	// tool blocks like server_tool_use and web_search_tool_result — verbatim
	// in Raw, so replaying the turn stays lossless.
	BlockOpaque = "opaque"
)

// ContentBlock is one typed unit of message content. Exactly one group of
// fields is meaningful per Type:
//
//   - BlockText:       Text
//   - BlockThinking:   Text (the reasoning), Signature (required for replay)
//   - BlockToolUse:    ID, Name, Arguments
//   - BlockToolResult: ToolUseID, Content, IsError
type ContentBlock struct {
	Type string `json:"type"`

	// Text carries text and thinking content.
	Text string `json:"text,omitempty"`
	// Signature authenticates a thinking block so it can be replayed to
	// the API on the next request of the same turn.
	Signature string `json:"signature,omitempty"`

	// Tool use fields.
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`

	// Tool result fields.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// Image fields (BlockImage): base64-encoded image Data plus its MediaType
	// (e.g. "image/png"). Vision-capable models read these on user turns.
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`

	// Raw is the verbatim API block for BlockOpaque — block types govega
	// does not model, preserved so turn replay is lossless.
	Raw json.RawMessage `json:"raw,omitempty"`
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

	// Blocks is the ordered, typed content of the response (thinking,
	// text, tool_use). Callers replaying the assistant turn should attach
	// these to the next Message so thinking blocks and tool invocations
	// survive the round trip. Empty on backends without block support.
	Blocks []ContentBlock

	// ToolCalls are any tool calls the model wants to make
	ToolCalls []ToolCall

	// Model is the model that actually produced this response, as reported
	// by the backend. It is not always the model that was asked for: the
	// OpenAI-compatible path falls back to its configured model when an
	// override names something the endpoint does not serve. Callers log and
	// price this rather than the request, so a local call is never recorded
	// as a hosted one.
	Model string

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

	// CostUSD is the request cost, set on MessageEnd by backends that
	// know their pricing. Callers should prefer this over recomputing
	// from the model name.
	CostUSD float64

	// StopReason, set on MessageEnd, indicates why generation stopped.
	// Callers must handle refusal and pause_turn instead of treating
	// every stream end as a completed answer.
	StopReason StopReason

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

	// Thinking block lifecycle. Delta carries the thinking text for
	// ThinkingDelta and the signature chunk for ThinkingSignature; the
	// block closes with the generic ContentEnd.
	StreamEventThinkingStart     StreamEventType = "thinking_start"
	StreamEventThinkingDelta     StreamEventType = "thinking_delta"
	StreamEventThinkingSignature StreamEventType = "thinking_signature"
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
	"claude-fable-5":    {10.00, 50.00},
	"claude-mythos-5":   {10.00, 50.00},
	"claude-opus-5":     {5.00, 25.00},
	"claude-sonnet-5":   {3.00, 15.00},
	"claude-opus-4-8":   {5.00, 25.00},
	"claude-opus-4-7":   {5.00, 25.00},
	"claude-opus-4-6":   {5.00, 25.00},
	"claude-opus-4-5":   {5.00, 25.00},
	"claude-sonnet-4-6": {3.00, 15.00},
	"claude-sonnet-4-5": {3.00, 15.00},
	"claude-haiku-4-5":  {1.00, 5.00},

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

	// SupportsTemperature — model accepts the temperature sampling
	// parameter. Opus 4.7 removed sampling params and returns 400 if
	// temperature/top_p/top_k are sent.
	SupportsTemperature bool

	// SupportsStructuredOutputs — model accepts output_config.format
	// for JSON schema enforcement. Supported on Claude 4.5+ Opus,
	// Sonnet 4.6, Haiku 4.5.
	SupportsStructuredOutputs bool

	// MaxOutputTokens is the streaming output ceiling for this model.
	MaxOutputTokens int
}

// modelCapabilities maps model IDs to their capabilities. Unknown models
// resolve to the zero value (no thinking, no effort, conservative max
// tokens) — safe default.
var modelCapabilities = map[string]ModelCapabilities{
	// Fable 5 / Mythos 5: thinking is always on (an explicit adaptive block
	// is accepted); sampling params removed; 128K output, 1M context.
	"claude-fable-5":  {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	"claude-mythos-5": {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	// Opus 5: thinking on by default (an explicit adaptive block is
	// accepted); sampling params removed; full effort ladder; 128K output.
	"claude-opus-5": {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	// Sonnet 5: adaptive thinking on by default; non-default sampling
	// params rejected; 128K output.
	"claude-sonnet-5": {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	// Opus 4.8 keeps the same request surface as 4.7.
	"claude-opus-4-8":   {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	"claude-opus-4-7":   {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: false, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	"claude-opus-4-6":   {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: true, SupportsStructuredOutputs: true, MaxOutputTokens: 128000},
	"claude-opus-4-5":   {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: true, SupportsStructuredOutputs: true, MaxOutputTokens: 64000},
	"claude-sonnet-4-6": {AdaptiveThinking: true, SupportsEffort: true, SupportsTemperature: true, SupportsStructuredOutputs: true, MaxOutputTokens: 64000},

	// Adaptive thinking + effort were introduced in 4.6. Older models
	// don't support them — leave at zero value.
}

// unknownModelWarned dedupes unknown-model warnings so a busy loop on a
// misconfigured model logs once per model, not once per request.
var unknownModelWarned sync.Map

func warnUnknownModel(model, table string) {
	if _, loaded := unknownModelWarned.LoadOrStore(table+"|"+model, true); !loaded {
		slog.Warn("unknown model — update the model tables in llm/types.go",
			"model", model, "table", table)
	}
}

// CapabilitiesFor returns the capabilities for the given model. Unknown
// models return the zero value (everything disabled: no adaptive thinking,
// no effort, conservative output cap) and log a warning so a stale table
// or a typo'd model ID surfaces instead of silently running degraded.
func CapabilitiesFor(model string) ModelCapabilities {
	caps, ok := modelCapabilities[model]
	if !ok {
		warnUnknownModel(model, "capabilities")
	}
	return caps
}

// CalculateCost calculates the cost of a request including prompt cache tokens.
// Cache writes cost 125% of base input price; cache reads cost 10%.
func CalculateCost(model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int) float64 {
	pricing, ok := modelPricing[model]
	if !ok {
		// Unknown model: warn and bill at the most expensive current rate
		// so budget enforcement over-estimates rather than silently
		// under-charging (which would let spend blow past Agent.Budget).
		warnUnknownModel(model, "pricing")
		pricing = modelPricing["claude-fable-5"]
	}

	inputCost := float64(inputTokens) / 1_000_000 * pricing.InputPer1M
	outputCost := float64(outputTokens) / 1_000_000 * pricing.OutputPer1M
	cacheWriteCost := float64(cacheCreationTokens) / 1_000_000 * pricing.InputPer1M * 1.25
	cacheReadCost := float64(cacheReadTokens) / 1_000_000 * pricing.InputPer1M * 0.10

	return inputCost + outputCost + cacheWriteCost + cacheReadCost
}
