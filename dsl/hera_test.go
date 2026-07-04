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

// TestHeraAgent_ClassifyDefaultsToHaiku — Hera's first-turn intent
// extraction ("what do you want me to build?") is shallow
// classification work; Haiku is the right cost tier for it.
// Mid-conversation turns where she's actually generating an agent
// definition still fall back to Sonnet via Agent.Model.
func TestHeraAgent_ClassifyDefaultsToHaiku(t *testing.T) {
	def := HeraAgent(DefaultHeraConfig())
	if def.Models == nil {
		t.Fatal("HeraAgent should populate Models with sensible defaults, got nil")
	}
	if got := def.Models["classify"]; got != "claude-haiku-4-5-20251001" {
		t.Errorf("Models[classify] = %q, want claude-haiku-4-5-20251001", got)
	}
}

func TestHeraAgent_ExplicitModelsRespected(t *testing.T) {
	cfg := DefaultHeraConfig()
	cfg.Models = map[string]string{"classify": "claude-sonnet-4-6"}
	def := HeraAgent(cfg)
	if got := def.Models["classify"]; got != "claude-sonnet-4-6" {
		t.Errorf("explicit cfg.Models should win, got %q", got)
	}
}

func TestInjectHera(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	if err := InjectHera(interp, DefaultHeraConfig(), nil); err != nil {
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

	RegisterHeraTools(interp, DefaultHeraConfig(), cb)
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

// TestHeraCreateAgent_DefaultIdentityWhenSystemEmpty covers the #48 bug:
// agents created via Hera's create_agent tool with no system prompt were
// inheriting the orchestrator persona ("I'm Iris..."). The fix: inject
// "You are <name>." when system is empty, mirroring the HTTP handler
// behavior shipped in PR #50.
func TestHeraCreateAgent_DefaultIdentityWhenSystemEmpty(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":         "test-agent-02",
		"display_name": "Test Agent 02",
		"model":        "test-model",
		// No system prompt
	})
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}

	def, ok := interp.Document().Agents["test-agent-02"]
	if !ok {
		t.Fatal("agent not registered in document")
	}
	if def.System == "" {
		t.Error("system prompt empty — would inherit orchestrator persona (refs #48)")
	}
	if !strings.Contains(def.System, "Test Agent 02") && !strings.Contains(def.System, "test-agent-02") {
		t.Errorf("system prompt doesn't mention agent identity: %q", def.System)
	}
}

// TestHeraCreateAgent_FiltersMetaToolsWhenToolsEmpty covers the second
// half of #48: empty tools list led to spawnAgent giving the agent
// everything, including Hera/Iris meta-tools, which primed the LLM to
// roleplay as the orchestrator.
func TestHeraCreateAgent_FiltersMetaToolsWhenToolsEmpty(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
	// Iris meta-tools need to be registered so we can verify they're filtered out.
	RegisterIrisTools(interp, DefaultIrisConfig(), nil)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "no-tools-specified",
		"system": "You are no-tools-specified.",
		"model":  "test-model",
		// No tools
	})
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}

	def := interp.Document().Agents["no-tools-specified"]
	if len(def.Tools) == 0 {
		t.Fatal("def.Tools is empty — spawnAgent will fill it with everything " +
			"including meta-tools, which is the #48 root cause")
	}
	for _, tn := range def.Tools {
		if IsHeraTool(tn) {
			t.Errorf("Hera meta-tool %q leaked into composed agent", tn)
		}
		if IsIrisTool(tn) {
			t.Errorf("Iris meta-tool %q leaked into composed agent", tn)
		}
	}
}

func TestHeraCreateAgentProtectsHera(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
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

	RegisterHeraTools(interp, DefaultHeraConfig(), cb)
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

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
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

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
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

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
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

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
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

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
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
	def := HeraAgent(DefaultHeraConfig())
	// Default is current Sonnet — Opus is overkill for builder/orchestrator
	// reasoning and is ~3x slower. Apps wanting Opus override via cfg.Model.
	if def.Model != "claude-sonnet-4-6" {
		t.Errorf("default model = %q, want claude-sonnet-4-6", def.Model)
	}

	cfg := DefaultHeraConfig()
	cfg.Model = "custom-model"
	def = HeraAgent(cfg)
	if def.Model != "custom-model" {
		t.Errorf("model = %q, want custom-model", def.Model)
	}
}

