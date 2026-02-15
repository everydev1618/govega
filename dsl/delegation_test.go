package dsl

import (
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
)

func TestBuildTeamPromptBasic(t *testing.T) {
	result := BuildTeamPrompt("You are a leader.", []string{"worker"}, map[string]string{
		"worker": "Does the work",
	}, false)

	if !strings.Contains(result, "worker") {
		t.Error("BuildTeamPrompt should mention team member")
	}
	if !strings.Contains(result, "Does the work") {
		t.Error("BuildTeamPrompt should include description")
	}
	if strings.Contains(result, "Shared Blackboard") {
		t.Error("BuildTeamPrompt should not mention blackboard when disabled")
	}
}

func TestBuildTeamPromptWithBlackboard(t *testing.T) {
	result := BuildTeamPrompt("You are a leader.", []string{"worker"}, nil, true)

	if !strings.Contains(result, "Shared Blackboard") {
		t.Error("BuildTeamPrompt should mention blackboard when enabled")
	}
	if !strings.Contains(result, "bb_write") {
		t.Error("BuildTeamPrompt should mention bb_write tool")
	}
	if !strings.Contains(result, "bb_read") {
		t.Error("BuildTeamPrompt should mention bb_read tool")
	}
	if !strings.Contains(result, "bb_list") {
		t.Error("BuildTeamPrompt should mention bb_list tool")
	}
}

func TestBuildTeamPromptEmpty(t *testing.T) {
	result := BuildTeamPrompt("Base prompt.", nil, nil, false)
	if result != "Base prompt." {
		t.Errorf("BuildTeamPrompt with no team should return system unchanged, got %q", result)
	}
}

func TestExtractCallerContextNilInputs(t *testing.T) {
	// Nil process
	dc := ExtractCallerContext(nil, &DelegationDef{ContextWindow: 5})
	if dc != nil {
		t.Error("ExtractCallerContext(nil, ...) should return nil")
	}

	// Nil config
	dc = ExtractCallerContext(&vega.Process{}, nil)
	if dc != nil {
		t.Error("ExtractCallerContext(..., nil) should return nil")
	}

	// Zero context window
	dc = ExtractCallerContext(&vega.Process{}, &DelegationDef{ContextWindow: 0})
	if dc != nil {
		t.Error("ExtractCallerContext with ContextWindow=0 should return nil")
	}
}

func TestExtractCallerContextWindow(t *testing.T) {
	// We can't easily set messages on a Process (unexported field),
	// so we test the function's logic with a process that has no messages.
	proc := &vega.Process{}
	dc := ExtractCallerContext(proc, &DelegationDef{ContextWindow: 3})
	if dc != nil {
		t.Error("ExtractCallerContext with empty messages should return nil")
	}
}

func TestFormatDelegationContextNil(t *testing.T) {
	result := FormatDelegationContext(nil, "Do the thing")
	if result != "Do the thing" {
		t.Errorf("FormatDelegationContext(nil, ...) should return original message, got %q", result)
	}
}

func TestFormatDelegationContextEmpty(t *testing.T) {
	dc := &DelegationContext{
		CallerAgent: "dan",
		Messages:    []vega.Message{},
	}
	result := FormatDelegationContext(dc, "Do the thing")
	if result != "Do the thing" {
		t.Errorf("FormatDelegationContext with empty messages should return original, got %q", result)
	}
}

func TestFormatDelegationContextFull(t *testing.T) {
	dc := &DelegationContext{
		CallerAgent: "dan",
		Messages: []vega.Message{
			{Role: vega.RoleUser, Content: "I need help delegating."},
			{Role: vega.RoleAssistant, Content: "Let me help you with that."},
		},
	}
	result := FormatDelegationContext(dc, "Schedule a follow-up")

	if !strings.Contains(result, "<delegation_context>") {
		t.Error("should contain <delegation_context> tag")
	}
	if !strings.Contains(result, "<from>dan</from>") {
		t.Error("should contain <from>dan</from>")
	}
	if !strings.Contains(result, "[user]: I need help delegating.") {
		t.Error("should contain user message")
	}
	if !strings.Contains(result, "[assistant]: Let me help you with that.") {
		t.Error("should contain assistant message")
	}
	if !strings.Contains(result, "<task>\nSchedule a follow-up\n</task>") {
		t.Error("should contain task in <task> tags")
	}
}

func TestParseDelegationConfig(t *testing.T) {
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
		t.Fatalf("Parse() returned error: %v", err)
	}

	leader := doc.Agents["leader"]
	if leader.Delegation == nil {
		t.Fatal("leader.Delegation should not be nil")
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
	if leader.Delegation.IncludeRoles[0] != "user" {
		t.Errorf("IncludeRoles[0] = %q, want %q", leader.Delegation.IncludeRoles[0], "user")
	}
	if leader.Delegation.IncludeRoles[1] != "assistant" {
		t.Errorf("IncludeRoles[1] = %q, want %q", leader.Delegation.IncludeRoles[1], "assistant")
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
		t.Fatalf("Parse() returned error: %v", err)
	}

	worker := doc.Agents["worker"]
	if worker.Delegation != nil {
		t.Error("worker.Delegation should be nil when not specified")
	}
}
