package dsl

import (
	"context"
	"testing"

	"github.com/everydev1618/govega/tools"
)

// TestAgentInheritsChannelTools pins that every spawned agent receives the
// channel tools (post_to_channel/read_channel/list_my_channels) — even when
// its persisted Tools allow-list doesn't mention them.
//
// Context: channels are how teammates coordinate. An agent spun up with a
// narrow allow-list that omits post_to_channel can't post updates, so it
// improvises (saving files, asking the human to relay) — see #musolist-launch
// where Quill reported "post_to_channel isn't in my active tool set". Channel
// participation should be first-class for every agent, the same way wiki
// memory is auto-included.
func TestAgentInheritsChannelTools(t *testing.T) {
	interp := newHeraTestInterpreter(t)
	defer interp.Shutdown()

	// Register stub tools with the canonical channel names. In the real boot,
	// dsl/channel_tools.go's RegisterChannelTools does this with store-backed
	// implementations; here we only care that the names propagate through the
	// spawn filter.
	for _, name := range ChannelToolNames() {
		interp.Tools().Register(name, tools.ToolDef{
			Description: "stub " + name,
			Fn: tools.ToolFunc(func(_ context.Context, _ map[string]any) (string, error) {
				return "", nil
			}),
		})
	}

	// Custom agent with a narrow, non-meta tool allow-list that omits every
	// channel tool. Mirrors a YAML-defined persona (e.g. Quill).
	def := &Agent{
		Name:   "quill",
		Model:  "test-model",
		System: "You are Quill, a custom agent.",
		Tools:  []string{"read_file", "write_file"},
	}
	if err := interp.AddAgent("quill", def); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	proc, ok := interp.Agents()["quill"]
	if !ok || proc.Agent == nil || proc.Agent.Tools == nil {
		t.Fatal("quill process should be spawned with a Tools collection")
	}

	have := map[string]bool{}
	for _, s := range proc.Agent.Tools.Schema() {
		have[s.Name] = true
	}
	for _, name := range ChannelToolNames() {
		if !have[name] {
			t.Errorf("agent surface missing %q — channel tools must be auto-included for every agent (got %d tools)", name, len(have))
		}
	}
}
