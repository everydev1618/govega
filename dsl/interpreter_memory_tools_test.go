package dsl

import (
	"context"
	"testing"

	"github.com/everydev1618/govega/tools"
)

// TestAgentInheritsWikiMemoryTools pins that every spawned agent receives
// the wiki memory tools (memory_read/list/search/write/append/edit) — even
// when its persisted Tools allow-list doesn't mention them.
//
// Context: an agent that doesn't carry memory_* in its allow-list can't
// `memory_read` linked pages from MEMORY.md or `memory_write` durable facts
// it learns in conversation. Result: shared memory is read-only-by-prompt
// for that agent; the wiki is a one-way street. We want memory to be
// first-class for every agent, the same way the sandbox surface is
// auto-included when FLY_SANDBOX_TOKEN is set.
func TestAgentInheritsWikiMemoryTools(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	// Register stub tools with the canonical wiki memory names. In the real
	// boot, serve/memory_wiki_tools.go does this with the actual store-backed
	// implementations; for this test we only care that the names propagate
	// through the spawn filter.
	for _, name := range []string{
		"memory_read", "memory_list", "memory_search",
		"memory_write", "memory_append", "memory_edit",
	} {
		interp.Tools().Register(name, tools.ToolDef{
			Description: "stub " + name,
			Fn: tools.ToolFunc(func(_ context.Context, _ map[string]any) (string, error) {
				return "", nil
			}),
		})
	}

	// Custom agent with a narrow, non-meta tool allow-list. Mirrors what a
	// YAML-defined sub-agent (e.g. a vega-cpo persona) looks like today: a
	// few file-reading tools, no memory tools.
	def := &Agent{
		Name:   "rosa",
		Model:  "test-model",
		System: "You are Rosa, a custom agent.",
		Tools:  []string{"read_file", "write_file", "exec"},
	}
	if err := interp.AddAgent("rosa", def); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	proc, ok := interp.Agents()["rosa"]
	if !ok || proc.Agent == nil || proc.Agent.Tools == nil {
		t.Fatal("rosa process should be spawned with a Tools collection")
	}

	have := map[string]bool{}
	for _, s := range proc.Agent.Tools.Schema() {
		have[s.Name] = true
	}
	want := []string{
		"memory_read", "memory_list", "memory_search",
		"memory_write", "memory_append", "memory_edit",
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("agent surface missing %q — wiki memory must be auto-included for every agent (got %d tools)", name, len(have))
		}
	}
}
