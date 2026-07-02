package dsl

import (
	"context"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// newMetaGuardInterp builds an interpreter with a non-meta worker and a
// meta system-agent, both spawned.
func newMetaGuardInterp(t *testing.T) *Interpreter {
	t.Helper()
	yamlStr := `
version: 1
agents:
  worker:
    model: test
    system: I am a worker.
  charlie:
    model: test
    system: I am the orchestrator.
`
	doc, err := NewParser().Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Mark charlie as a system (meta) agent, as Hera/Iris injection would.
	doc.Agents["charlie"].IsMeta = true

	orch := vega.NewOrchestrator(vega.WithLLM(&recordingLLM{response: "ok"}))
	t.Cleanup(func() { _ = orch.Shutdown(context.Background()) })

	interp := &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             tools.NewTools(),
		delegationConfigs: make(map[string]*DelegationDef),
	}
	for name, def := range doc.Agents {
		if err := interp.spawnAgent(name, def); err != nil {
			t.Fatalf("spawnAgent(%s): %v", name, err)
		}
	}
	return interp
}

// TestIsMetaAgent verifies the meta-agent predicate that gates the Hera
// delete_agent/update_agent tools. RemoveAgent itself stays unguarded so
// authenticated admin flows (e.g. renaming the orchestrator) still work.
func TestIsMetaAgent(t *testing.T) {
	interp := newMetaGuardInterp(t)

	if !interp.IsMetaAgent("charlie") {
		t.Error("IsMetaAgent(charlie) = false, want true (marked IsMeta)")
	}
	if interp.IsMetaAgent("worker") {
		t.Error("IsMetaAgent(worker) = true, want false")
	}

	// RemoveAgent remains usable for meta agents by admin callers.
	if err := interp.RemoveAgent("charlie"); err != nil {
		t.Errorf("RemoveAgent(charlie) error = %v, want nil (admin path unguarded)", err)
	}
}

// TestSendToAgentBlocksNonMetaToMeta verifies a worker agent cannot delegate
// into a system agent (privilege escalation), while top-level calls still can.
func TestSendToAgentBlocksNonMetaToMeta(t *testing.T) {
	interp := newMetaGuardInterp(t)

	worker, err := interp.EnsureAgent("worker")
	if err != nil {
		t.Fatalf("EnsureAgent(worker): %v", err)
	}

	// Delegation from inside the worker's tool loop → meta target must fail.
	ctx := vega.ContextWithProcess(context.Background(), worker)
	if _, err := interp.SendToAgent(ctx, "charlie", "do admin things"); err == nil {
		t.Error("SendToAgent(worker -> meta) returned nil, want error")
	} else if !strings.Contains(err.Error(), "system agent") {
		t.Errorf("error = %v, want it to mention system agent", err)
	}

	// A top-level call (no caller process in context) is allowed.
	if _, err := interp.SendToAgent(context.Background(), "charlie", "hello"); err != nil {
		t.Errorf("top-level SendToAgent(charlie) error = %v, want nil", err)
	}
}
