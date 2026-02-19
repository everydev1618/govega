package vega

import (
	"sync"
	"time"
)

// ChatEventType categorizes chat stream events.
type ChatEventType string

const (
	ChatEventTextDelta ChatEventType = "text_delta"
	ChatEventToolStart ChatEventType = "tool_start"
	ChatEventToolEnd   ChatEventType = "tool_end"
	ChatEventError     ChatEventType = "error"
	ChatEventDone      ChatEventType = "done"
)

// ChatEvent is a structured event emitted during a streaming chat response.
// It carries text deltas alongside tool call lifecycle events so that
// callers can render tool activity inline with the response text.
type ChatEvent struct {
	Type       ChatEventType  `json:"type"`
	Delta      string         `json:"delta,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	Result     string         `json:"result,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
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
