package dsl

import (
	"context"
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/llm"
)

// ---------- BuildTeamPrompt ----------

func TestBuildTeamPromptBasic(t *testing.T) {
	result := BuildTeamPrompt("You are a leader.", []string{"worker"}, map[string]string{
		"worker": "Does the work",
	}, false)

	if !strings.Contains(result, "worker") {
		t.Error("should mention team member")
	}
	if !strings.Contains(result, "Does the work") {
		t.Error("should include description")
	}
	if strings.Contains(result, "Shared Blackboard") {
		t.Error("should not mention blackboard when disabled")
	}
}

func TestBuildTeamPromptMultipleMembers(t *testing.T) {
	descs := map[string]string{
		"ann":  "Chief of Staff",
		"mark": "Sales Lead",
	}
	result := BuildTeamPrompt("Base.", []string{"ann", "mark"}, descs, false)

	if !strings.Contains(result, "**ann** — Chief of Staff") {
		t.Error("should format ann with description")
	}
	if !strings.Contains(result, "**mark** — Sales Lead") {
		t.Error("should format mark with description")
	}
}

func TestBuildTeamPromptNoDescriptions(t *testing.T) {
	result := BuildTeamPrompt("Base.", []string{"worker"}, nil, false)
	if !strings.Contains(result, "**worker**") {
		t.Error("should list member without description")
	}
}

func TestBuildTeamPromptMixedDescriptions(t *testing.T) {
	descs := map[string]string{
		"ann": "Chief of Staff",
		// mark has no description
	}
	result := BuildTeamPrompt("Base.", []string{"ann", "mark"}, descs, false)
	if !strings.Contains(result, "**ann** — Chief of Staff") {
		t.Error("should show ann with description")
	}
	if !strings.Contains(result, "**mark**\n") {
		t.Error("should show mark without description")
	}
}

func TestBuildTeamPromptWithBlackboard(t *testing.T) {
	result := BuildTeamPrompt("You are a leader.", []string{"worker"}, nil, true)

	for _, expected := range []string{"Shared Blackboard", "bb_write", "bb_read", "bb_list"} {
		if !strings.Contains(result, expected) {
			t.Errorf("should contain %q when blackboard enabled", expected)
		}
	}
}

func TestBuildTeamPromptEmpty(t *testing.T) {
	result := BuildTeamPrompt("Base prompt.", nil, nil, false)
	if result != "Base prompt." {
		t.Errorf("with no team should return system unchanged, got %q", result)
	}
}

func TestBuildTeamPromptPreservesSystem(t *testing.T) {
	system := "You are Dan Martell. Coach founders."
	result := BuildTeamPrompt(system, []string{"ann"}, nil, true)
	if !strings.HasPrefix(result, system) {
		t.Error("should preserve original system prompt as prefix")
	}
}

// ---------- ExtractCallerContext ----------

func TestExtractCallerContextNilProcess(t *testing.T) {
	dc := ExtractCallerContext(nil, &DelegationDef{ContextWindow: 5})
	if dc != nil {
		t.Error("nil process should return nil")
	}
}

func TestExtractCallerContextNilConfig(t *testing.T) {
	dc := ExtractCallerContext(&vega.Process{}, nil)
	if dc != nil {
		t.Error("nil config should return nil")
	}
}

func TestExtractCallerContextZeroWindow(t *testing.T) {
	dc := ExtractCallerContext(&vega.Process{}, &DelegationDef{ContextWindow: 0})
	if dc != nil {
		t.Error("ContextWindow=0 should return nil")
	}
}

func TestExtractCallerContextNegativeWindow(t *testing.T) {
	dc := ExtractCallerContext(&vega.Process{}, &DelegationDef{ContextWindow: -1})
	if dc != nil {
		t.Error("negative ContextWindow should return nil")
	}
}

func TestExtractCallerContextEmptyMessages(t *testing.T) {
	proc := &vega.Process{}
	dc := ExtractCallerContext(proc, &DelegationDef{ContextWindow: 3})
	if dc != nil {
		t.Error("empty messages should return nil")
	}
}

func TestExtractCallerContextWithMessages(t *testing.T) {
	// Use WithMessages to pre-populate a process.
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	defer orch.Shutdown(context.Background())

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "msg1"},
		{Role: llm.RoleAssistant, Content: "msg2"},
		{Role: llm.RoleUser, Content: "msg3"},
		{Role: llm.RoleAssistant, Content: "msg4"},
		{Role: llm.RoleUser, Content: "msg5"},
	}

	agent := vega.Agent{
		Name:   "dan",
		Model:  "test",
		System: vega.StaticPrompt("Test agent"),
	}
	proc, err := orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Window of 3 should take last 3 messages
	dc := ExtractCallerContext(proc, &DelegationDef{ContextWindow: 3})
	if dc == nil {
		t.Fatal("expected non-nil DelegationContext")
	}
	if dc.CallerAgent != "dan" {
		t.Errorf("CallerAgent = %q, want %q", dc.CallerAgent, "dan")
	}
	if len(dc.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(dc.Messages))
	}
	if dc.Messages[0].Content != "msg3" {
		t.Errorf("Messages[0].Content = %q, want %q", dc.Messages[0].Content, "msg3")
	}
	if dc.Messages[2].Content != "msg5" {
		t.Errorf("Messages[2].Content = %q, want %q", dc.Messages[2].Content, "msg5")
	}
}

