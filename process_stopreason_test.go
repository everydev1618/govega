package vega

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/memory"
)

// TestSendSurfacesRefusal verifies a refusal stop reason becomes an error
// (and rolls back the user turn) instead of being returned as a normal,
// possibly empty, answer.
func TestSendSurfacesRefusal(t *testing.T) {
	mock := &blockRecordingLLM{
		responses: []*llm.LLMResponse{
			{Content: "", StopReason: llm.StopReasonRefusal},
		},
	}
	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "refused", System: StaticPrompt("t")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = proc.Send(context.Background(), "do the thing")
	if !errors.Is(err, ErrLLMRefusal) {
		t.Fatalf("Send error = %v, want ErrLLMRefusal", err)
	}
	if msgs := proc.Messages(); len(msgs) != 0 {
		t.Errorf("history not rolled back after refusal: %d messages", len(msgs))
	}
}

// TestSendResumesPauseTurn verifies pause_turn re-sends the assistant turn
// so the server can resume, rather than returning a half-finished answer.
func TestSendResumesPauseTurn(t *testing.T) {
	mock := &blockRecordingLLM{
		responses: []*llm.LLMResponse{
			{
				Content:    "working on it",
				Blocks:     []llm.ContentBlock{{Type: llm.BlockText, Text: "working on it"}},
				StopReason: llm.StopReasonPause,
			},
			{Content: "final answer", StopReason: llm.StopReasonEnd},
		},
	}
	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "pauser", System: StaticPrompt("t")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	resp, err := proc.Send(context.Background(), "long task")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp != "final answer" {
		t.Errorf("response = %q, want the resumed final answer", resp)
	}

	// Second call must include the paused assistant turn for resumption.
	second := mock.callMessages(t, 1)
	last := second[len(second)-1]
	if last.Role != llm.RoleAssistant || last.Content != "working on it" {
		t.Errorf("paused assistant turn not re-sent; last message: %+v", last)
	}
}

// compactingContext is a CompactableContext that records Compact calls.
type compactingContext struct {
	*memory.TokenBudgetContext
	compacted atomic.Int32
}

func (c *compactingContext) Compact(l llm.LLM) error {
	c.compacted.Add(1)
	return nil
}

func (c *compactingContext) NeedsCompaction(threshold int) bool { return true }

// TestSendCompactsOnContextExceeded verifies a context-window overflow
// triggers compaction and a retry when the agent has a compactable context.
func TestSendCompactsOnContextExceeded(t *testing.T) {
	cm := &compactingContext{TokenBudgetContext: memory.NewTokenBudgetContext(100000)}
	mock := &blockRecordingLLM{
		responses: []*llm.LLMResponse{
			{StopReason: llm.StopReasonContextExceeded},
			{Content: "fits now", StopReason: llm.StopReasonEnd},
		},
	}
	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "compactor", System: StaticPrompt("t"), Context: cm})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	resp, err := proc.Send(context.Background(), "big ask")
	if err != nil {
		t.Fatalf("Send should have compacted and retried: %v", err)
	}
	if resp != "fits now" {
		t.Errorf("response = %q", resp)
	}
	if got := cm.compacted.Load(); got != 1 {
		t.Errorf("Compact called %d times, want 1", got)
	}
}

// TestSendErrsOnContextExceededWithoutCompaction verifies a clear error
// when there is no compactable context to shrink.
func TestSendErrsOnContextExceededWithoutCompaction(t *testing.T) {
	mock := &blockRecordingLLM{
		responses: []*llm.LLMResponse{
			{StopReason: llm.StopReasonContextExceeded},
		},
	}
	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "overflow", System: StaticPrompt("t")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = proc.Send(context.Background(), "big ask")
	if !errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("Send error = %v, want ErrContextWindowExceeded", err)
	}
}

// TestStreamSurfacesRefusal verifies the streaming path surfaces a refusal
// stop reason as a stream error.
func TestStreamSurfacesRefusal(t *testing.T) {
	mock := &blockRecordingLLM{
		streamEvents: [][]llm.StreamEvent{
			{
				{Type: llm.StreamEventMessageStart, InputTokens: 5},
				{Type: llm.StreamEventMessageEnd, StopReason: llm.StopReasonRefusal},
			},
		},
	}
	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "stream-refused", System: StaticPrompt("t")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	stream, err := proc.SendStream(context.Background(), "do the thing")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	for range stream.Chunks() {
	}
	if err := stream.Err(); !errors.Is(err, ErrLLMRefusal) {
		t.Fatalf("stream error = %v, want ErrLLMRefusal", err)
	}
}

// TestStreamResumesPauseTurn verifies the streaming loop re-sends the
// assistant turn on pause_turn and continues.
func TestStreamResumesPauseTurn(t *testing.T) {
	mock := &blockRecordingLLM{
		streamEvents: [][]llm.StreamEvent{
			{
				{Type: llm.StreamEventMessageStart, InputTokens: 5},
				{Type: llm.StreamEventContentStart},
				{Type: llm.StreamEventContentDelta, Delta: "working"},
				{Type: llm.StreamEventContentEnd},
				{Type: llm.StreamEventMessageEnd, StopReason: llm.StopReasonPause},
			},
			{
				{Type: llm.StreamEventMessageStart, InputTokens: 5},
				{Type: llm.StreamEventContentStart},
				{Type: llm.StreamEventContentDelta, Delta: " done"},
				{Type: llm.StreamEventContentEnd},
				{Type: llm.StreamEventMessageEnd, StopReason: llm.StopReasonEnd},
			},
		},
	}
	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "stream-pauser", System: StaticPrompt("t")})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	stream, err := proc.SendStream(context.Background(), "long task")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	var text string
	for chunk := range stream.Chunks() {
		text += chunk
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text != "working done" {
		t.Errorf("streamed text = %q, want %q", text, "working done")
	}

	second := mock.callMessages(t, 1)
	last := second[len(second)-1]
	if last.Role != llm.RoleAssistant {
		t.Errorf("paused assistant turn not re-sent; last message role: %v", last.Role)
	}
}
