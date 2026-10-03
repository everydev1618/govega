package dsl

import "testing"

func TestParseTriggerParams(t *testing.T) {
	raw := []any{
		map[string]any{"on": "agent.completed", "where": "status == failed", "prompt": "handle it"},
		map[string]any{"on": "signal.deploy", "gate": "model", "prompt": "react"},
		map[string]any{"on": "", "prompt": "no event"},       // dropped: no On
		map[string]any{"on": "schedule.fired", "prompt": ""}, // dropped: no Prompt
		"not-a-map", // dropped: wrong shape
	}
	got := parseTriggerParams(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 valid triggers, got %d: %+v", len(got), got)
	}
	if got[0].On != "agent.completed" || got[0].Where != "status == failed" {
		t.Errorf("trigger[0] mis-parsed: %+v", got[0])
	}
	if got[1].On != "signal.deploy" || got[1].Gate != "model" {
		t.Errorf("trigger[1] mis-parsed: %+v", got[1])
	}

	if parseTriggerParams(nil) != nil {
		t.Error("nil input should yield nil")
	}
	if parseTriggerParams("garbage") != nil {
		t.Error("non-list input should yield nil")
	}
}
