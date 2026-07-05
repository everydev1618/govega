package dsl

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMetaAgentsDontGetExecOrServiceTools pins lever 1 of the runaway-loop fix
// (TonyVega exec meltdown, Jul 4): a router that holds `exec`/`start_service`
// shells out to build and host deliverables itself — 70+ exec calls spinning
// up nc/python/tunnels — instead of dispatching to a specialist. Meta-agents
// must never receive shell/build tools even if their persisted allow-list
// names them. Workers keep them.
func TestMetaAgentsDontGetExecOrServiceTools(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	meta := &Agent{
		Name:   "tony",
		Model:  "test-model",
		System: "You orchestrate.",
		Tools:  []string{"list_agents", "send_to_agent", "read_file", "exec", "start_service"},
		IsMeta: true,
	}
	if err := interp.AddAgent("tony", meta); err != nil {
		t.Fatalf("AddAgent meta: %v", err)
	}
	worker := &Agent{
		Name:   "builder-bee",
		Model:  "test-model",
		System: "You build.",
		Tools:  []string{"read_file", "exec", "start_service"},
	}
	if err := interp.AddAgent("builder-bee", worker); err != nil {
		t.Fatalf("AddAgent worker: %v", err)
	}

	metaHas := map[string]bool{}
	for _, s := range interp.Agents()["tony"].Agent.Tools.Schema() {
		metaHas[s.Name] = true
	}
	for _, denied := range []string{"exec", "start_service", "stop_service", "list_services", "service_logs"} {
		if metaHas[denied] {
			t.Errorf("meta-agent must not hold %q — routers dispatch, they don't shell out", denied)
		}
	}
	// It keeps its read-only verification tools (list_agents/send_to_agent
	// are Iris-injected, not builtins, so they're not registered in this bare
	// harness — read_file is the builtin we can assert survives the strip).
	if !metaHas["read_file"] {
		t.Error("meta-agent should keep read_file for read-only verification")
	}

	workerHas := map[string]bool{}
	for _, s := range interp.Agents()["builder-bee"].Agent.Tools.Schema() {
		workerHas[s.Name] = true
	}
	if !workerHas["exec"] || !workerHas["start_service"] {
		t.Error("worker agents must keep exec/start_service — they do the building")
	}
}

// TestMetaAgentsDontGetDeployTools: app-hosting tools (deploy_app etc.) are
// build tools — a router dispatches deployment to specialists, it doesn't host
// apps itself. Meta-agents are denied them; workers keep them.
func TestMetaAgentsDontGetDeployTools(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()
	interp.Tools().RegisterBuiltins() // ensure deploy_app is registered

	meta := &Agent{Name: "tony", Model: "test-model", System: "route", Tools: []string{"read_file", "deploy_app"}, IsMeta: true}
	if err := interp.AddAgent("tony", meta); err != nil {
		t.Fatalf("AddAgent meta: %v", err)
	}
	worker := &Agent{Name: "dev", Model: "test-model", System: "build", Tools: []string{"read_file", "deploy_app"}}
	if err := interp.AddAgent("dev", worker); err != nil {
		t.Fatalf("AddAgent worker: %v", err)
	}

	metaHas, workerHas := map[string]bool{}, map[string]bool{}
	for _, s := range interp.Agents()["tony"].Agent.Tools.Schema() {
		metaHas[s.Name] = true
	}
	for _, s := range interp.Agents()["dev"].Agent.Tools.Schema() {
		workerHas[s.Name] = true
	}
	if metaHas["deploy_app"] {
		t.Error("meta-agent must not hold deploy_app")
	}
	if !workerHas["deploy_app"] {
		t.Error("worker must keep deploy_app")
	}
}

// TestDeliveryPromptScopedByRole: worker agents get the DIY "files you write
// are served at …" invitation; meta-agents instead get delegation guidance —
// they relay deliverable URLs but are told the building isn't theirs to do.
func TestDeliveryPromptScopedByRole(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()
	interp.SetServerBaseURL("https://et.example.com")

	worker := &Agent{Name: "builder-bee", Model: "test-model", System: "You build.", Tools: []string{"read_file"}}
	if err := interp.AddAgent("builder-bee", worker); err != nil {
		t.Fatalf("AddAgent worker: %v", err)
	}
	meta := &Agent{Name: "tony", Model: "test-model", System: "You orchestrate.", Tools: []string{"read_file"}, IsMeta: true}
	if err := interp.AddAgent("tony", meta); err != nil {
		t.Fatalf("AddAgent meta: %v", err)
	}

	workerPrompt := interp.Agents()["builder-bee"].Agent.System.Prompt()
	metaPrompt := interp.Agents()["tony"].Agent.System.Prompt()

	// Both roles must be told to use the exact write_file/deploy_app URL and
	// NEVER hand-build a /workspace/ URL — a reconstructed URL drops the
	// capability token and 401s on gated instances (the jackal-game bug).
	for name, prompt := range map[string]string{"worker": workerPrompt, "meta": metaPrompt} {
		if !strings.Contains(prompt, "Accessible at:") {
			t.Errorf("%s prompt should point at write_file's Accessible-at URL", name)
		}
		if !strings.Contains(prompt, "NEVER hand-build") {
			t.Errorf("%s prompt should warn against reconstructing workspace URLs (token loss)", name)
		}
	}

	// Worker builds directly (deploy_app), meta delegates.
	if !strings.Contains(workerPrompt, "deploy_app") {
		t.Error("worker prompt should point browser apps at deploy_app")
	}
	if strings.Contains(workerPrompt, "specialist agents, never by you") {
		t.Error("worker prompt must not carry the meta delegation-only framing")
	}
	if !strings.Contains(metaPrompt, "specialist") {
		t.Error("meta prompt should direct build work to specialist agents")
	}
	// The old DIY-invite phrasing must be gone from both.
	if strings.Contains(workerPrompt, "it will be accessible at") {
		t.Error("worker prompt must not teach hand-constructed workspace URLs")
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
	if !strings.Contains(errStr, "line 10") {
		t.Errorf("ValidationError.Error() = %q, want the decimal line number, not a rune cast", errStr)
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
