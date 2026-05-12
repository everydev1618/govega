package dsl

import (
	"context"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
	"github.com/everydev1618/govega/tools"
)

// ---------- Integration: Parser → Interpreter wiring ----------

func TestInterpreterStoresDelegationConfig(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      context_window: 6
      blackboard: true
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// Verify delegation config was stored for dan
	interp.mu.RLock()
	config := interp.delegationConfigs["dan"]
	interp.mu.RUnlock()

	if config == nil {
		t.Fatal("delegation config for 'dan' should be stored")
	}
	if config.ContextWindow != 6 {
		t.Errorf("ContextWindow = %d, want 6", config.ContextWindow)
	}
	if !config.Blackboard {
		t.Error("Blackboard should be true")
	}

	// ann should NOT have a delegation config (she has no team)
	interp.mu.RLock()
	annConfig := interp.delegationConfigs["ann"]
	interp.mu.RUnlock()

	if annConfig != nil {
		t.Error("ann should not have a delegation config")
	}
}

func TestInterpreterNoDelegationConfig(t *testing.T) {
	yaml := `
name: Test
agents:
  worker:
    model: test-model
    system: You are a worker.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	interp.mu.RLock()
	config := interp.delegationConfigs["worker"]
	interp.mu.RUnlock()

	if config != nil {
		t.Error("worker with no team should not have delegation config")
	}
}

// ---------- Integration: Team group auto-creation ----------

func TestInterpreterCreatesTeamGroup(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      context_window: 3
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// Verify team:dan group was created
	group, ok := interp.orch.GetGroup("team:dan")
	if !ok {
		t.Fatal("team:dan group should exist")
	}

	// Both dan and ann should be members
	members := group.Members()
	if len(members) != 2 {
		t.Fatalf("team:dan group should have 2 members, got %d", len(members))
	}

	names := make(map[string]bool)
	for _, m := range members {
		if m.Agent != nil {
			names[m.Agent.Name] = true
		}
	}
	if !names["dan"] {
		t.Error("dan should be in team:dan group")
	}
	if !names["ann"] {
		t.Error("ann should be in team:dan group")
	}
}

func TestInterpreterNoGroupWithoutTeam(t *testing.T) {
	yaml := `
name: Test
agents:
  solo:
    model: test-model
    system: You work alone.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	_, ok := interp.orch.GetGroup("team:solo")
	if ok {
		t.Error("solo agent should not have a team group")
	}
}

// ---------- Integration: Blackboard tools registered ----------

func TestInterpreterRegistersBlackboardTools(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      blackboard: true
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	schema := interp.tools.Schema()
	toolNames := make(map[string]bool)
	for _, s := range schema {
		toolNames[s.Name] = true
	}

	for _, name := range []string{"bb_read", "bb_write", "bb_list"} {
		if !toolNames[name] {
			t.Errorf("tool %q should be registered when blackboard is enabled", name)
		}
	}
}

// TestInterpreterRegistersBlackboardForTeams asserts that bb_read/bb_write/
// bb_list are registered for every team-bound agent — the blackboard is
// the canonical "what's in flight, what's done" coordination store and
// must always be available so team members can avoid duplicating work.
//
// This replaced an earlier test that asserted bb_* were NOT registered
// unless explicitly opted in via delegation.blackboard=true. We changed
// the default: passive channels + always-on blackboard, because the
// previous setup (reactive channels + opt-in blackboard) produced
// runaway duplicate-work loops where each "I'm starting on the frontend"
// post triggered a fresh restart from every other team member.
func TestInterpreterRegistersBlackboardForTeams(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      context_window: 3
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	want := map[string]bool{"bb_read": false, "bb_write": false, "bb_list": false}
	for _, s := range interp.tools.Schema() {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q should be registered for team-bound agent (always-on blackboard)", name)
		}
	}
}

// ---------- Integration: Delegate tool registered ----------

func TestInterpreterRegistersDelegateTool(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	schema := interp.tools.Schema()
	found := false
	for _, s := range schema {
		if s.Name == "delegate" {
			found = true
			break
		}
	}
	if !found {
		t.Error("delegate tool should be registered when agent has a team")
	}
}

// ---------- Integration: System prompt enrichment ----------

