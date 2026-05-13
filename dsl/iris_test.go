package dsl

import (
	"strings"
	"testing"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/tools"
)

// TestIrisAgentPopulatesTitle covers govega#59: the orchestrator meta-agent
// must carry a Title on its DSL def so /api/v1/agents surfaces
// "title": "Orchestrator" (or the tenant override) rather than leaving it
// empty. Cody flagged that the FE was reading the orchestrator's title from
// the description field because the title field was unset.
func TestIrisAgentPopulatesTitle(t *testing.T) {
	def := IrisAgent(DefaultIrisConfig())
	if def.Title != "Orchestrator" {
		t.Errorf("default title = %q, want %q", def.Title, "Orchestrator")
	}

	cfg := DefaultIrisConfig()
	cfg.Title = "Chief of Staff"
	def = IrisAgent(cfg)
	if def.Title != "Chief of Staff" {
		t.Errorf("title override = %q, want %q", def.Title, "Chief of Staff")
	}
}

// TestInjectIris_ExposesChannelTools covers govega#57: ARIA (the
// orchestrator) failed onboarding with "coordination tools / create_channel
// not available" because the channel tools weren't registered on the
// interpreter at InjectIris time. Filter takes a snapshot of the registry,
// so Iris's process schema permanently lacked create_channel even after
// the tools were registered later in the boot sequence.
//
// The contract this test pins: callers must register channel tools BEFORE
// injecting Iris, and when they do, Iris's tool schema (the slice handed
// to the LLM) must include create_channel, post_to_channel, and
// list_my_channels.
func TestInjectIris_ExposesChannelTools(t *testing.T) {
	doc := &Document{
		Name:     "test",
		Agents:   make(map[string]*Agent),
		Settings: &Settings{DefaultModel: "test-model"},
	}
	mockLLM := &stubLLM{response: "ok"}
	orch := vega.NewOrchestrator(vega.WithLLM(mockLLM))
	toolSet := tools.NewTools()
	toolSet.RegisterBuiltins()
	interp := &Interpreter{
		doc:               doc,
		orch:              orch,
		agents:            make(map[string]*vega.Process),
		tools:             toolSet,
		delegationConfigs: make(map[string]*DelegationDef),
	}
	defer interp.Shutdown()

	backend := &mockChannelBackend{}
	// Correct boot order: register channel tools FIRST, then inject Iris.
	RegisterChannelTools(interp, backend, nil, nil, nil)
	if err := InjectIris(interp, DefaultIrisConfig(), backend); err != nil {
		t.Fatalf("InjectIris: %v", err)
	}

	proc := interp.Agents()["iris"]
	if proc == nil || proc.Agent == nil || proc.Agent.Tools == nil {
		t.Fatal("iris process has no tools")
	}
	schema := proc.Agent.Tools.Schema()
	want := []string{"create_channel", "post_to_channel", "list_my_channels"}
	for _, w := range want {
		found := false
		for _, s := range schema {
			if s.Name == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("iris tool schema missing %q (got %d tools)", w, len(schema))
		}
	}
}

// TestDefaultVisualIdentity covers govega#60: agent-creation paths need a
// reasonable icon + color so the FE doesn't render blank placeholders before
// the user picks one. The default must be (a) non-empty, (b) deterministic
// per-name so the same agent gets the same identity across restarts and
// callers, and (c) drawn from a curated palette.
func TestDefaultVisualIdentity(t *testing.T) {
	icon, gradient := DefaultVisualIdentity("riley")
	if icon == "" {
		t.Error("icon should be non-empty")
	}
	if len(gradient) != 2 {
		t.Errorf("gradient = %v, want 2 stops", gradient)
	}
	for _, stop := range gradient {
		if !strings.HasPrefix(stop, "#") || len(stop) != 7 {
			t.Errorf("gradient stop %q is not a 6-hex color", stop)
		}
	}

	// Determinism: same name → same identity.
	icon2, gradient2 := DefaultVisualIdentity("riley")
	if icon != icon2 || gradient[0] != gradient2[0] {
		t.Errorf("not deterministic: (%q,%v) vs (%q,%v)", icon, gradient, icon2, gradient2)
	}

	// Different names should generally produce different identities (probabilistic).
	differentSeen := false
	for _, n := range []string{"alex", "morgan", "quinn", "sage", "drew", "casey", "jamie"} {
		i2, g2 := DefaultVisualIdentity(n)
		if i2 != icon || g2[0] != gradient[0] {
			differentSeen = true
			break
		}
	}
	if !differentSeen {
		t.Error("expected at least one different name to yield a different identity")
	}

	// Empty seed should still return something (random fallback is fine).
	icon3, gradient3 := DefaultVisualIdentity("")
	if icon3 == "" || len(gradient3) != 2 {
		t.Errorf("empty seed should still return defaults; got icon=%q gradient=%v", icon3, gradient3)
	}
}
