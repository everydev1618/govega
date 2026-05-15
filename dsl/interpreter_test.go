package dsl

import (
	"context"
	"testing"
	"time"

	"github.com/everydev1618/govega/tools"
)

// TestAgentInheritsSandboxTools covers the localhost-URL regression
// (https://everydev.v39a.com/chat/nadia, May 14): agents created before
// govega@55fd33a have a persisted Tools allow-list that doesn't mention
// spawn_app/run_in_app/write_file_to_app/destroy_app. spawnAgent's filter
// (interpreter.go:423) drops anything not on the list, so even with
// FLY_SANDBOX_TOKEN present the agent never sees the sandbox surface and
// falls back to `start_service` + `python -m http.server`, producing a
// localhost URL. Sandbox tools belong in the always-available bucket
// alongside `__`-prefixed server tools.
func TestAgentInheritsSandboxTools(t *testing.T) {
	t.Setenv("FLY_SANDBOX_TOKEN", "fake-token")

	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	// Re-register sandbox tools so the FLY_SANDBOX_TOKEN we just set takes
	// effect (newHeraTestInterpreter built its Tools before t.Setenv).
	tools.RegisterSandboxTools(interp.Tools())

	// Simulate a longstanding agent: persisted Tools list with only the
	// pre-sandbox-era allowance. No spawn_app, no run_in_app, etc.
	def := &Agent{
		Name:   "nadia",
		Model:  "test-model",
		System: "You are nadia.",
		Tools:  []string{"read_file", "write_file", "exec"},
	}
	if err := interp.AddAgent("nadia", def); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	proc, ok := interp.Agents()["nadia"]
	if !ok || proc.Agent == nil || proc.Agent.Tools == nil {
		t.Fatal("nadia process should be spawned with a Tools collection")
	}

	have := map[string]bool{}
	for _, s := range proc.Agent.Tools.Schema() {
		have[s.Name] = true
	}
	for _, name := range []string{"spawn_app", "run_in_app", "write_file_to_app", "destroy_app"} {
		if !have[name] {
			t.Errorf("agent surface missing %q — longstanding agents won't get the sandbox path", name)
		}
	}
}

// TestEvictIdle_PreservesDocAgents covers govega#76: idle eviction was
// calling RemoveAgent which deletes from BOTH i.agents AND i.doc.Agents,
// so an evicted agent silently disappeared from list_agents — the exact
// scenario where scout vanished from the everydev tenant after the 09:00
// UTC cron firing went idle.
//
// The documented contract of evictIdle (see comment in interpreter.go)
// is: kill the process, leave the definition in doc.Agents so the agent
// respawns on next use. This test pins that contract.
func TestEvictIdle_PreservesDocAgents(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	// Add a composed agent via AddAgent — mirrors what restoreComposedAgents
	// does at boot. Critically NOT a yaml-defined agent (those are excluded
	// from eviction), and not IsMeta (also excluded).
	agentDef := &Agent{
		Name:   "scout",
		Model:  "test-model",
		System: "You are scout.",
		Tools:  []string{},
	}
	if err := interp.AddAgent("scout", agentDef); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	// Sanity: scout is registered in both maps post-AddAgent.
	if _, ok := interp.Agents()["scout"]; !ok {
		t.Fatal("scout should be in i.agents after AddAgent")
	}
	if _, ok := interp.Document().Agents["scout"]; !ok {
		t.Fatal("scout should be in doc.Agents after AddAgent")
	}

	// Force LastActiveAt to non-zero by sending one message through the
	// stub LLM path. evictIdle skips processes whose LastActiveAt is zero.
	ctx := context.Background()
	if _, err := interp.SendToAgent(ctx, "scout", "ping"); err != nil {
		t.Fatalf("SendToAgent: %v", err)
	}

	// Trigger eviction with idleTTL=0 so anything active >0ns ago evicts.
	interp.evictIdle(0)

	// Process should be gone from i.agents (eviction did its job).
	if _, ok := interp.Agents()["scout"]; ok {
		t.Error("expected scout's process to be evicted from i.agents")
	}

	// But the definition MUST remain in doc.Agents so:
	//   (a) list_agents still returns scout (Charlie can find it)
	//   (b) EnsureAgent respawns it on the next message
	// If this assertion fails, scout has effectively disappeared from the
	// roster even though composed_agents in SQLite still holds the row.
	if _, ok := interp.Document().Agents["scout"]; !ok {
		t.Error("evictIdle wiped scout from doc.Agents — list_agents will lose it")
	}
}

