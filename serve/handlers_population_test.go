package serve

import (
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
