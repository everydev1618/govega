package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/everydev1618/govega/llm"
)

// costLLM returns a fixed per-call cost so budget accounting is deterministic.
type costLLM struct{ cost float64 }

func (m costLLM) Generate(context.Context, []llm.Message, []llm.ToolSchema) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: "ok", InputTokens: 1, OutputTokens: 1, CostUSD: m.cost}, nil
}

func (m costLLM) GenerateStream(context.Context, []llm.Message, []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// TestBudgetBlockStopsCallsOverLimit verifies BudgetBlock returns
// ErrBudgetExceeded once accumulated cost reaches the limit.
func TestBudgetBlockStopsCallsOverLimit(t *testing.T) {
	o := NewOrchestrator(WithLLM(costLLM{cost: 1.0}))
	agent := Agent{
		Name:   "spender",
		Budget: &Budget{Limit: 0.5, OnExceed: BudgetBlock},
	}
	p, err := o.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// First call is under budget (spent starts at 0) and commits 1.0.
	if _, err := p.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("first Send should succeed: %v", err)
	}

	// Second call must be blocked: accumulated 1.0 >= limit 0.5.
	_, err = p.Send(context.Background(), "again")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("second Send error = %v, want ErrBudgetExceeded", err)
	}
}

// TestBudgetWarnAllowsCallsOverLimit verifies BudgetWarn does not block.
func TestBudgetWarnAllowsCallsOverLimit(t *testing.T) {
	o := NewOrchestrator(WithLLM(costLLM{cost: 1.0}))
	agent := Agent{
		Name:   "warner",
		Budget: &Budget{Limit: 0.5, OnExceed: BudgetWarn},
	}
	p, err := o.Spawn(agent)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if _, err := p.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if _, err := p.Send(context.Background(), "again"); err != nil {
		t.Errorf("BudgetWarn must not block, got: %v", err)
	}
}

// TestNoBudgetIsUnlimited verifies a nil budget imposes no cap.
func TestNoBudgetIsUnlimited(t *testing.T) {
	o := NewOrchestrator(WithLLM(costLLM{cost: 100.0}))
	p, err := o.Spawn(Agent{Name: "free"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := p.Send(context.Background(), "hi"); err != nil {
			t.Fatalf("Send #%d with no budget: %v", i, err)
		}
	}
}
