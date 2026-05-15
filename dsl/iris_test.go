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

// TestIrisAgent_ClassifyDefaultsToHaiku pins the orchestrator's per-step
// routing default: first-turn-with-tools dispatches should run on Haiku
// because picking "which agent gets this" is N-way classification, not
// long-form reasoning. Iris stays on Sonnet for synthesis turns where
// the step-type tag is "" (no rule fires) and ModelFor falls back to
// Model.
func TestIrisAgent_ClassifyDefaultsToHaiku(t *testing.T) {
	def := IrisAgent(DefaultIrisConfig())
	if def.Models == nil {
		t.Fatal("IrisAgent should populate Models with sensible defaults, got nil")
	}
	if got := def.Models["classify"]; got != "claude-haiku-4-5-20251001" {
		t.Errorf("Models[classify] = %q, want claude-haiku-4-5-20251001", got)
	}
}

func TestIrisAgent_ExplicitModelsRespected(t *testing.T) {
	cfg := DefaultIrisConfig()
	cfg.Models = map[string]string{"classify": "claude-sonnet-4-6"}
	def := IrisAgent(cfg)
	if got := def.Models["classify"]; got != "claude-sonnet-4-6" {
		t.Errorf("explicit cfg.Models should win, got %q", got)
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
	want := []string{"create_channel", "post_to_channel", "list_my_channels", "read_channel"}
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

// TestIrisSystemPrompt_AntiHallucinationGuardrail pins the rule that prevents
// the orchestrator from confabulating an agent's non-existence. Charlie (the
// everydev-tenant orchestrator) confidently told the user "there is no Scout
// agent — and never was" when scout was in composed_agents the whole time.
// Root cause: irisSystemPrompt told the orchestrator to call list_agents in
// team-creation / delegation flows, but not when answering existence questions.
// This test pins a guardrail that forces list_agents before any "X doesn't
// exist" answer.
func TestIrisSystemPrompt_AntiHallucinationGuardrail(t *testing.T) {
	required := []string{
		"Never claim an agent doesn't exist from memory",
		"list_agents",
	}
	for _, s := range required {
		if !strings.Contains(irisSystemPrompt, s) {
			t.Errorf("irisSystemPrompt missing required guardrail substring: %q", s)
		}
	}
}

// TestIrisSystemPrompt_AntiPhantomDispatchGuardrail covers govega#89: the
// orchestrator fabricated a dispatch ("Dispatched to Scout. She's building
// it now...") without making any tool calls in that turn. This guardrail
// blocks the narrate-instead-of-act failure mode. Sister rule to the
// anti-hallucination guardrail (#76 in spirit) but for claimed actions
// instead of claimed roster state.
func TestIrisSystemPrompt_AntiPhantomDispatchGuardrail(t *testing.T) {
	required := []string{
		"Never narrate a dispatch you didn't make",
		"send_to_agent",
	}
	for _, s := range required {
		if !strings.Contains(irisSystemPrompt, s) {
			t.Errorf("irisSystemPrompt missing required guardrail substring: %q", s)
		}
	}
}

// TestIrisSystemPrompt_FirstRunIdentityRitual covers govega#92: a brand-new
// tenant's first conversation with the orchestrator currently opens with
// "Hey! What do you need?" — efficient, but it leaves the user nameless and
// the agent without a profile. Mira eventually catches a name from organic
// chat, but the first few exchanges feel transactional and the agent doesn't
// know how to address the user. This guardrail forces a one-time bootstrap
// ritual: if profile.md is missing from the memory injection, ask the user
// what to call them BEFORE diving into work, then persist the answer.
func TestIrisSystemPrompt_FirstRunIdentityRitual(t *testing.T) {
	required := []string{
		"First conversation",
		"profile.md",
		"What should I call you",
	}
	for _, s := range required {
		if !strings.Contains(irisSystemPrompt, s) {
			t.Errorf("irisSystemPrompt missing required first-run ritual substring: %q", s)
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
