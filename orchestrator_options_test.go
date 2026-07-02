package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

// alwaysToolLLM always returns a tool call, so the tool loop never terminates
// on its own — it can only stop at the iteration cap.
type alwaysToolLLM struct{}

func (alwaysToolLLM) Generate(context.Context, []llm.Message, []llm.ToolSchema) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{
		Content:   "working",
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "noop", Arguments: map[string]any{}}},
	}, nil
}

func (alwaysToolLLM) GenerateStream(context.Context, []llm.Message, []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// TestWithMaxIterationsCapsToolLoop verifies the spawn-level override actually
// bounds the tool loop (previously a documented no-op).
func TestWithMaxIterationsCapsToolLoop(t *testing.T) {
	ts := tools.NewTools()
	ts.Register("noop", func() string { return "ok" })

	o := NewOrchestrator(WithLLM(alwaysToolLLM{}))
	agent := Agent{Name: "looper", Tools: ts}

	p, err := o.Spawn(agent, WithMaxIterations(2))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = p.Send(context.Background(), "go")
	if !errors.Is(err, ErrMaxIterationsExceeded) {
		t.Fatalf("Send error = %v, want ErrMaxIterationsExceeded", err)
	}
}

// TestWithRateLimitsRejectsPerModel verifies per-model rate limiting is
// enforced (previously dead code — allow() was never called).
func TestWithRateLimitsRejectsPerModel(t *testing.T) {
	o := NewOrchestrator(
		WithLLM(costLLM{cost: 0}),
		WithRateLimits(map[string]RateLimitConfig{
			"rl-model": {RequestsPerMinute: 1, Strategy: RateLimitReject},
		}),
	)
	agent := Agent{Name: "limited", Model: "rl-model"}
	p, err := o.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// First call consumes the only token.
	if _, err := p.Send(context.Background(), "one"); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	// Second call within the same minute must be rejected.
	_, err = p.Send(context.Background(), "two")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second Send error = %v, want ErrRateLimited", err)
	}
}
