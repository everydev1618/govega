package dsl

import "testing"

func TestParseAgentEffortAndMaxTokens(t *testing.T) {
	yaml := `
name: Test
agents:
  worker:
    model: claude-opus-4-7
    system: You are a worker.
    max_tokens: 64000
    effort: xhigh
`
	p := NewParser()
	doc, err := p.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	worker, ok := doc.Agents["worker"]
	if !ok {
		t.Fatal("worker agent missing")
	}
	if worker.MaxTokens != 64000 {
		t.Errorf("MaxTokens = %d, want 64000", worker.MaxTokens)
	}
	if worker.Effort != "xhigh" {
		t.Errorf("Effort = %q, want xhigh", worker.Effort)
	}
}
