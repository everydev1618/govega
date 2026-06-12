package dsl

import (
	"testing"

	vega "github.com/everydev1618/govega"
)

// TestResetAllAgents_RespawnPicksUpNewMCPTools — spawnAgent's Filter() takes
// a snapshot of the global tool collection at injection time, so MCP tools
// that register later (e.g. a Composio server wired up after an OAuth
// consent) are invisible to already-spawned agents (same bug class as
// govega#57). ResetAllAgents kills every live process while keeping the
// definitions, so the next EnsureAgent respawns with a fresh snapshot that
// includes the new tools.
func TestResetAllAgents_RespawnPicksUpNewMCPTools(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	// Explicit Tools list — that's the snapshotted path. Agents with an
	// empty list share the live collection and don't have this bug; real
	// agents (riley's YAML, composed agents, Iris/Hera) all carry a list.
	if err := interp.AddAgent("scout", &Agent{Name: "scout", Model: "test-model", Tools: []string{"read_file"}}); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	before, err := interp.EnsureAgent("scout")
	if err != nil {
		t.Fatalf("EnsureAgent: %v", err)
	}

	// An MCP-style tool registers after the agent spawned — the "__" infix
	// puts it in the always-available bucket every agent gets at spawn.
	toolName := "composio_github__GITHUB_CREATE_AN_ISSUE"
	if err := interp.Tools().Register(toolName, func(repo string) (string, error) {
		return "", nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if agentHasTool(before, toolName) {
		t.Fatalf("expected the live process's tool snapshot to NOT include %s — has the snapshot semantics changed?", toolName)
	}

	interp.ResetAllAgents()

	after, err := interp.EnsureAgent("scout")
	if err != nil {
		t.Fatalf("EnsureAgent after reset: %v", err)
	}
	if after == before {
		t.Fatal("expected a fresh process after ResetAllAgents, got the old one")
	}
	if !agentHasTool(after, toolName) {
		t.Fatalf("respawned agent's tools should include %s", toolName)
	}
}

// TestResetAllAgents_KeepsDefinitions — reset must not remove agents from
// the document; that's RemoveAgent's job (explicit deletes).
func TestResetAllAgents_KeepsDefinitions(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	if err := interp.AddAgent("scout", &Agent{Name: "scout", Model: "test-model"}); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	if _, err := interp.EnsureAgent("scout"); err != nil {
		t.Fatalf("EnsureAgent: %v", err)
	}

	interp.ResetAllAgents()

	if _, ok := interp.Document().Agents["scout"]; !ok {
		t.Fatal("ResetAllAgents must keep agent definitions in the document")
	}
}

func agentHasTool(proc *vega.Process, name string) bool {
	if proc == nil || proc.Agent == nil || proc.Agent.Tools == nil {
		return false
	}
	for _, schema := range proc.Agent.Tools.Schema() {
		if schema.Name == name {
			return true
		}
	}
	return false
}
