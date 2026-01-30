package dsl

import (
	"strings"
	"testing"
)

func TestNewParser(t *testing.T) {
	p := NewParser()
	if p == nil {
		t.Fatal("NewParser() returned nil")
	}
}

func TestParseSimpleDocument(t *testing.T) {
	yaml := `
name: Test Team
description: A test team configuration

agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You are a coder.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	if doc.Name != "Test Team" {
		t.Errorf("Document.Name = %q, want %q", doc.Name, "Test Team")
	}

	if doc.Description != "A test team configuration" {
		t.Errorf("Document.Description = %q, want %q", doc.Description, "A test team configuration")
	}

	if len(doc.Agents) != 1 {
		t.Errorf("len(Document.Agents) = %d, want 1", len(doc.Agents))
	}

	coder, ok := doc.Agents["coder"]
	if !ok {
		t.Fatal("Agent 'coder' not found")
	}

	if coder.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Agent.Model = %q, want %q", coder.Model, "claude-sonnet-4-20250514")
	}
}

func TestParseAgentWithExtends(t *testing.T) {
	yaml := `
name: Test
agents:
  base:
    model: claude-sonnet-4-20250514
    system: Base agent.

  specialist:
    extends: base
    model: claude-sonnet-4-20250514
    system: Specialized agent.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	specialist, ok := doc.Agents["specialist"]
	if !ok {
		t.Fatal("Agent 'specialist' not found")
	}

	if specialist.Extends != "base" {
		t.Errorf("Agent.Extends = %q, want %q", specialist.Extends, "base")
	}
}

func TestParseAgentWithTemperature(t *testing.T) {
	yaml := `
name: Test
agents:
  creative:
    model: claude-sonnet-4-20250514
    system: You are creative.
    temperature: 0.9
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	agent := doc.Agents["creative"]
	if agent.Temperature == nil {
		t.Fatal("Agent.Temperature should not be nil")
	}

	if *agent.Temperature != 0.9 {
		t.Errorf("Agent.Temperature = %f, want 0.9", *agent.Temperature)
	}
}

func TestParseAgentWithTools(t *testing.T) {
	yaml := `
name: Test
agents:
  developer:
    model: claude-sonnet-4-20250514
    system: You are a developer.
    tools:
      - read_file
      - write_file
      - run_command
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	agent := doc.Agents["developer"]
	if len(agent.Tools) != 3 {
		t.Errorf("len(Agent.Tools) = %d, want 3", len(agent.Tools))
	}

	if agent.Tools[0] != "read_file" {
		t.Errorf("Agent.Tools[0] = %q, want %q", agent.Tools[0], "read_file")
	}
}

func TestParseAgentWithSupervision(t *testing.T) {
	yaml := `
name: Test
agents:
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
    supervision:
      strategy: restart
      max_restarts: 5
      window: 10m
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	agent := doc.Agents["worker"]
	if agent.Supervision == nil {
		t.Fatal("Agent.Supervision should not be nil")
	}

	if agent.Supervision.Strategy != "restart" {
		t.Errorf("Supervision.Strategy = %q, want %q", agent.Supervision.Strategy, "restart")
	}

	if agent.Supervision.MaxRestarts != 5 {
		t.Errorf("Supervision.MaxRestarts = %d, want 5", agent.Supervision.MaxRestarts)
	}
}

func TestParseWorkflow(t *testing.T) {
	yaml := `
name: Test
agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You are a coder.

workflows:
  simple:
    description: A simple workflow
    inputs:
      task:
        type: string
        required: true
    steps:
      - coder:
          send: "{{task}}"
          save: result
    output: "{{result}}"
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	if len(doc.Workflows) != 1 {
		t.Errorf("len(Document.Workflows) = %d, want 1", len(doc.Workflows))
	}

	wf, ok := doc.Workflows["simple"]
	if !ok {
		t.Fatal("Workflow 'simple' not found")
	}

	if wf.Description != "A simple workflow" {
		t.Errorf("Workflow.Description = %q, want %q", wf.Description, "A simple workflow")
	}

	if len(wf.Inputs) != 1 {
		t.Errorf("len(Workflow.Inputs) = %d, want 1", len(wf.Inputs))
	}

	taskInput, ok := wf.Inputs["task"]
	if !ok {
		t.Fatal("Input 'task' not found")
	}

	if !taskInput.Required {
		t.Error("Input 'task' should be required")
	}
}

