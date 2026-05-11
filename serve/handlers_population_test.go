package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// populationTestServer wires the minimum a *Server needs for handleCreateAgent
// tests: in-memory store, a real Interpreter (so AddAgent + spawnAgent run for
// real), and the Iris tool surface registered so meta-tool filtering can be
// asserted as it would behave in production.
func populationTestServer(t *testing.T) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents:   map[string]*dsl.Agent{},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	// Mirror production: Iris tools are registered on the interpreter even
	// for non-Iris agents to pick from. The bug is that they leak to
	// composed agents who don't ask for them; the fix is to filter them.
	dsl.RegisterIrisTools(interp, dsl.DefaultIrisConfig())
	return &Server{
		store:   newTestStore(t),
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
		cfg: Config{
			Builder:      dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{Name: "iris"},
		},
	}
}

// TestHandleCreateAgent_DefaultsIdentityWhenSystemEmpty pins the fix for
// issue #48: an agent created without a system prompt must get a minimal
// "you are <name>" identity rather than relying on the LLM to invent one.
// Today the LLM confabulates the orchestrator (Iris) persona because the
// agent's tool surface looks like Iris's and there's no system prompt to
// tell it otherwise.
func TestHandleCreateAgent_DefaultsIdentityWhenSystemEmpty(t *testing.T) {
	s := populationTestServer(t)
	body := `{"name":"test-agent-02","model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	def := s.interp.Document().Agents["test-agent-02"]
	if def == nil {
		t.Fatal("agent def not registered on interpreter")
	}
	if def.System == "" {
		t.Errorf("agent System is empty; expected a fallback identity")
	}
	if !strings.Contains(def.System, "test-agent-02") {
		t.Errorf("agent System %q does not name the agent", def.System)
	}

	// The persisted record must also carry the fallback identity so a
	// server restart doesn't drop the agent back to no-system.
	composed, err := s.store.ListComposedAgents()
	if err != nil {
		t.Fatalf("ListComposedAgents: %v", err)
	}
	if len(composed) != 1 {
		t.Fatalf("composed agents = %d, want 1", len(composed))
	}
	if composed[0].System == "" {
		t.Errorf("persisted ComposedAgent.System is empty; expected fallback identity")
	}
}

// TestHandleCreateAgent_DefaultsIdentityHonorsDisplayName checks that the
// fallback identity uses the display name when set, since that's the name
// the user sees and presumably the one the agent should claim.
func TestHandleCreateAgent_DefaultsIdentityHonorsDisplayName(t *testing.T) {
	s := populationTestServer(t)
	body := `{"name":"test-agent-02","display_name":"Test Agent 02","model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	def := s.interp.Document().Agents["test-agent-02"]
	if def == nil {
		t.Fatal("agent def not registered")
	}
	if !strings.Contains(def.System, "Test Agent 02") {
		t.Errorf("agent System %q does not include display name", def.System)
	}
}

// TestHandleCreateAgent_PreservesExplicitSystem checks that an explicit
// system prompt is left alone — the fallback only activates when system
// is empty.
func TestHandleCreateAgent_PreservesExplicitSystem(t *testing.T) {
	s := populationTestServer(t)
	body := `{"name":"riley","model":"claude-sonnet-4-6","system":"You are Riley, a CPO."}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	def := s.interp.Document().Agents["riley"]
	if def == nil {
		t.Fatal("agent def not registered")
	}
	if def.System != "You are Riley, a CPO." {
		t.Errorf("agent System = %q, want it preserved verbatim", def.System)
	}
}

// TestHandleCreateAgent_ExcludesMetaToolsWhenToolsEmpty pins the second
// half of the issue #48 fix: a composed agent created without an explicit
// tool list must NOT inherit Iris's orchestrator tools (or Hera's builder
// tools). Today spawnAgent treats `len(def.Tools) == 0` as "give it
// everything", which leaks meta-tools and primes the agent to roleplay
// as Iris.
func TestHandleCreateAgent_ExcludesMetaToolsWhenToolsEmpty(t *testing.T) {
	s := populationTestServer(t)
	body := `{"name":"test-agent-02","model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	def := s.interp.Document().Agents["test-agent-02"]
	if def == nil {
		t.Fatal("agent def not registered")
	}

	// After the fix, def.Tools is populated with the non-meta subset of
	// the registry rather than left empty. An empty slice would cause
	// spawnAgent to fall through to "give everything" — exactly the leak
	// we're guarding against.
	if len(def.Tools) == 0 {
		t.Fatal("def.Tools is empty; would leak full registry to spawnAgent")
	}
	for _, name := range def.Tools {
		if dsl.IsIrisTool(name) {
			t.Errorf("agent inherited Iris meta tool %q", name)
		}
		if dsl.IsHeraTool(name) {
			t.Errorf("agent inherited Hera meta tool %q", name)
		}
	}
}

// TestHandleCreateAgent_TeamAgentStillGetsDelegate is a regression guard:
// when an agent is created with a team, it should still get the `delegate`
// tool (via the team-prompt branch) plus the non-meta default tools, even
// though we now filter meta tools in the empty-tools branch.
func TestHandleCreateAgent_TeamAgentStillGetsDelegate(t *testing.T) {
	s := populationTestServer(t)
	// Pre-register a teammate so AddAgent doesn't fail on validation.
	s.interp.Document().Agents["riley"] = &dsl.Agent{Name: "riley", Model: "claude-sonnet-4-6", System: "You are Riley."}
	body := `{"name":"alex","model":"claude-sonnet-4-6","team":["riley"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	def := s.interp.Document().Agents["alex"]
	if def == nil {
		t.Fatal("agent def not registered")
	}
	hasDelegate := false
	for _, name := range def.Tools {
		if name == "delegate" {
			hasDelegate = true
		}
	}
	if !hasDelegate {
		t.Errorf("team agent missing delegate tool; tools = %v", def.Tools)
	}
}

// TestInjectHera_WiresProvisioningCallback covers the second half of
// govega#56: when ARIA delegates "spin up a team" to Hera, the FE needs
// the same SSE provisioning events as when an agent is created via HTTP.
// Server.injectHera registers an OnProvisioning callback that forwards
// to the broker; this test exercises that wiring end-to-end by invoking
// Hera's create_agent tool and confirming agent.provisioning events fire.
func TestInjectHera_WiresProvisioningCallback(t *testing.T) {
	s := populationTestServer(t)
	s.injectHera()
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	_, err := s.interp.Tools().Execute(req(t), "create_agent", map[string]any{
		"name":         "marcus",
		"display_name": "Marcus",
		"title":        "Engineer",
		"system":       "You are Marcus.",
		"model":        "claude-sonnet-4-6",
		"avatar":       "m1",
	})
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}

	got := drainBroker(sub)
	if !hasProvisioningPhase(got, "marcus", "started") {
		t.Errorf("missing started event; got = %+v", got)
	}
	if !hasProvisioningPhase(got, "marcus", "ready") {
		t.Errorf("missing ready event; got = %+v", got)
	}
}

// req returns a placeholder context for tool calls in tests. Hera's
// create_agent doesn't read process info, but the signature wants a context.
func req(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

// drainBroker pulls all events from a broker subscription without blocking.
func drainBroker(ch <-chan BrokerEvent) []BrokerEvent {
	var out []BrokerEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

// hasProvisioningPhase reports whether the slice contains an
// agent.provisioning event for the given agent in the given phase.
func hasProvisioningPhase(events []BrokerEvent, agent, phase string) bool {
	for _, ev := range events {
		if ev.Type != "agent.provisioning" || ev.Agent != agent {
			continue
		}
		if data, ok := ev.Data.(map[string]any); ok {
			if p, _ := data["phase"].(string); p == phase {
				return true
			}
		}
	}
	return false
}

// TestHandleCreateAgent_EmitsProvisioningEvents covers govega#56: the
// HTTP create-agent path must publish provisioning events over the SSE
// broker so the apex-host-mgmt sidebar can render a placeholder row that
// fills in as data arrives. Today the FE sees nothing until the agent
// pops into the list. We require at least a "started" and a "ready"
// event with type prefix "agent.provisioning".
func TestHandleCreateAgent_EmitsProvisioningEvents(t *testing.T) {
	s := populationTestServer(t)
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	body := `{"name":"sofia","display_name":"Sofia","model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Drain the broker channel (non-blocking — events have already been
	// published synchronously inside the handler).
	var events []BrokerEvent
	for {
		select {
		case ev, ok := <-sub:
			if !ok {
				goto done
			}
			events = append(events, ev)
		default:
			goto done
		}
	}
done:

	hasPhase := func(phase string) bool {
		for _, ev := range events {
			if ev.Type == "agent.provisioning" && ev.Agent == "sofia" {
				if data, ok := ev.Data.(map[string]any); ok {
					if p, _ := data["phase"].(string); p == phase {
						return true
					}
				}
			}
		}
		return false
	}
	if !hasPhase("started") {
		t.Errorf("missing agent.provisioning started event; events = %+v", events)
	}
	if !hasPhase("ready") {
		t.Errorf("missing agent.provisioning ready event; events = %+v", events)
	}
}

// TestHandleCreateAgent_DefaultsIconAndGradient pins govega#60: an agent
// created without icon / avatar_gradient must come back with both populated
// — otherwise the FE renders blank placeholders until a user picks one in
// settings. Defaults are deterministic per-name (see dsl.DefaultVisualIdentity).
func TestHandleCreateAgent_DefaultsIconAndGradient(t *testing.T) {
	s := populationTestServer(t)
	body := `{"name":"sofia","model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	def := s.interp.Document().Agents["sofia"]
	if def == nil {
		t.Fatal("agent def not registered")
	}
	if def.Icon == "" {
		t.Error("def.Icon empty; expected default icon")
	}
	if len(def.AvatarGradient) != 2 {
		t.Errorf("def.AvatarGradient = %v; want 2 stops", def.AvatarGradient)
	}

	// The defaults must also be persisted to composed_agents so a restart
	// doesn't regress to blank placeholders.
	composed, err := s.store.ListComposedAgents()
	if err != nil {
		t.Fatalf("ListComposedAgents: %v", err)
	}
	if len(composed) != 1 {
		t.Fatalf("composed agents = %d, want 1", len(composed))
	}
	if composed[0].Icon != def.Icon {
		t.Errorf("persisted Icon %q != def Icon %q", composed[0].Icon, def.Icon)
	}
	if len(composed[0].AvatarGradient) != 2 {
		t.Errorf("persisted AvatarGradient = %v; want 2 stops", composed[0].AvatarGradient)
	}
}

// TestHandleCreateAgent_PreservesExplicitVisualIdentity is a regression
// guard: caller-supplied icon/color must not be overwritten by defaults.
func TestHandleCreateAgent_PreservesExplicitVisualIdentity(t *testing.T) {
	s := populationTestServer(t)
	body := `{"name":"marcus","model":"claude-sonnet-4-6","icon":"Cpu","avatar_gradient":["#0EA5E9","#22D3EE"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	def := s.interp.Document().Agents["marcus"]
	if def.Icon != "Cpu" {
		t.Errorf("def.Icon = %q, want Cpu", def.Icon)
	}
	if len(def.AvatarGradient) != 2 || def.AvatarGradient[0] != "#0EA5E9" {
		t.Errorf("def.AvatarGradient = %v, want explicit values preserved", def.AvatarGradient)
	}
}

// TestHandleCreateAgent_RejectsMissingName covers the existing 400 path so
// the new fallback branches don't accidentally let a nameless request
// through.
func TestHandleCreateAgent_RejectsMissingName(t *testing.T) {
	s := populationTestServer(t)
	body := `{"model":"claude-sonnet-4-6"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Error, "name") {
		t.Errorf("error %q should mention name", resp.Error)
	}
}