func TestInterpreterEnrichesSystemPrompt(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      blackboard: true
  ann:
    model: test-model
    system: You are Ann. Chief of Staff.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// Get dan's process and check system prompt was enriched
	interp.mu.RLock()
	danProc := interp.agents["dan"]
	interp.mu.RUnlock()

	if danProc == nil {
		t.Fatal("dan process should exist")
	}

	// The agent's system prompt should mention the team and blackboard
	systemStr := danProc.Agent.System.Prompt()
	if !strings.Contains(systemStr, "ann") {
		t.Error("dan's system prompt should mention team member ann")
	}
	if !strings.Contains(systemStr, "Shared Blackboard") {
		t.Error("dan's system prompt should mention shared blackboard")
	}
	if !strings.Contains(systemStr, "bb_write") {
		t.Error("dan's system prompt should mention bb_write")
	}
}

func TestInterpreterComposesNormIntoSystemPrompt(t *testing.T) {
	yaml := `
name: Test
agents:
  guide:
    model: test-model
    system: You are a writing guide.
    norm: llm_friendly
  plain:
    model: test-model
    system: You are a plain agent.

norms:
  llm_friendly:
    description: Karpathy norm
    system: |
      Atomic sections with stable anchor ids.
      One claim per paragraph.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	interp.mu.RLock()
	guideProc := interp.agents["guide"]
	plainProc := interp.agents["plain"]
	interp.mu.RUnlock()

	if guideProc == nil || plainProc == nil {
		t.Fatal("both processes should exist")
	}

	guideStr := guideProc.Agent.System.Prompt()
	if !strings.Contains(guideStr, "Atomic sections with stable anchor ids") {
		t.Errorf("guide system prompt should contain norm body, got:\n%s", guideStr)
	}
	if !strings.Contains(guideStr, "You are a writing guide.") {
		t.Errorf("guide system prompt should still contain base prompt, got:\n%s", guideStr)
	}

	plainStr := plainProc.Agent.System.Prompt()
	if strings.Contains(plainStr, "Atomic sections") {
		t.Errorf("plain agent (no norm) should NOT contain norm body, got:\n%s", plainStr)
	}
}

func TestInterpreterTeamPromptWithoutBlackboard(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	interp.mu.RLock()
	danProc := interp.agents["dan"]
	interp.mu.RUnlock()

	systemStr := danProc.Agent.System.Prompt()
	if !strings.Contains(systemStr, "delegate") {
		t.Error("dan's system prompt should mention delegate tool")
	}
	if strings.Contains(systemStr, "Shared Blackboard") {
		t.Error("dan's system prompt should NOT mention blackboard when disabled")
	}
}

// ---------- Integration: Context enrichment in delegation ----------

func TestDelegateToolEnrichesMessageWithContext(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      context_window: 3
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// Verify the delegation config was stored
	interp.mu.RLock()
	config := interp.delegationConfigs["dan"]
	interp.mu.RUnlock()

	if config == nil {
		t.Fatal("delegation config for dan should exist")
	}

	// Create a process with pre-populated messages to test enrichment
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "I struggle with delegation"},
		{Role: llm.RoleAssistant, Content: "Classic Operator's Trap"},
		{Role: llm.RoleUser, Content: "What should I do?"},
	}
	agent := vega.Agent{Name: "dan", Model: "test", System: vega.StaticPrompt("test")}
	proc, err := interp.orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Test the enrichment logic with the stored config
	dc := ExtractCallerContext(proc, config)
	if dc == nil {
		t.Fatal("should extract context from dan's messages")
	}

	enriched := FormatDelegationContext(dc, "Schedule follow-up with Marcus")
	if !strings.Contains(enriched, "<delegation_context>") {
		t.Error("enriched message should contain delegation_context tag")
	}
	if !strings.Contains(enriched, "dan") {
		t.Error("enriched message should identify caller as dan")
	}
	if !strings.Contains(enriched, "I struggle with delegation") {
		t.Error("enriched message should include conversation context")
	}
	if !strings.Contains(enriched, "<task>") {
		t.Error("enriched message should wrap task in task tag")
	}
	if !strings.Contains(enriched, "Schedule follow-up with Marcus") {
		t.Error("enriched message should preserve original task")
	}
}

func TestDelegateToolNoContextWhenNotConfigured(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// dan has no delegation config, so context extraction should return nil
	interp.mu.RLock()
	config := interp.delegationConfigs["dan"]
	interp.mu.RUnlock()

	if config != nil {
		t.Error("dan without delegation block should have nil config")
	}

	// FormatDelegationContext with nil should pass through
	result := FormatDelegationContext(nil, "Just do it")
	if result != "Just do it" {
		t.Errorf("nil context should pass through, got %q", result)
	}
}

// ---------- Integration: Blackboard shared via team group ----------

func TestBlackboardSharedViaTeamGroup(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      blackboard: true
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// Get the team group
	group, ok := interp.orch.GetGroup("team:dan")
	if !ok {
		t.Fatal("team:dan group should exist")
	}

	// Write to blackboard directly on group
	group.BBSet("plan", "ship v2")

	// Read back
	val, ok := group.BBGet("plan")
	if !ok || val != "ship v2" {
		t.Errorf("BBGet(plan) = %v, %v; want ship v2, true", val, ok)
	}

	// Both dan and ann processes should be in this group
	interp.mu.RLock()
	danProc := interp.agents["dan"]
	annProc := interp.agents["ann"]
	interp.mu.RUnlock()

	if !group.Has(danProc) {
		t.Error("dan should be in team:dan group")
	}
	if !group.Has(annProc) {
		t.Error("ann should be in team:dan group")
	}
}

// ---------- Integration: teamGroupResolver ----------

func TestTeamGroupResolverFindsGroup(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      blackboard: true
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	resolver := interp.teamGroupResolver("team:dan")

	// Create context with dan's process
	interp.mu.RLock()
	danProc := interp.agents["dan"]
	interp.mu.RUnlock()

	ctx := vega.ContextWithProcess(context.Background(), danProc)
	group := resolver(ctx)

	if group == nil {
		t.Fatal("resolver should find team:dan group for dan's process")
	}
	if group.Name() != "team:dan" {
		t.Errorf("group name = %q, want %q", group.Name(), "team:dan")
	}
}

func TestTeamGroupResolverFallsBackToDefault(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
    delegation:
      blackboard: true
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	resolver := interp.teamGroupResolver("team:dan")

	// Context without a process should fall back to default group
	group := resolver(context.Background())
	if group == nil {
		t.Fatal("resolver should fall back to default group")
	}
	if group.Name() != "team:dan" {
		t.Errorf("fallback group name = %q, want %q", group.Name(), "team:dan")
	}
}

// ---------- Integration: registerToolIfAbsent idempotency ----------

func TestRegisterToolIfAbsentIdempotent(t *testing.T) {
	yaml := `
name: Test
agents:
  solo:
    model: test-model
    system: You are solo.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	// Register a tool
	interp.registerToolIfAbsent("test_tool", tools.ToolDef{
		Description: "first",
		Fn:          func(ctx context.Context, params map[string]any) (string, error) { return "first", nil },
		Params:      map[string]tools.ParamDef{},
	})

	// Register again with same name — should be no-op
	interp.registerToolIfAbsent("test_tool", tools.ToolDef{
		Description: "second",
		Fn:          func(ctx context.Context, params map[string]any) (string, error) { return "second", nil },
		Params:      map[string]tools.ParamDef{},
	})

	// Verify only one tool with that name exists
	count := 0
	for _, s := range interp.tools.Schema() {
		if s.Name == "test_tool" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("should have exactly 1 test_tool, got %d", count)
	}
}