func TestParseWorkflowWithDefault(t *testing.T) {
	yaml := `
name: Test
agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You are a coder.

workflows:
  with-defaults:
    inputs:
      language:
        type: string
        default: python
    steps:
      - coder:
          send: "Write in {{language}}"
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	input := doc.Workflows["with-defaults"].Inputs["language"]
	if input.Default != "python" {
		t.Errorf("Input.Default = %v, want %q", input.Default, "python")
	}
}

func TestParseSettings(t *testing.T) {
	yaml := `
name: Test
agents:
  test:
    model: claude-sonnet-4-20250514
    system: Test agent.

settings:
  default_model: claude-sonnet-4-20250514
  sandbox: /tmp/sandbox
  budget: "$10.00"
  rate_limit:
    requests_per_minute: 60
    tokens_per_minute: 100000
  logging:
    level: debug
    file: /var/log/vega.log
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	if doc.Settings == nil {
		t.Fatal("Document.Settings should not be nil")
	}

	if doc.Settings.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("Settings.DefaultModel = %q, want %q", doc.Settings.DefaultModel, "claude-sonnet-4-20250514")
	}

	if doc.Settings.Sandbox != "/tmp/sandbox" {
		t.Errorf("Settings.Sandbox = %q, want %q", doc.Settings.Sandbox, "/tmp/sandbox")
	}

	if doc.Settings.Budget != "$10.00" {
		t.Errorf("Settings.Budget = %q, want %q", doc.Settings.Budget, "$10.00")
	}

	if doc.Settings.RateLimit == nil {
		t.Fatal("Settings.RateLimit should not be nil")
	}

	if doc.Settings.RateLimit.RequestsPerMinute != 60 {
		t.Errorf("RateLimit.RequestsPerMinute = %d, want 60", doc.Settings.RateLimit.RequestsPerMinute)
	}
}

func TestParseInvalidYAML(t *testing.T) {
	yaml := `
name: Test
agents:
  - invalid: this is not a map
`
	p := NewParser()
	_, err := p.Parse([]byte(yaml))
	if err == nil {
		t.Error("Parse() should return error for invalid YAML structure")
	}
}

func TestContainsExpression(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Hello, world!", false},
		{"{{name}}", true},
		{"Hello, {{name}}!", true},
		{"{{a}} and {{b}}", true},
		{"{not an expression}", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := ContainsExpression(tt.input); got != tt.want {
			t.Errorf("ContainsExpression(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtractExpressions(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"Hello, world!", nil},
		{"{{name}}", []string{"name"}},
		{"Hello, {{name}}!", []string{"name"}},
		{"{{a}} and {{b}}", []string{"a", "b"}},
		{"{{user.name}}", []string{"user.name"}},
	}

	for _, tt := range tests {
		got := ExtractExpressions(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("ExtractExpressions(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			// Trim spaces since the implementation trims them
			gotTrimmed := strings.TrimSpace(got[i])
			wantTrimmed := strings.TrimSpace(tt.want[i])
			if gotTrimmed != wantTrimmed {
				t.Errorf("ExtractExpressions(%q)[%d] = %q, want %q", tt.input, i, gotTrimmed, wantTrimmed)
			}
		}
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "workflow references unknown agent",
			yaml: `
name: Test
agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You are a coder.

workflows:
  bad:
    steps:
      - unknown_agent:
          send: hello
`,
			wantErr: "unknown_agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser()
			_, err := p.Parse([]byte(tt.yaml))
			if err == nil {
				t.Error("Parse() should return validation error")
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParseStepTypes(t *testing.T) {
	yaml := `
name: Test
agents:
  coder:
    model: claude-sonnet-4-20250514
    system: You are a coder.

workflows:
  complex:
    steps:
      - set:
          counter: 0
      - coder:
          send: "Hello"
          save: greeting
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	wf := doc.Workflows["complex"]
	if len(wf.Steps) != 2 {
		t.Errorf("len(Workflow.Steps) = %d, want 2", len(wf.Steps))
	}

	// First step should be a set step
	if wf.Steps[0].Set == nil {
		t.Error("First step should have Set")
	}

	// Second step should be an agent step
	if wf.Steps[1].Agent != "coder" {
		t.Errorf("Second step Agent = %q, want %q", wf.Steps[1].Agent, "coder")
	}
}

func TestParseMultipleAgents(t *testing.T) {
	yaml := `
name: Development Team
agents:
  frontend:
    model: claude-sonnet-4-20250514
    system: You are a frontend developer.

  backend:
    model: claude-sonnet-4-20250514
    system: You are a backend developer.

  reviewer:
    model: claude-sonnet-4-20250514
    system: You are a code reviewer.
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	if len(doc.Agents) != 3 {
		t.Errorf("len(Document.Agents) = %d, want 3", len(doc.Agents))
	}

	for _, name := range []string{"frontend", "backend", "reviewer"} {
		if _, ok := doc.Agents[name]; !ok {
			t.Errorf("Agent %q not found", name)
		}
	}
}

func TestParseEmptyDocument(t *testing.T) {
	yaml := `
name: Empty
`
	p := NewParser()
	_, err := p.Parse([]byte(yaml))
	if err == nil {
		t.Error("Parse() should return error for document without agents")
	}
}

func TestParseAgentWithBudget(t *testing.T) {
	yaml := `
name: Test
agents:
  worker:
    model: claude-sonnet-4-20250514
    system: You are a worker.
    budget: "$5.00"
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	agent := doc.Agents["worker"]
	if agent.Budget != "$5.00" {
		t.Errorf("Agent.Budget = %q, want %q", agent.Budget, "$5.00")
	}
}
