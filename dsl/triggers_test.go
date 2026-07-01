package dsl

import "testing"

func TestParseAgentTriggers(t *testing.T) {
	yaml := `
name: Reactive Team
agents:
  night-watch:
    model: claude-haiku-4-5
    system: You watch for failures.
    triggers:
      - on: agent.completed
        where: "status == failed"
        prompt: "Agent {{.Data.agent}} failed: {{.Data.error}}."
      - on: schedule.fired
        gate: model
        prompt: "Produce the morning digest."
`
	doc, err := NewParser().Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}
	nw, ok := doc.Agents["night-watch"]
	if !ok {
		t.Fatal("agent night-watch not found")
	}
	if len(nw.Triggers) != 2 {
		t.Fatalf("want 2 triggers, got %d", len(nw.Triggers))
	}
	if nw.Triggers[0].On != "agent.completed" || nw.Triggers[0].Where != "status == failed" {
		t.Errorf("trigger[0] mis-parsed: %+v", nw.Triggers[0])
	}
	if nw.Triggers[0].Prompt == "" {
		t.Error("trigger[0] prompt should be populated")
	}
	if nw.Triggers[1].On != "schedule.fired" || nw.Triggers[1].Gate != "model" {
		t.Errorf("trigger[1] mis-parsed: %+v", nw.Triggers[1])
	}
}

// ReactiveTriggers must surface an agent's triggers from its definition even
// when the agent has never been spawned (lazy-spawn) — otherwise a purely
// reactive agent could never wake. Regression guard for the discovery gap.
func TestReactiveTriggersAreSpawnIndependent(t *testing.T) {
	yaml := `
name: Reactive Team
agents:
  night-watch:
    model: claude-haiku-4-5
    system: You watch for failures.
    triggers:
      - on: agent.completed
        where: "status == failed"
        prompt: "Handle {{.Data.agent}}."
`
	doc, err := NewParser().Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// WithLazySpawn: nothing is spawned, so i.agents is empty.
	interp, err := NewInterpreter(doc, WithLazySpawn())
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}

	got := interp.ReactiveTriggers()
	trigs, ok := got["night-watch"]
	if !ok || len(trigs) != 1 {
		t.Fatalf("expected night-watch triggers to be discoverable pre-spawn, got %#v", got)
	}
	if trigs[0].On != "agent.completed" || trigs[0].Where != "status == failed" {
		t.Errorf("trigger not carried through to reactive.Trigger: %+v", trigs[0])
	}
}
