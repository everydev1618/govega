package dsl

import (
	"strings"
	"testing"
)

// TestRenderPromptPlaceholders_BasicSubstitution pins the happy path.
func TestRenderPromptPlaceholders_BasicSubstitution(t *testing.T) {
	got := renderPromptPlaceholders(
		"You are {{orchestrator_display}}, running on {{product_name}}.",
		map[string]string{
			"orchestrator_display": "Atlas",
			"product_name":         "Apex",
		},
	)
	want := "You are Atlas, running on Apex."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderPromptPlaceholders_LeavesUnknownIntact — a typo in one
// placeholder shouldn't blank out the whole prompt before the agent is
// even spawned.
func TestRenderPromptPlaceholders_LeavesUnknownIntact(t *testing.T) {
	got := renderPromptPlaceholders(
		"Hi {{orchestrator_display}}, signed {{oops_typo}}.",
		map[string]string{"orchestrator_display": "Atlas"},
	)
	if !strings.Contains(got, "{{oops_typo}}") {
		t.Errorf("unknown placeholder was rewritten: %q", got)
	}
}

// TestRenderPromptPlaceholders_NoOpWithoutMarker confirms templates
// that don't use the marker pass through unchanged so we never
// accidentally activate placeholder substitution on a legacy prompt.
func TestRenderPromptPlaceholders_NoOpWithoutMarker(t *testing.T) {
	got := renderPromptPlaceholders(
		"You are Atlas. No placeholders here.",
		map[string]string{"anything": "value"},
	)
	if got != "You are Atlas. No placeholders here." {
		t.Errorf("template without marker mutated: %q", got)
	}
}

// TestRenderIrisPrompt_PlaceholderResolvesCollision is the bug the
// substitution refactor was filed for (refs govega#32 item E). With
// cfg.BuilderDisplayName == cfg.ProductName == "Apex", the legacy
// strings.NewReplacer can't tell which slot a literal "Apex" in the
// template was meant to fill. Placeholders disambiguate by name.
func TestRenderIrisPrompt_PlaceholderResolvesCollision(t *testing.T) {
	cfg := IrisConfig{
		Name: "atlas", DisplayName: "Atlas", Title: "Orchestrator",
		BuilderName: "apex", BuilderDisplayName: "Apex",
		ProductName: "Apex",
		SystemPrompt: "I am {{orchestrator_display}}. " +
			"My builder is {{builder_display}}. " +
			"This is the {{product_name}} platform.",
	}
	got := renderIrisPrompt(cfg)
	wantSubs := []string{
		"I am Atlas.",
		"My builder is Apex.",
		"This is the Apex platform.",
	}
	for _, w := range wantSubs {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in rendered prompt:\n%s", w, got)
		}
	}
	// No literal placeholder syntax left behind.
	if strings.Contains(got, "{{") {
		t.Errorf("rendered prompt still contains placeholder markers: %s", got)
	}
}

// TestRenderIrisPrompt_LegacySubstringStillWorks confirms the
// backwards-compat path. Existing apex tenant prompts use literal
// names; that style must keep working.
func TestRenderIrisPrompt_LegacySubstringStillWorks(t *testing.T) {
	cfg := IrisConfig{
		Name: "atlas", DisplayName: "Atlas", Title: "Orchestrator",
		BuilderName: "forge", BuilderDisplayName: "Forge",
		ProductName: "Sky",
		SystemPrompt: "I am Iris. My builder is Hera. This is the Vega platform.",
	}
	got := renderIrisPrompt(cfg)
	want := "I am Atlas. My builder is Forge. This is the Sky platform."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderHeraPrompt_PlaceholderResolvesCollision — same scenario
// from the builder's side.
func TestRenderHeraPrompt_PlaceholderResolvesCollision(t *testing.T) {
	cfg := HeraConfig{
		Name: "apex", DisplayName: "Apex",
		OrchestratorName: "atlas", OrchestratorDisplayName: "Atlas",
		ProductName: "Apex",
		SystemPrompt: "I am {{builder_display}}. " +
			"My orchestrator is {{orchestrator_display}}. " +
			"This is the {{product_name}} platform.",
	}
	got := renderHeraPrompt(cfg)
	if !strings.Contains(got, "I am Apex.") ||
		!strings.Contains(got, "My orchestrator is Atlas.") ||
		!strings.Contains(got, "This is the Apex platform.") {
		t.Errorf("unexpected render:\n%s", got)
	}
}

// TestRenderIrisPrompt_DefaultIdentityFastPath proves the fast path
// for the bundled identity hasn't regressed.
func TestRenderIrisPrompt_DefaultIdentityFastPath(t *testing.T) {
	got := renderIrisPrompt(DefaultIrisConfig())
	if got != irisSystemPrompt {
		t.Error("default Iris config should return the bundled prompt unchanged")
	}
}
