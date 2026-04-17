package dsl

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

func newHeraTestInterpreter(t *testing.T) *Interpreter {
	t.Helper()
	doc := &Document{
		Name:   "MotherTest",
		Agents: make(map[string]*Agent),
		Settings: &Settings{
			DefaultModel: "test-model",
		},
	}

	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))

	toolSet := tools.NewTools()
	toolSet.RegisterBuiltins()

	return &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             toolSet,
		delegationConfigs: make(map[string]*DelegationDef),
	}
}

func TestInjectHera(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	if err := InjectHera(interp, nil); err != nil {
		t.Fatalf("InjectHera: %v", err)
	}

	// Hera should appear in the agent map.
	agents := interp.Agents()
	if _, ok := agents["hera"]; !ok {
		t.Fatal("hera agent should exist after InjectHera")
	}

	// Hera's definition should be in the document.
	if _, ok := interp.Document().Agents["hera"]; !ok {
		t.Fatal("hera definition should be in document")
	}
}

func TestHeraCreateAgent(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	var createdName string
	cb := &HeraCallbacks{
		OnAgentCreated: func(agent *Agent) error {
			createdName = agent.Name
			return nil
		},
	}

	RegisterHeraTools(interp, cb)
	ctx := context.Background()

	result, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "reviewer",
		"system": "You review code carefully.",
		"model":  "test-model",
		"tools":  []any{"read_file"},
	})
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}

	if !strings.Contains(result, "reviewer") {
		t.Errorf("result should mention agent name, got: %s", result)
	}

	// Verify agent was added.
	agents := interp.Agents()
	if _, ok := agents["reviewer"]; !ok {
		t.Fatal("reviewer agent should exist after create_agent")
	}

	// Verify callback fired.
	if createdName != "reviewer" {
		t.Errorf("OnAgentCreated name = %q, want %q", createdName, "reviewer")
	}
}

func TestHeraCreateAgentProtectsHera(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, nil)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "hera",
		"system": "Trying to overwrite hera",
	})
	if err == nil {
		t.Fatal("should not be able to create agent named 'hera'")
	}
}

func TestHeraDeleteAgent(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	var deletedName string
	cb := &HeraCallbacks{
		OnAgentDeleted: func(name string) {
			deletedName = name
		},
	}

	RegisterHeraTools(interp, cb)
	ctx := context.Background()

	// Create an agent first.
	interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "temp",
		"system": "Temporary agent.",
		"model":  "test-model",
	})

	// Delete it.
	result, err := interp.Tools().Execute(ctx, "delete_agent", map[string]any{
		"name": "temp",
	})
	if err != nil {
		t.Fatalf("delete_agent: %v", err)
	}

	if !strings.Contains(result, "temp") {
		t.Errorf("result should mention agent name, got: %s", result)
	}

	// Verify agent was removed.
	if _, ok := interp.Agents()["temp"]; ok {
		t.Fatal("temp agent should not exist after delete_agent")
	}

	// Verify callback fired.
	if deletedName != "temp" {
		t.Errorf("OnAgentDeleted name = %q, want %q", deletedName, "temp")
	}
}

func TestHeraDeleteAgentProtectsHera(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, nil)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "delete_agent", map[string]any{
		"name": "hera",
	})
	if err == nil {
		t.Fatal("should not be able to delete Hera")
	}
}

func TestHeraUpdateAgent(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, nil)
	ctx := context.Background()

	// Create an agent.
	interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "helper",
		"system": "You help with things.",
		"model":  "test-model",
	})

	// Update its system prompt.
	result, err := interp.Tools().Execute(ctx, "update_agent", map[string]any{
		"name":   "helper",
		"system": "You help with things. Be extra friendly.",
	})
	if err != nil {
		t.Fatalf("update_agent: %v", err)
	}

	if !strings.Contains(result, "helper") {
		t.Errorf("result should mention agent name, got: %s", result)
	}

	// Verify definition updated.
	interp.mu.RLock()
	def := interp.doc.Agents["helper"]
	interp.mu.RUnlock()

	if def == nil {
		t.Fatal("helper should still exist after update")
	}
	if !strings.Contains(def.System, "extra friendly") {
		t.Errorf("system prompt should be updated, got: %s", def.System)
	}
}

func TestHeraListAgents(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, nil)
	ctx := context.Background()

	// Create two agents.
	interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "alice",
		"system": "You are Alice.",
		"model":  "test-model",
	})
	interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "bob",
		"system": "You are Bob.",
		"model":  "test-model",
	})

	result, err := interp.Tools().Execute(ctx, "list_agents", map[string]any{})
	if err != nil {
		t.Fatalf("list_agents: %v", err)
	}

	// Parse JSON output.
	var agents []map[string]any
	if err := json.Unmarshal([]byte(result), &agents); err != nil {
		t.Fatalf("list_agents returned invalid JSON: %v\nresult: %s", err, result)
	}

	names := make(map[string]bool)
	for _, a := range agents {
		if n, ok := a["name"].(string); ok {
			names[n] = true
		}
	}

	if !names["alice"] {
		t.Error("alice should be in the agent list")
	}
	if !names["bob"] {
		t.Error("bob should be in the agent list")
	}
}

func TestHeraListAvailableTools(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, nil)
	ctx := context.Background()

	result, err := interp.Tools().Execute(ctx, "list_available_tools", map[string]any{})
	if err != nil {
		t.Fatalf("list_available_tools: %v", err)
	}

	// Should return valid JSON.
	var tools []map[string]any
	if err := json.Unmarshal([]byte(result), &tools); err != nil {
		t.Fatalf("returned invalid JSON: %v", err)
	}

	// Should include built-in tools but NOT Hera's meta-tools.
	names := make(map[string]bool)
	for _, tool := range tools {
		if n, ok := tool["name"].(string); ok {
			names[n] = true
		}
	}

	if names["create_agent"] {
		t.Error("Hera's meta-tools should be excluded from the list")
	}

	// Built-in tools should be present (registered via RegisterBuiltins).
	if len(tools) == 0 {
		t.Error("should have some built-in tools")
	}
}

func TestHeraListMCPRegistry(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, nil)
	ctx := context.Background()

	result, err := interp.Tools().Execute(ctx, "list_mcp_registry", map[string]any{})
	if err != nil {
		t.Fatalf("list_mcp_registry: %v", err)
	}

	var servers []map[string]any
	if err := json.Unmarshal([]byte(result), &servers); err != nil {
		t.Fatalf("returned invalid JSON: %v", err)
	}

	// Should have entries from DefaultRegistry.
	if len(servers) == 0 {
		t.Error("MCP registry should have entries")
	}

	// Check that "github" is present.
	found := false
	for _, s := range servers {
		if s["name"] == "github" {
			found = true
			break
		}
	}
	if !found {
		t.Error("github should be in the MCP registry")
	}
}

func TestHeraAgentDefaults(t *testing.T) {
	def := HeraAgent("")
	if def.Model != "claude-opus-4-20250514" {
		t.Errorf("default model = %q, want claude-opus-4-20250514", def.Model)
	}

	def = HeraAgent("custom-model")
	if def.Model != "custom-model" {
		t.Errorf("model = %q, want custom-model", def.Model)
	}
}

func TestIsHeraTool(t *testing.T) {
	if !IsHeraTool("create_agent") {
		t.Error("create_agent should be a hera tool")
	}
	if IsHeraTool("read_file") {
		t.Error("read_file should not be a hera tool")
	}
}