// ---------- Integration: Process group membership tracking ----------

func TestProcessKnowsItsGroups(t *testing.T) {
	yaml := `
name: Test
agents:
  dan:
    model: test-model
    system: You are Dan.
    team: [ann]
  ann:
    model: test-model
    system: You are Ann.
`
	doc := mustParse(t, yaml)

	interp := newTestInterpreter(t, doc)
	defer interp.Shutdown()

	interp.mu.RLock()
	annProc := interp.agents["ann"]
	interp.mu.RUnlock()

	groups := annProc.Groups()
	found := false
	for _, g := range groups {
		if g == "team:dan" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ann should be in team:dan group, groups = %v", groups)
	}
}

// ---------- Helpers ----------

func mustParse(t *testing.T, yamlStr string) *Document {
	t.Helper()
	p := NewParser()
	doc, err := p.Parse([]byte(yamlStr))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func newTestInterpreter(t *testing.T, doc *Document) *Interpreter {
	t.Helper()
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))

	toolSet := tools.NewTools()
	toolSet.RegisterBuiltins()

	interp := &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             toolSet,
		delegationConfigs: make(map[string]*DelegationDef),
	}

	// Spawn all agents
	for name, agentDef := range doc.Agents {
		if err := interp.spawnAgent(name, agentDef); err != nil {
			t.Fatalf("spawnAgent(%s): %v", name, err)
		}
	}

	return interp
}