// TestHeraCreateAgent_EmitsProvisioningEvents covers govega#56: the FE
// shows a placeholder agent card the moment provisioning starts and
// progressively fills it in as data lands. Hera's create_agent tool must
// emit at least three events to the caller-provided OnProvisioning hook:
//
//  1. phase=started — earliest signal; carries only the agent name.
//  2. phase=field_set — after the agent metadata is built; carries the
//     full snapshot so the FE can render the card.
//  3. phase=ready — after AddAgent has spawned the process and before
//     persistence completes.
//
// Order matters — the FE relies on "started" preceding "field_set".
func TestHeraCreateAgent_EmitsProvisioningEvents(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	var events []ProvisioningEvent
	cb := &HeraCallbacks{
		OnProvisioning: func(ev ProvisioningEvent) {
			events = append(events, ev)
		},
	}
	RegisterHeraTools(interp, DefaultHeraConfig(), cb)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":         "sofia",
		"display_name": "Sofia",
		"title":        "Content Strategist",
		"system":       "You are Sofia.",
		"model":        "test-model",
		"avatar":       "f1",
	})
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}

	wantPhases := []ProvisioningPhase{
		ProvisioningPhaseStarted,
		ProvisioningPhaseFieldSet,
		ProvisioningPhaseReady,
	}
	if len(events) < len(wantPhases) {
		t.Fatalf("got %d events, want at least %d: %+v", len(events), len(wantPhases), events)
	}
	for i, want := range wantPhases {
		if events[i].Phase != want {
			t.Errorf("event[%d].Phase = %q, want %q", i, events[i].Phase, want)
		}
		if events[i].Name != "sofia" {
			t.Errorf("event[%d].Name = %q, want sofia", i, events[i].Name)
		}
	}

	// field_set must carry a snapshot the FE can paint with.
	fieldSet := events[1]
	if fieldSet.Snapshot == nil {
		t.Fatal("field_set event has no Snapshot")
	}
	if fieldSet.Snapshot.DisplayName != "Sofia" {
		t.Errorf("snapshot DisplayName = %q, want Sofia", fieldSet.Snapshot.DisplayName)
	}
	if fieldSet.Snapshot.Title != "Content Strategist" {
		t.Errorf("snapshot Title = %q, want Content Strategist", fieldSet.Snapshot.Title)
	}
	if fieldSet.Snapshot.Icon == "" {
		t.Error("snapshot Icon empty — visual identity defaults should have applied")
	}
}

// TestHeraCreateAgent_EmitsFailedOnError ensures the FE can clear the
// placeholder when provisioning fails (e.g. AddAgent returns an error
// because the name already exists).
func TestHeraCreateAgent_EmitsFailedOnError(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()
	// Pre-register the agent so AddAgent fails with a duplicate.
	if err := interp.AddAgent("sofia", &Agent{Name: "sofia", Model: "test-model", System: "..."}); err != nil {
		t.Fatalf("preload: %v", err)
	}

	var events []ProvisioningEvent
	cb := &HeraCallbacks{
		OnProvisioning: func(ev ProvisioningEvent) {
			events = append(events, ev)
		},
	}
	RegisterHeraTools(interp, DefaultHeraConfig(), cb)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":   "sofia",
		"system": "You are Sofia.",
		"model":  "test-model",
		"avatar": "f1",
	})
	if err == nil {
		t.Fatal("expected error from duplicate AddAgent")
	}
	// Final event must be failed for the agent we tried to create.
	if len(events) == 0 {
		t.Fatal("no provisioning events emitted")
	}
	last := events[len(events)-1]
	if last.Phase != ProvisioningPhaseFailed {
		t.Errorf("last event phase = %q, want failed", last.Phase)
	}
	if last.Name != "sofia" {
		t.Errorf("last event name = %q, want sofia", last.Name)
	}
}

// TestHeraCreateAgent_DefaultsIconAndGradient covers govega#60: agents
// auto-spawned by the orchestrator (via Hera) must come with a populated
// icon + avatar_gradient so the FE doesn't render blank placeholders.
func TestHeraCreateAgent_DefaultsIconAndGradient(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	RegisterHeraTools(interp, DefaultHeraConfig(), nil)
	ctx := context.Background()

	_, err := interp.Tools().Execute(ctx, "create_agent", map[string]any{
		"name":         "sofia",
		"display_name": "Sofia",
		"system":       "You are Sofia.",
		"model":        "test-model",
		"avatar":       "f1",
	})
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}

	def := interp.Document().Agents["sofia"]
	if def == nil {
		t.Fatal("agent not registered")
	}
	if def.Icon == "" {
		t.Error("Icon empty; expected default")
	}
	if len(def.AvatarGradient) != 2 {
		t.Errorf("AvatarGradient = %v; want 2 stops", def.AvatarGradient)
	}
}

// TestHeraAgentPopulatesTitle covers govega#59: the builder meta-agent must
// carry a Title on its DSL def so /api/v1/agents surfaces "title": "Agent
// Builder" (or the tenant override) rather than leaving it empty.
func TestHeraAgentPopulatesTitle(t *testing.T) {
	def := HeraAgent(DefaultHeraConfig())
	if def.Title != "Agent Builder" {
		t.Errorf("default title = %q, want %q", def.Title, "Agent Builder")
	}

	cfg := DefaultHeraConfig()
	cfg.Title = "Forge Lead"
	def = HeraAgent(cfg)
	if def.Title != "Forge Lead" {
		t.Errorf("title override = %q, want %q", def.Title, "Forge Lead")
	}
}

// TestHeraPromptCoversDesignDiscipline guards the prompt rule that tells
// Hera to attach the design-impeccable skill (and the examples/skills
// directory) when she builds any UI/frontend/designer-style agent. Without
// this, spawned designers default to generic AI-styled output — purple
// gradients, glassmorphism, Inter on everything.
func TestHeraPromptCoversDesignDiscipline(t *testing.T) {
	def := HeraAgent(DefaultHeraConfig())
	prompt := def.System

	wants := []string{
		"design-impeccable",
		"skills_dirs",
		"frontend",
	}
	for _, w := range wants {
		if !strings.Contains(prompt, w) {
			t.Errorf("Hera prompt must reference %q so design discipline is attached to UI agents", w)
		}
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
