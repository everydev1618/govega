package dsl

import (
	"context"
	"fmt"
	"sync"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// TestDocAgentsConcurrentAccess exercises concurrent writers (AddAgent/
// RemoveAgent) against readers (HasAgent, ReactiveTriggers, ensureAgent) to
// catch data races on i.doc.Agents. Run with -race. Regression test for M4.
func TestDocAgentsConcurrentAccess(t *testing.T) {
	yamlStr := `
version: 1
agents:
  base:
    model: test
    system: base agent
`
	doc, err := NewParser().Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	orch := vega.NewOrchestrator(vega.WithLLM(&recordingLLM{response: "ok"}))
	t.Cleanup(func() { _ = orch.Shutdown(context.Background()) })

	interp := &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             tools.NewTools(),
		delegationConfigs: make(map[string]*DelegationDef),
	}
	if err := interp.spawnAgent("base", doc.Agents["base"]); err != nil {
		t.Fatalf("spawnAgent(base): %v", err)
	}

	var wg sync.WaitGroup

	// Writers: add and remove composed agents.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				name := fmt.Sprintf("w%d_%d", w, i)
				_ = interp.AddAgent(name, &Agent{Name: name, Model: "test", System: "x"})
				_ = interp.RemoveAgent(name)
			}
		}(w)
	}

	// Readers: touch the definition map through the guarded accessors.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				interp.HasAgent("base")
				interp.ReactiveTriggers()
				_, _ = interp.EnsureAgent("base")
			}
		}()
	}

	wg.Wait()
}
