package serve

import (
	"context"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func TestMiraConfig_AppliesDefaults(t *testing.T) {
	cfg := MiraConfig{}
	cfg.applyDefaults()
	if cfg.Name != "mira" {
		t.Errorf("Name = %q, want mira", cfg.Name)
	}
	if cfg.DisplayName != "Mira" {
		t.Errorf("DisplayName = %q, want Mira", cfg.DisplayName)
	}
	if cfg.Title == "" {
		t.Error("Title should be defaulted")
	}
	if cfg.Model == "" {
		t.Error("Model should be defaulted")
	}
	if cfg.FallbackModel == "" {
		t.Error("FallbackModel should be defaulted")
	}
}

func TestMiraConfig_PreservesExplicitFields(t *testing.T) {
	cfg := MiraConfig{Name: "custom", Model: "claude-opus-4-7"}
	cfg.applyDefaults()
	if cfg.Name != "custom" {
		t.Errorf("Name = %q, want custom", cfg.Name)
	}
	if cfg.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", cfg.Model)
	}
}

func TestMiraAgent_HasOnlyMemoryTools(t *testing.T) {
	agent := MiraAgent(DefaultMiraConfig())
	expected := map[string]bool{
		"memory_read": false, "memory_list": false, "memory_search": false,
		"memory_write": false, "memory_append": false, "memory_edit": false,
		"memory_delete": false, "memory_rename": false,
	}
	for _, tool := range agent.Tools {
		if _, ok := expected[tool]; !ok {
			t.Errorf("unexpected tool on Mira: %q (only memory_* allowed)", tool)
		}
		expected[tool] = true
	}
	for tool, found := range expected {
		if !found {
			t.Errorf("Mira missing required tool: %q", tool)
		}
	}
}

func TestMiraAgent_IsMetaAgent(t *testing.T) {
	agent := MiraAgent(DefaultMiraConfig())
	if !agent.IsMeta {
		t.Error("Mira must be marked IsMeta so it doesn't appear in user-facing agent lists")
	}
	if agent.System == "" {
		t.Error("Mira must have a system prompt")
	}
}

func TestInjectMira_RegistersAgentOnInterpreter(t *testing.T) {
	doc := &dsl.Document{Agents: map[string]*dsl.Agent{}, Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"}}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	if err := InjectMira(interp, DefaultMiraConfig()); err != nil {
		t.Fatalf("InjectMira: %v", err)
	}
	got := interp.Document().Agents["mira"]
	if got == nil {
		t.Fatal("mira not registered on interpreter")
	}
	if !got.IsMeta {
		t.Error("registered mira must keep IsMeta=true")
	}
}

func TestBuildCuratorPrompt_IncludesExchange(t *testing.T) {
	prompt := buildCuratorPrompt("et", "tony", "what's our runway?", "About 18 months at current burn.")
	for _, want := range []string{"et", "tony", "runway", "18 months"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q: %s", want, prompt)
		}
	}
	if !strings.Contains(strings.ToLower(prompt), "shared user wiki") && !strings.Contains(strings.ToLower(prompt), "wiki") {
		t.Errorf("prompt should reference the wiki: %s", prompt)
	}
}

func TestBuildCuratorPrompt_TrimsWhitespace(t *testing.T) {
	prompt := buildCuratorPrompt("et", "tony", "   hello\n\n", "\n\nhi there\n")
	if strings.Contains(prompt, "hello\n\n\n") {
		t.Errorf("user message whitespace not trimmed: %q", prompt)
	}
	if !strings.Contains(prompt, "hello") || !strings.Contains(prompt, "hi there") {
		t.Errorf("content lost: %q", prompt)
	}
}

func TestCurateMemory_NoOpOnEmpty(t *testing.T) {
	// Empty userMsg / response should skip the LLM call entirely. We can't
	// verify the no-call directly without DI, but we can confirm no panic
	// and no error escapes (the function returns nothing).
	s := &Server{}
	s.curateMemory(context.Background(), "et", "tony", "", "response")
	s.curateMemory(context.Background(), "et", "tony", "user msg", "")
	s.curateMemory(context.Background(), "", "tony", "user msg", "response")
	// If we got here without panicking, we're good.
}
