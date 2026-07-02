package vega

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

// blockRecordingLLM captures the messages of every Generate/GenerateStream call
// and plays back scripted responses.
type blockRecordingLLM struct {
	mu        sync.Mutex
	calls     [][]llm.Message
	responses []*llm.LLMResponse
	// streamEvents, when set, scripts GenerateStream calls (one slice per call).
	streamEvents [][]llm.StreamEvent
}

func (r *blockRecordingLLM) record(messages []llm.Message) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	r.calls = append(r.calls, cp)
	return len(r.calls) - 1
}

func (r *blockRecordingLLM) Generate(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	i := r.record(messages)
	if i < len(r.responses) {
		return r.responses[i], nil
	}
	return &llm.LLMResponse{Content: "done"}, nil
}

func (r *blockRecordingLLM) GenerateStream(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	i := r.record(messages)
	ch := make(chan llm.StreamEvent, 32)
	go func() {
		defer close(ch)
		if i < len(r.streamEvents) {
			for _, ev := range r.streamEvents[i] {
				ch <- ev
			}
		}
	}()
	return ch, nil
}

func (r *blockRecordingLLM) callMessages(t *testing.T, i int) []llm.Message {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) <= i {
		t.Fatalf("LLM saw %d calls, want > %d", len(r.calls), i)
	}
	return r.calls[i]
}

func echoTools() *tools.Tools {
	ts := tools.NewTools()
	ts.Register("echo", func(text string) string { return "echoed: " + text })
	return ts
}

// assertStructuredToolTurn checks that the replayed assistant turn and tool
// results in the second LLM call use typed Blocks, with the thinking block
// (and signature) intact and no XML markup anywhere.
func assertStructuredToolTurn(t *testing.T, msgs []llm.Message, wantThinking bool) {
	t.Helper()
	if len(msgs) < 3 {
		t.Fatalf("second call has %d messages, want >= 3 (user, assistant, tool results)", len(msgs))
	}

	assistant := msgs[len(msgs)-2]
	if assistant.Role != llm.RoleAssistant {
		t.Fatalf("second-to-last message role = %q, want assistant", assistant.Role)
	}
	if len(assistant.Blocks) == 0 {
		t.Fatal("assistant turn has no typed Blocks — still using the XML round trip")
	}
	var sawThinking, sawToolUse bool
	for _, b := range assistant.Blocks {
		switch b.Type {
		case llm.BlockThinking:
			sawThinking = true
			if b.Signature == "" {
				t.Error("thinking block replayed without its signature")
			}
		case llm.BlockToolUse:
			sawToolUse = true
			if b.ID != "tu-1" || b.Name != "echo" {
				t.Errorf("tool_use block wrong: %+v", b)
			}
		}
	}
	if wantThinking && !sawThinking {
		t.Error("thinking block was dropped from the replayed assistant turn")
	}
	if !sawToolUse {
		t.Error("no tool_use block in the replayed assistant turn")
	}

	results := msgs[len(msgs)-1]
	if results.Role != llm.RoleUser {
		t.Fatalf("last message role = %q, want user (tool results)", results.Role)
	}
	if len(results.Blocks) != 1 {
		t.Fatalf("tool results message has %d blocks, want 1: %+v", len(results.Blocks), results.Blocks)
	}
	tr := results.Blocks[0]
	if tr.Type != llm.BlockToolResult || tr.ToolUseID != "tu-1" {
		t.Errorf("tool_result block wrong: %+v", tr)
	}
	if !strings.Contains(tr.Content, "echoed: hi") {
		t.Errorf("tool_result content = %q, want the tool output", tr.Content)
	}

	for _, m := range msgs {
		if strings.Contains(m.Content, "<tool_use ") || strings.Contains(m.Content, "<tool_result ") {
			t.Errorf("XML tool markup still present in message content: %q", m.Content)
		}
	}
}

// TestToolLoopReplaysStructuredBlocks covers the non-streaming Send path.
func TestToolLoopReplaysStructuredBlocks(t *testing.T) {
	mock := &blockRecordingLLM{
		responses: []*llm.LLMResponse{
			{
				Content: "Let me echo that.",
				Blocks: []llm.ContentBlock{
					{Type: llm.BlockThinking, Text: "the user wants an echo", Signature: "sig-1"},
					{Type: llm.BlockText, Text: "Let me echo that."},
					{Type: llm.BlockToolUse, ID: "tu-1", Name: "echo", Arguments: map[string]any{"text": "hi"}},
				},
				ToolCalls: []llm.ToolCall{{ID: "tu-1", Name: "echo", Arguments: map[string]any{"text": "hi"}}},
			},
			{Content: "It said: echoed: hi"},
		},
	}

	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "echoer", System: StaticPrompt("t"), Tools: echoTools()})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	resp, err := proc.Send(context.Background(), "please echo hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp != "It said: echoed: hi" {
		t.Errorf("response = %q", resp)
	}

	assertStructuredToolTurn(t, mock.callMessages(t, 1), true)
}

// TestStreamToolLoopReplaysStructuredBlocks covers the streaming path,
// including thinking blocks assembled from stream events.
func TestStreamToolLoopReplaysStructuredBlocks(t *testing.T) {
	mock := &blockRecordingLLM{
		streamEvents: [][]llm.StreamEvent{
			{
				{Type: llm.StreamEventMessageStart, InputTokens: 10},
				{Type: llm.StreamEventThinkingStart},
				{Type: llm.StreamEventThinkingDelta, Delta: "the user wants an echo"},
				{Type: llm.StreamEventThinkingSignature, Delta: "sig-1"},
				{Type: llm.StreamEventContentEnd},
				{Type: llm.StreamEventContentStart},
				{Type: llm.StreamEventContentDelta, Delta: "Let me echo that."},
				{Type: llm.StreamEventContentEnd},
				{Type: llm.StreamEventToolStart, ToolCall: &llm.ToolCall{ID: "tu-1", Name: "echo"}},
				{Type: llm.StreamEventToolDelta, Delta: `{"text":"hi"}`},
				{Type: llm.StreamEventContentEnd},
				{Type: llm.StreamEventMessageEnd, OutputTokens: 5},
			},
			{
				{Type: llm.StreamEventMessageStart, InputTokens: 20},
				{Type: llm.StreamEventContentStart},
				{Type: llm.StreamEventContentDelta, Delta: "It said: echoed: hi"},
				{Type: llm.StreamEventContentEnd},
				{Type: llm.StreamEventMessageEnd, OutputTokens: 8},
			},
		},
	}

	o := NewOrchestrator(WithLLM(mock))
	proc, err := o.Spawn(Agent{Name: "echoer-stream", System: StaticPrompt("t"), Tools: echoTools()})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	stream, err := proc.SendStream(context.Background(), "please echo hi")
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	for range stream.Chunks() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream: %v", err)
	}

	assertStructuredToolTurn(t, mock.callMessages(t, 1), true)
}