func TestExtractCallerContextWindowLargerThanHistory(t *testing.T) {
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	defer orch.Shutdown(context.Background())

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "only-msg"},
	}

	agent := vega.Agent{Name: "a", Model: "test", System: vega.StaticPrompt("test")}
	proc, err := orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Window=10 but only 1 message — should return all
	dc := ExtractCallerContext(proc, &DelegationDef{ContextWindow: 10})
	if dc == nil {
		t.Fatal("expected non-nil")
	}
	if len(dc.Messages) != 1 {
		t.Errorf("len(Messages) = %d, want 1", len(dc.Messages))
	}
}

func TestExtractCallerContextWindowExact(t *testing.T) {
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	defer orch.Shutdown(context.Background())

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
		{Role: llm.RoleUser, Content: "c"},
	}
	agent := vega.Agent{Name: "a", Model: "test", System: vega.StaticPrompt("test")}
	proc, err := orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Window exactly matches message count
	dc := ExtractCallerContext(proc, &DelegationDef{ContextWindow: 3})
	if dc == nil {
		t.Fatal("expected non-nil")
	}
	if len(dc.Messages) != 3 {
		t.Errorf("len(Messages) = %d, want 3", len(dc.Messages))
	}
	if dc.Messages[0].Content != "a" {
		t.Errorf("first message should be 'a', got %q", dc.Messages[0].Content)
	}
}

func TestExtractCallerContextRoleFilter(t *testing.T) {
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	defer orch.Shutdown(context.Background())

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleUser, Content: "u3"},
	}

	agent := vega.Agent{Name: "dan", Model: "test", System: vega.StaticPrompt("test")}
	proc, err := orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Only include user messages, window of 2
	dc := ExtractCallerContext(proc, &DelegationDef{
		ContextWindow: 2,
		IncludeRoles:  []string{"user"},
	})
	if dc == nil {
		t.Fatal("expected non-nil")
	}
	if len(dc.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(dc.Messages))
	}
	// Should be last 2 user messages: u2, u3
	if dc.Messages[0].Content != "u2" {
		t.Errorf("Messages[0] = %q, want %q", dc.Messages[0].Content, "u2")
	}
	if dc.Messages[1].Content != "u3" {
		t.Errorf("Messages[1] = %q, want %q", dc.Messages[1].Content, "u3")
	}
}

func TestExtractCallerContextRoleFilterFiltersAll(t *testing.T) {
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	defer orch.Shutdown(context.Background())

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleUser, Content: "u2"},
	}

	agent := vega.Agent{Name: "a", Model: "test", System: vega.StaticPrompt("test")}
	proc, err := orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Only include "system" role — no messages match
	dc := ExtractCallerContext(proc, &DelegationDef{
		ContextWindow: 10,
		IncludeRoles:  []string{"system"},
	})
	if dc != nil {
		t.Error("should return nil when role filter eliminates all messages")
	}
}

func TestExtractCallerContextAgentName(t *testing.T) {
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	defer orch.Shutdown(context.Background())

	history := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}

	// Agent with explicit name
	agent := vega.Agent{Name: "dan", Model: "test", System: vega.StaticPrompt("test")}
	proc, err := orch.Spawn(agent, vega.WithMessages(history))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	dc := ExtractCallerContext(proc, &DelegationDef{ContextWindow: 5})
	if dc.CallerAgent != "dan" {
		t.Errorf("CallerAgent = %q, want %q", dc.CallerAgent, "dan")
	}
}

// ---------- FormatDelegationContext ----------

func TestFormatDelegationContextNil(t *testing.T) {
	result := FormatDelegationContext(nil, "Do the thing")
	if result != "Do the thing" {
		t.Errorf("nil context should return original, got %q", result)
	}
}

func TestFormatDelegationContextEmptyMessages(t *testing.T) {
	dc := &DelegationContext{CallerAgent: "dan", Messages: []llm.Message{}}
	result := FormatDelegationContext(dc, "Do the thing")
	if result != "Do the thing" {
		t.Errorf("empty messages should return original, got %q", result)
	}
}

func TestFormatDelegationContextSingleMessage(t *testing.T) {
	dc := &DelegationContext{
		CallerAgent: "dan",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "Help me."}},
	}
	result := FormatDelegationContext(dc, "Do it")

	expected := []string{
		"<delegation_context>",
		"<from>dan</from>",
		"[user]: Help me.",
		"</recent_conversation>",
		"</delegation_context>",
		"<task>",
		"Do it",
		"</task>",
	}
	for _, s := range expected {
		if !strings.Contains(result, s) {
			t.Errorf("result should contain %q", s)
		}
	}
}

