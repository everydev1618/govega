package serve

import (
	"context"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func TestMemoraConfig_AppliesDefaults(t *testing.T) {
	cfg := MemoraConfig{}
	cfg.applyDefaults()
	if cfg.Name != "memora" {
		t.Errorf("Name = %q, want memora", cfg.Name)
	}
	if cfg.DisplayName != "Memora" {
		t.Errorf("DisplayName = %q, want Memora", cfg.DisplayName)
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

func TestMemoraConfig_PreservesExplicitFields(t *testing.T) {
	cfg := MemoraConfig{Name: "custom", Model: "claude-opus-4-7"}
	cfg.applyDefaults()
	if cfg.Name != "custom" {
		t.Errorf("Name = %q, want custom", cfg.Name)
	}
	if cfg.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", cfg.Model)
	}
}

func TestMemoraAgent_HasOnlyMemoryTools(t *testing.T) {
	agent := MemoraAgent(DefaultMemoraConfig())
	expected := map[string]bool{
		"memory_read": false, "memory_list": false, "memory_search": false,
		"memory_write": false, "memory_append": false, "memory_edit": false,
		"memory_delete": false, "memory_rename": false,
	}
	for _, tool := range agent.Tools {
		if _, ok := expected[tool]; !ok {
			t.Errorf("unexpected tool on Memora: %q (only memory_* allowed)", tool)
		}
		expected[tool] = true
	}
	for tool, found := range expected {
		if !found {
			t.Errorf("Memora missing required tool: %q", tool)
		}
	}
}

func TestMemoraAgent_IsMetaAgent(t *testing.T) {
	agent := MemoraAgent(DefaultMemoraConfig())
	if !agent.IsMeta {
		t.Error("Memora must be marked IsMeta so it doesn't appear in user-facing agent lists")
	}
	if agent.System == "" {
		t.Error("Memora must have a system prompt")
	}
}

func TestInjectMemora_RegistersAgentOnInterpreter(t *testing.T) {
	doc := &dsl.Document{Agents: map[string]*dsl.Agent{}, Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"}}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	if err := InjectMemora(interp, DefaultMemoraConfig()); err != nil {
		t.Fatalf("InjectMemora: %v", err)
	}
	got := interp.Document().Agents["memora"]
	if got == nil {
		t.Fatal("memora not registered on interpreter")
	}
	if !got.IsMeta {
		t.Error("registered memora must keep IsMeta=true")
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
