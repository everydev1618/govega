package serve

import (
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// A composed (Hera-created) agent's triggers must survive persistence so a
// reactive agent still reacts after a restart (D1).
func TestComposedAgentTriggersRoundTrip(t *testing.T) {
	store := newTestStore(t)

	in := ComposedAgent{
		Name:   "night-watch",
		Model:  "claude-haiku-4-5",
		System: "You watch.",
		Triggers: []dsl.TriggerDef{
			{On: "agent.completed", Where: "status == failed", Prompt: "Handle {{.Data.agent}}."},
			{On: "schedule.fired", Gate: "model", Prompt: "Digest."},
		},
	}
	if err := store.InsertComposedAgent(in); err != nil {
		t.Fatalf("InsertComposedAgent: %v", err)
	}

	agents, err := store.ListComposedAgents()
	if err != nil {
		t.Fatalf("ListComposedAgents: %v", err)
	}
	var got *ComposedAgent
	for i := range agents {
		if agents[i].Name == "night-watch" {
			got = &agents[i]
			break
		}
	}
	if got == nil {
		t.Fatal("composed agent not found after insert")
	}
	if len(got.Triggers) != 2 {
		t.Fatalf("want 2 triggers after round-trip, got %d", len(got.Triggers))
	}
	if got.Triggers[0].On != "agent.completed" || got.Triggers[0].Where != "status == failed" || got.Triggers[0].Prompt == "" {
		t.Errorf("trigger[0] did not survive: %+v", got.Triggers[0])
	}
	if got.Triggers[1].Gate != "model" {
		t.Errorf("trigger[1] gate did not survive: %+v", got.Triggers[1])
	}
}