func TestFormatDelegationContextMultipleMessages(t *testing.T) {
	dc := &DelegationContext{
		CallerAgent: "dan",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "I'm struggling to delegate."},
			{Role: llm.RoleAssistant, Content: "Classic. You're in the Operator's Trap."},
			{Role: llm.RoleUser, Content: "What should I do?"},
		},
	}
	result := FormatDelegationContext(dc, "Schedule a follow-up with Marcus")

	// Verify message order
	idxUser1 := strings.Index(result, "[user]: I'm struggling")
	idxAssist := strings.Index(result, "[assistant]: Classic")
	idxUser2 := strings.Index(result, "[user]: What should")
	idxTask := strings.Index(result, "<task>")

	if idxUser1 == -1 || idxAssist == -1 || idxUser2 == -1 || idxTask == -1 {
		t.Fatalf("missing expected content in:\n%s", result)
	}
	if !(idxUser1 < idxAssist && idxAssist < idxUser2 && idxUser2 < idxTask) {
		t.Error("messages should appear in chronological order before task")
	}
}

func TestFormatDelegationContextEmptyCallerAgent(t *testing.T) {
	dc := &DelegationContext{
		CallerAgent: "",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}
	result := FormatDelegationContext(dc, "task")
	if !strings.Contains(result, "<from></from>") {
		t.Error("should handle empty caller agent gracefully")
	}
}

func TestFormatDelegationContextPreservesTask(t *testing.T) {
	dc := &DelegationContext{
		CallerAgent: "x",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "ctx"}},
	}
	task := "Schedule meeting with <special> chars & stuff"
	result := FormatDelegationContext(dc, task)
	if !strings.Contains(result, task) {
		t.Error("should preserve task content exactly")
	}
}

// ---------- Parser: Delegation Config ----------

func TestParseDelegationConfigFull(t *testing.T) {
	yaml := `
name: Test
agents:
  leader:
    model: claude-sonnet-4-20250514
    system: You are a leader.
    team: [worker]
    delegation:
      context_window: 6
      blackboard: true
      include_roles:
        - user
        - assistant

  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	leader := doc.Agents["leader"]
	if leader.Delegation == nil {
		t.Fatal("Delegation should not be nil")
	}
	if leader.Delegation.ContextWindow != 6 {
		t.Errorf("ContextWindow = %d, want 6", leader.Delegation.ContextWindow)
	}
	if !leader.Delegation.Blackboard {
		t.Error("Blackboard should be true")
	}
	if len(leader.Delegation.IncludeRoles) != 2 {
		t.Fatalf("IncludeRoles length = %d, want 2", len(leader.Delegation.IncludeRoles))
	}
	if leader.Delegation.IncludeRoles[0] != "user" || leader.Delegation.IncludeRoles[1] != "assistant" {
		t.Errorf("IncludeRoles = %v, want [user assistant]", leader.Delegation.IncludeRoles)
	}
}

func TestParseDelegationConfigAbsent(t *testing.T) {
	yaml := `
name: Test
agents:
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Agents["worker"].Delegation != nil {
		t.Error("Delegation should be nil when absent")
	}
}

func TestParseDelegationContextWindowOnly(t *testing.T) {
	yaml := `
name: Test
agents:
  leader:
    model: claude-sonnet-4-20250514
    system: You are a leader.
    team: [worker]
    delegation:
      context_window: 3
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := doc.Agents["leader"].Delegation
	if d == nil {
		t.Fatal("Delegation should not be nil")
	}
	if d.ContextWindow != 3 {
		t.Errorf("ContextWindow = %d, want 3", d.ContextWindow)
	}
	if d.Blackboard {
		t.Error("Blackboard should default to false")
	}
	if len(d.IncludeRoles) != 0 {
		t.Errorf("IncludeRoles should be empty, got %v", d.IncludeRoles)
	}
}

func TestParseDelegationBlackboardOnly(t *testing.T) {
	yaml := `
name: Test
agents:
  leader:
    model: claude-sonnet-4-20250514
    system: You are a leader.
    team: [worker]
    delegation:
      blackboard: true
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := doc.Agents["leader"].Delegation
	if d == nil {
		t.Fatal("Delegation should not be nil")
	}
	if d.ContextWindow != 0 {
		t.Errorf("ContextWindow should be 0, got %d", d.ContextWindow)
	}
	if !d.Blackboard {
		t.Error("Blackboard should be true")
	}
}

func TestParseDelegationEmptyBlock(t *testing.T) {
	yaml := `
name: Test
agents:
  leader:
    model: claude-sonnet-4-20250514
    system: You are a leader.
    team: [worker]
    delegation: {}
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := doc.Agents["leader"].Delegation
	if d == nil {
		t.Fatal("empty delegation block should still create struct")
	}
	if d.ContextWindow != 0 || d.Blackboard || len(d.IncludeRoles) != 0 {
		t.Error("empty delegation should have all zero values")
	}
}

// ---------- stubLLM for tests ----------

type stubLLM struct {
	response string
}

func (m *stubLLM) Generate(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: m.response}, nil
}

func (m *stubLLM) GenerateStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSchema) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 1)
	go func() {
		ch <- llm.StreamEvent{Delta: m.response}
		close(ch)
	}()
	return ch, nil
}
