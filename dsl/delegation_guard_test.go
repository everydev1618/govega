package dsl

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/everydev1618/govega/llm"
)

// scriptedToolLLM returns scripted responses; records every call's messages.
type scriptedToolLLM struct {
	mu        sync.Mutex
	calls     [][]llm.Message
	responses []*llm.LLMResponse
}

func (s *scriptedToolLLM) Generate(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (*llm.LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]llm.Message, len(messages))
	copy(cp, messages)
	s.calls = append(s.calls, cp)
	if len(s.calls) <= len(s.responses) {
		return s.responses[len(s.calls)-1], nil
	}
	return &llm.LLMResponse{Content: "done"}, nil
}

func (s *scriptedToolLLM) GenerateStream(ctx context.Context, messages []llm.Message, _ []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Type: llm.StreamEventContentDelta, Delta: "done"}
	close(ch)
	return ch, nil
}

func guardInterpreter(t *testing.T, backend llm.LLM) *Interpreter {
	t.Helper()
	doc, err := NewParser().Parse([]byte(`
name: guard-test
agents:
  worker:
    model: claude-sonnet-4-6
    system: worker
  echoer:
    model: claude-sonnet-4-6
    system: echoer
    tools: [send_to_agent]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	interp, err := NewInterpreter(doc, WithLLM(backend))
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	RegisterIrisTools(interp, DefaultIrisConfig())
	return interp
}

// TestDelegationDepthCapped verifies both delegation paths refuse once the
// chain reaches the depth limit, instead of burning tokens forever.
func TestDelegationDepthCapped(t *testing.T) {
	interp := guardInterpreter(t, &scriptedToolLLM{})

	ctx := context.Background()
	for i := 0; i < maxDelegationDepth(); i++ {
		ctx = contextWithDelegationHop(ctx, fmt.Sprintf("agent-%d", i))
	}

	if _, err := interp.SendToAgent(ctx, "worker", "go"); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("SendToAgent at depth limit: err = %v, want depth error", err)
	}
	if _, err := interp.DispatchToAgent(ctx, "worker", "go"); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("DispatchToAgent at depth limit: err = %v, want depth error", err)
	}
}

// TestDelegationCycleBlocked verifies delegating to an agent that is already
// in the active chain is rejected — an A→B→A ping-pong must not loop.
func TestDelegationCycleBlocked(t *testing.T) {
	interp := guardInterpreter(t, &scriptedToolLLM{})

	ctx := contextWithDelegationHop(context.Background(), "worker")
	if _, err := interp.SendToAgent(ctx, "worker", "go"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("SendToAgent to agent already in chain: err = %v, want cycle error", err)
	}
	if _, err := interp.DispatchToAgent(ctx, "worker", "go"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("DispatchToAgent to agent already in chain: err = %v, want cycle error", err)
	}
}

// TestDelegationChainThreadsThroughTurns proves the chain rides the real
// tool path: an agent whose turn calls send_to_agent targeting itself gets
// a cycle error back as the tool result.
func TestDelegationChainThreadsThroughTurns(t *testing.T) {
	mock := &scriptedToolLLM{
		responses: []*llm.LLMResponse{
			{
				Content:   "delegating to myself",
				ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "send_to_agent", Arguments: map[string]any{"agent": "echoer", "message": "hi me"}}},
			},
			{Content: "finished"},
		},
	}
	interp := guardInterpreter(t, mock)

	if _, err := interp.SendToAgent(context.Background(), "echoer", "start"); err != nil {
		t.Fatalf("SendToAgent: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.calls) < 2 {
		t.Fatalf("expected a second LLM call carrying the tool result, got %d calls", len(mock.calls))
	}
	found := false
	for _, msg := range mock.calls[1] {
		for _, b := range msg.Blocks {
			if b.Type == llm.BlockToolResult && strings.Contains(b.Content, "cycle") {
				found = true
			}
		}
	}
	if !found {
		t.Error("self-delegation via the send_to_agent tool was not rejected with a cycle error")
	}
}
