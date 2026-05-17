package vega

import (
	"sync"
	"time"
)

// ChatEventType categorizes chat stream events.
type ChatEventType string

const (
	ChatEventTextDelta       ChatEventType = "text_delta"
	ChatEventToolStart       ChatEventType = "tool_start"
	ChatEventToolEnd         ChatEventType = "tool_end"
	ChatEventError           ChatEventType = "error"
	ChatEventDone            ChatEventType = "done"
	// ChatEventNoActiveStream is sent on the reconnect endpoint when the
	// caller connected to an agent that has no in-progress stream. It's
	// always followed by a `done` event and the connection closes. Lets
	// frontends use one SSE code path for both "live stream" and "nothing
	// to resume" cases without a content-type heuristic.
	ChatEventNoActiveStream ChatEventType = "no_active_stream"
	// ChatEventRecalled carries the list of memory pages that backed
	// the just-finished turn — both bodies auto-injected via
	// `active: true` and pages explicitly read by the agent through
	// memory_read. Sent immediately before the `done` event so the FE
	// can attach "remembered from X" pills to the assistant message.
	// Refs govega#100.
	ChatEventRecalled ChatEventType = "recalled"
)

// ChatRecallSource discriminates how a memory page entered the
// agent's context for the current turn. "active" means it was
// auto-injected via the `active: true` frontmatter flag; "read"
// means the agent called memory_read explicitly. Both map to the
// same "remembered from" UI surface — the user shouldn't have to
// care about the mechanism. Refs govega#100.
type ChatRecallSource string

const (
	ChatRecallSourceActive ChatRecallSource = "active"
	ChatRecallSourceRead   ChatRecallSource = "read"
)

// ChatRecallEntry is the wire shape of a single recall event in a
// turn's ledger. Scope is a plain string ("user" or "agent") rather
// than a typed enum to keep this package free of any dependency on
// the serve layer's MemoryScope type. Refs govega#100.
type ChatRecallEntry struct {
	Scope  string           `json:"scope"`
	Path   string           `json:"path"`
	Source ChatRecallSource `json:"source"`
	At     time.Time        `json:"at"`
}

// ChatEventCode is a stable string discriminator carried on chat error
// events. Lets callers switch on the cause (rate limit vs auth vs
// invalid request) without substring-matching the prose `error` field.
// Mirrors ErrorClass — same categories, exposed as strings on the wire.
type ChatEventCode string

const (
	ChatEventCodeRateLimit       ChatEventCode = "rate_limit"
	ChatEventCodeOverloaded      ChatEventCode = "overloaded"
	ChatEventCodeTimeout         ChatEventCode = "timeout"
	ChatEventCodeTemporary       ChatEventCode = "temporary"
	ChatEventCodeInvalidRequest  ChatEventCode = "invalid_request"
	ChatEventCodeAuthentication  ChatEventCode = "authentication"
	ChatEventCodeBudgetExceeded  ChatEventCode = "budget_exceeded"
)

// ChatEventCodeFromError classifies an error into a ChatEventCode for
// inclusion on SSE error events. Returns "" for nil so the field omits
// when there's no error to classify.
func ChatEventCodeFromError(err error) ChatEventCode {
	if err == nil {
		return ""
	}
	switch ClassifyError(err) {
	case ErrClassRateLimit:
		return ChatEventCodeRateLimit
	case ErrClassOverloaded:
		return ChatEventCodeOverloaded
	case ErrClassTimeout:
		return ChatEventCodeTimeout
	case ErrClassInvalidRequest:
		return ChatEventCodeInvalidRequest
	case ErrClassAuthentication:
		return ChatEventCodeAuthentication
	case ErrClassBudgetExceeded:
		return ChatEventCodeBudgetExceeded
	default:
		return ChatEventCodeTemporary
	}
}

// ChatEventMetrics holds token/cost/duration stats for a completed response.
type ChatEventMetrics struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMs   int64   `json:"duration_ms"`
}

// ChatEvent is a structured event emitted during a streaming chat response.
// It carries text deltas alongside tool call lifecycle events so that
// callers can render tool activity inline with the response text.
type ChatEvent struct {
	Type        ChatEventType     `json:"type"`
	Delta       string            `json:"delta,omitempty"`
	ToolCallID  string            `json:"tool_call_id,omitempty"`
	ToolName    string            `json:"tool_name,omitempty"`
	Arguments   map[string]any    `json:"arguments,omitempty"`
	Result      string            `json:"result,omitempty"`
	DurationMs  int64             `json:"duration_ms,omitempty"`
	Error       string            `json:"error,omitempty"`
	// Code is a stable error classifier — only set on Type=="error" events.
	// Lets callers switch on the cause without substring-matching `error`.
	Code        ChatEventCode     `json:"code,omitempty"`
	NestedAgent string            `json:"nested_agent,omitempty"`
	Metrics     *ChatEventMetrics `json:"metrics,omitempty"`
	// Recalled is the list of memory pages that backed this turn —
	// only set on Type=="recalled" events. Refs govega#100.
	Recalled []ChatRecallEntry `json:"recalled,omitempty"`
}

// ToolActivity is a completed tool call captured from a streaming
// chat turn. Stored alongside the assistant message so that loading
// chat history reproduces the same tool-call timeline the user saw
// during the live stream (refs govega#48 and #55).
type ToolActivity struct {
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	Result     string         `json:"result,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// CollectToolActivities walks a stream's history of ChatEvents and
// returns the completed tool calls (one per tool_end event). Used by
// the chat / channel handlers to persist tool activity onto the final
// assistant message before storing it.
func CollectToolActivities(events []ChatEvent) []ToolActivity {
	out := make([]ToolActivity, 0)
	for _, e := range events {
		if e.Type != ChatEventToolEnd {
			continue
		}
		out = append(out, ToolActivity{
			ToolCallID: e.ToolCallID,
			ToolName:   e.ToolName,
			Arguments:  e.Arguments,
			Result:     e.Result,
			DurationMs: e.DurationMs,
			Error:      e.Error,
		})
	}
	return out
}

// ChatStream represents a streaming chat response with structured events.
type ChatStream struct {
	events   chan ChatEvent
	response string
	err      error
	done     chan struct{}
	mu       sync.RWMutex
}

// Events returns the channel of chat events.
func (cs *ChatStream) Events() <-chan ChatEvent {
	return cs.events
}

// Response returns the complete text response after the stream is done.
func (cs *ChatStream) Response() string {
	<-cs.done
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.response
}

// Err returns any error that occurred during streaming.
func (cs *ChatStream) Err() error {
	<-cs.done
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.err
}

// newChatStream creates a ChatStream with a buffered event channel.
func newChatStream() *ChatStream {
	return &ChatStream{
		events: make(chan ChatEvent, DefaultStreamBufferSize),
		done:   make(chan struct{}),
	}
}

// toolDuration returns milliseconds elapsed since start.
func toolDuration(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