// TestAddAgent_AugmentsReadChannelForChannelMembers pins the auto-augmentation
// added when read_channel was introduced: any agent whose persisted tools list
// already mentions post_to_channel or list_my_channels should also get
// read_channel. Without this, existing DB-stored agents (from before the new
// tool existed) can only see truncated previews of teammate posts via
// check_status and have no way to fetch the full body.
func TestAddAgent_AugmentsReadChannelForChannelMembers(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	def := &Agent{
		Name:   "scout",
		Model:  "test-model",
		System: "You are scout.",
		Tools:  []string{"post_to_channel", "list_my_channels", "read_file"},
	}
	if err := interp.AddAgent("scout", def); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	stored := interp.Document().Agents["scout"]
	if stored == nil {
		t.Fatal("scout missing from doc.Agents after AddAgent")
	}
	has := func(name string) bool {
		for _, t := range stored.Tools {
			if t == name {
				return true
			}
		}
		return false
	}
	if !has("read_channel") {
		t.Errorf("expected read_channel auto-added to channel-using agent, got tools: %v", stored.Tools)
	}
	if !has("post_to_channel") {
		t.Errorf("auto-augmentation should not drop existing tools, got: %v", stored.Tools)
	}
}

func TestAddAgent_LeavesNonChannelAgentsAlone(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	def := &Agent{
		Name:   "loner",
		Model:  "test-model",
		System: "You are a solo agent.",
		Tools:  []string{"read_file", "exec"},
	}
	if err := interp.AddAgent("loner", def); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	stored := interp.Document().Agents["loner"]
	for _, name := range stored.Tools {
		if name == "read_channel" {
			t.Errorf("read_channel should not be added to agents without channel tools, got: %v", stored.Tools)
		}
	}
}

func TestAddAgent_DoesNotDuplicateReadChannel(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	def := &Agent{
		Name:   "remy",
		Model:  "test-model",
		System: "You are remy.",
		Tools:  []string{"post_to_channel", "list_my_channels", "read_channel"},
	}
	if err := interp.AddAgent("remy", def); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	stored := interp.Document().Agents["remy"]
	count := 0
	for _, name := range stored.Tools {
		if name == "read_channel" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected read_channel exactly once, got %d in: %v", count, stored.Tools)
	}
}

func TestExecutionContext(t *testing.T) {
	ctx := &ExecutionContext{
		Inputs:    map[string]any{"task": "test"},
		Variables: make(map[string]any),
		StartTime: time.Now(),
	}

	if ctx.Inputs["task"] != "test" {
		t.Errorf("ExecutionContext.Inputs[task] = %v, want %v", ctx.Inputs["task"], "test")
	}
}

func TestLoopState(t *testing.T) {
	ls := &LoopState{
		Index: 2,
		Count: 3,
		Item:  "item-2",
		First: false,
		Last:  false,
	}

	if ls.Index != 2 {
		t.Errorf("LoopState.Index = %d, want 2", ls.Index)
	}

	if ls.Item != "item-2" {
		t.Errorf("LoopState.Item = %v, want %v", ls.Item, "item-2")
	}
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		File:    "test.vega.yaml",
		Line:    10,
		Column:  5,
		Field:   "agents.coder.model",
		Message: "invalid model name",
		Hint:    "try 'claude-sonnet-4-20250514'",
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("ValidationError.Error() should not be empty")
	}
}

// TestInterpreterInterpolation tests the interpolation logic
func TestInterpreterInterpolation(t *testing.T) {
	// Create a minimal document for testing interpolation
	doc := &Document{
		Name:      "Test",
		Agents:    make(map[string]*Agent),
		Workflows: make(map[string]*Workflow),
	}

	// Test the copyMap helper
	original := map[string]any{
		"a": 1,
		"b": "two",
		"c": true,
	}

	copied := copyMap(original)

	if len(copied) != len(original) {
		t.Errorf("copyMap() length = %d, want %d", len(copied), len(original))
	}

	// Modify original shouldn't affect copy
	original["a"] = 999
	if copied["a"] == 999 {
		t.Error("copyMap() should create independent copy")
	}

	_ = doc // Use doc to avoid unused variable
}

// TestExpressionEvaluationPatterns tests common expression patterns
func TestExpressionEvaluationPatterns(t *testing.T) {
	tests := []struct {
		name     string
		template string
		vars     map[string]any
		want     string
	}{
		{
			name:     "simple variable",
			template: "Hello, {{name}}!",
			vars:     map[string]any{"name": "World"},
			want:     "Hello, World!",
		},
		{
			name:     "multiple variables",
			template: "{{greeting}}, {{name}}!",
			vars:     map[string]any{"greeting": "Hi", "name": "Alice"},
			want:     "Hi, Alice!",
		},
		{
			name:     "no variables",
			template: "Static text",
			vars:     map[string]any{},
			want:     "Static text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that the patterns we expect are detected
			if len(tt.vars) > 0 {
				if !ContainsExpression(tt.template) {
					t.Errorf("ContainsExpression(%q) should be true", tt.template)
				}
			}
		})
	}
}

