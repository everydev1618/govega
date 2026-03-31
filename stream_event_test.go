package vega

import (
	"testing"
	"time"
)

func TestChatEventTypes(t *testing.T) {
	tests := []struct {
		et   ChatEventType
		want string
	}{
		{ChatEventTextDelta, "text_delta"},
		{ChatEventToolStart, "tool_start"},
		{ChatEventToolEnd, "tool_end"},
		{ChatEventError, "error"},
		{ChatEventDone, "done"},
	}

	for _, tt := range tests {
		if string(tt.et) != tt.want {
			t.Errorf("ChatEventType = %q, want %q", tt.et, tt.want)
		}
	}
}

func TestNewChatStream(t *testing.T) {
	cs := newChatStream()
	if cs == nil {
		t.Fatal("newChatStream() returned nil")
	}
	if cs.events == nil {
		t.Error("events channel should not be nil")
	}
	if cs.done == nil {
		t.Error("done channel should not be nil")
	}
}

func TestChatStreamEvents(t *testing.T) {
	cs := newChatStream()

	go func() {
		cs.events <- ChatEvent{Type: ChatEventTextDelta, Delta: "hello"}
		cs.events <- ChatEvent{Type: ChatEventDone}
		close(cs.events)
		close(cs.done)
	}()

	var events []ChatEvent
	for ev := range cs.Events() {
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}
	if events[0].Delta != "hello" {
		t.Errorf("First event delta = %q, want %q", events[0].Delta, "hello")
	}
	if events[1].Type != ChatEventDone {
		t.Errorf("Second event type = %q, want %q", events[1].Type, ChatEventDone)
	}
}

func TestChatStreamResponse(t *testing.T) {
	cs := newChatStream()

	go func() {
		cs.mu.Lock()
		cs.response = "complete response"
		cs.mu.Unlock()
		close(cs.events)
		close(cs.done)
	}()

	resp := cs.Response()
	if resp != "complete response" {
		t.Errorf("Response() = %q, want %q", resp, "complete response")
	}
}

func TestChatStreamErr(t *testing.T) {
	cs := newChatStream()

	go func() {
		cs.mu.Lock()
		cs.err = ErrTimeout
		cs.mu.Unlock()
		close(cs.events)
		close(cs.done)
	}()

	err := cs.Err()
	if err != ErrTimeout {
		t.Errorf("Err() = %v, want %v", err, ErrTimeout)
	}
}

func TestChatStreamNoError(t *testing.T) {
	cs := newChatStream()

	go func() {
		close(cs.events)
		close(cs.done)
	}()

	err := cs.Err()
	if err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestChatEventMetrics(t *testing.T) {
	m := &ChatEventMetrics{
		InputTokens:  100,
		OutputTokens: 50,
		CostUSD:      0.005,
		DurationMs:   250,
	}

	if m.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", m.InputTokens)
	}
	if m.DurationMs != 250 {
		t.Errorf("DurationMs = %d, want 250", m.DurationMs)
	}
}

func TestChatEventFields(t *testing.T) {
	ev := ChatEvent{
		Type:        ChatEventToolStart,
		ToolCallID:  "call-1",
		ToolName:    "read_file",
		Arguments:   map[string]any{"path": "/tmp/test.txt"},
		NestedAgent: "SubAgent",
	}

	if ev.ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q, want %q", ev.ToolCallID, "call-1")
	}
	if ev.NestedAgent != "SubAgent" {
		t.Errorf("NestedAgent = %q, want %q", ev.NestedAgent, "SubAgent")
	}
}

func TestToolDuration(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	d := toolDuration(start)
	if d < 90 || d > 200 {
		t.Errorf("toolDuration = %d, expected ~100ms", d)
	}
}
