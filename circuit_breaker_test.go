package vega

import (
	"context"
	"strings"
	"testing"

	"github.com/everydev1618/govega/tools"
)

// TestToolLoopCircuitBreakerTripsOnThrash pins the runaway-loop backstop
// (TonyVega exec meltdown, Jul 4): an agent that keeps making the SAME tool
// call and getting the SAME result is looping without progress. Rather than
// grind to the 100-iteration ceiling (ErrMaxIterationsExceeded, no useful
// output), the breaker trips early and ends the turn with an honest message
// naming the stuck tool. This is the Erlang max-restart-intensity idea applied
// to the tool loop.
func TestToolLoopCircuitBreakerTripsOnThrash(t *testing.T) {
	ts := tools.NewTools()
	ts.Register("noop", func() string { return "ok" })

	o := NewOrchestrator(WithLLM(alwaysToolLLM{})) // default MaxIterations = 100
	p, err := o.Spawn(Agent{Name: "looper", Tools: ts})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	out, err := p.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("breaker should end the turn cleanly, got error: %v", err)
	}
	if !strings.Contains(out, "noop") {
		t.Errorf("thrash message should name the stuck tool, got: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "loop") {
		t.Errorf("thrash message should explain the loop, got: %q", out)
	}
	// Must trip within a few iterations, nowhere near the 100 cap.
	if n := p.Metrics().ToolCalls; n > 10 {
		t.Errorf("breaker should trip within a few iterations, ran %d tool calls", n)
	}
}