// TestFilterPatterns tests the filter syntax parsing
func TestFilterPatterns(t *testing.T) {
	tests := []struct {
		expr       string
		hasFilter  bool
		filterName string
	}{
		{"name", false, ""},
		{"name | upper", true, "upper"},
		{"name | lower", true, "lower"},
		{"name | default:unknown", true, "default"},
		{"items | join:,", true, "join"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			hasFilter := containsFilter(tt.expr)
			if hasFilter != tt.hasFilter {
				t.Errorf("containsFilter(%q) = %v, want %v", tt.expr, hasFilter, tt.hasFilter)
			}
		})
	}
}

// Helper function for testing
func containsFilter(expr string) bool {
	for i := 0; i < len(expr); i++ {
		if expr[i] == '|' {
			return true
		}
	}
	return false
}

// TestStepTypeDetection tests that we can identify step types
func TestStepTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		step     Step
		stepType string
	}{
		{
			name:     "agent step",
			step:     Step{Agent: "coder", Send: "hello"},
			stepType: "agent",
		},
		{
			name:     "set step",
			step:     Step{Set: map[string]any{"x": 1}},
			stepType: "set",
		},
		{
			name:     "conditional step",
			step:     Step{Condition: "x > 0", Then: []Step{}},
			stepType: "conditional",
		},
		{
			name:     "parallel step",
			step:     Step{Parallel: []Step{{}, {}}},
			stepType: "parallel",
		},
		{
			name:     "return step",
			step:     Step{Return: "result"},
			stepType: "return",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectStepType(tt.step)
			if got != tt.stepType {
				t.Errorf("detectStepType() = %q, want %q", got, tt.stepType)
			}
		})
	}
}

// Helper function for testing
func detectStepType(step Step) string {
	switch {
	case step.Condition != "":
		return "conditional"
	case len(step.Parallel) > 0:
		return "parallel"
	case step.Repeat != nil:
		return "repeat"
	case step.ForEach != "":
		return "foreach"
	case step.Workflow != "":
		return "subworkflow"
	case step.Set != nil:
		return "set"
	case step.Return != "":
		return "return"
	case step.Agent != "":
		return "agent"
	default:
		return "unknown"
	}
}

// TestContextTimeout tests context timeout handling
func TestContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Simulate waiting for context to expire
	time.Sleep(20 * time.Millisecond)

	select {
	case <-ctx.Done():
		if ctx.Err() != context.DeadlineExceeded {
			t.Errorf("Context error = %v, want DeadlineExceeded", ctx.Err())
		}
	default:
		t.Error("Context should be done after timeout")
	}
}

// TestInputValidation tests workflow input validation
func TestInputValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   Input
		value   any
		wantErr bool
	}{
		{
			name:    "required with value",
			input:   Input{Type: "string", Required: true},
			value:   "hello",
			wantErr: false,
		},
		{
			name:    "required without value",
			input:   Input{Type: "string", Required: true},
			value:   nil,
			wantErr: true,
		},
		{
			name:    "optional without value",
			input:   Input{Type: "string", Required: false},
			value:   nil,
			wantErr: false,
		},
		{
			name:    "with default",
			input:   Input{Type: "string", Required: true, Default: "default"},
			value:   nil,
			wantErr: false, // Default should satisfy requirement
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInput(tt.input, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateInput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper function for testing
func validateInput(input Input, value any) error {
	if input.Required && value == nil && input.Default == nil {
		return &ValidationError{Message: "required input missing"}
	}
	return nil
}

// TestWorkflowOutput tests output evaluation patterns
func TestWorkflowOutput(t *testing.T) {
	tests := []struct {
		name   string
		output any
		isMap  bool
	}{
		{
			name:   "string output",
			output: "{{result}}",
			isMap:  false,
		},
		{
			name:   "map output",
			output: map[string]any{"code": "{{code}}", "review": "{{review}}"},
			isMap:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, isMap := tt.output.(map[string]any)
			if isMap != tt.isMap {
				t.Errorf("output type map = %v, want %v", isMap, tt.isMap)
			}
		})
	}
}

func TestInterpreterNormAccessor(t *testing.T) {
	yaml := `
name: norms accessor

agents:
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.

norms:
  llm_friendly:
    description: structured for LLM readers
    system: Atomic sections. One claim per paragraph.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	interp, err := NewInterpreter(doc, WithLazySpawn())
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	defer interp.Shutdown()

	got, ok := interp.Norm("llm_friendly")
	if !ok {
		t.Fatal(`Norm("llm_friendly") not found`)
	}
	if got.System != "Atomic sections. One claim per paragraph." {
		t.Errorf("Norm system = %q", got.System)
	}

	if _, ok := interp.Norm("nope"); ok {
		t.Error(`Norm("nope") should be missing`)
	}
}
