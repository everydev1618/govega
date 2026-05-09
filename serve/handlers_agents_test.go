package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
)

// agentTestServer wires the minimum a *Server needs for agent handler tests:
// in-memory store, a real Interpreter holding a Document with the given
// agents, an empty streams map, and a Config that names the builder so we
// can verify it gets hidden.
func agentTestServer(t *testing.T, agents map[string]*dsl.Agent) *Server {
	t.Helper()
	if agents == nil {
		agents = map[string]*dsl.Agent{}
	}
	doc := &dsl.Document{
		Agents:   agents,
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
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

func TestHandleGetAgent_HappyPath(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {
			Name:        "riley",
			DisplayName: "Riley",
			Title:       "Chief Product Officer",
			Avatar:      "🦉",
			Icon:        "Sparkles",
			Model:       "claude-sonnet-4-6",
			System:      "You are Riley.",
			Tools:       []string{"send_to_agent"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "riley" || got.DisplayName != "Riley" || got.Title != "Chief Product Officer" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.Avatar != "🦉" || got.Icon != "Sparkles" {
		t.Errorf("visual fields wrong: avatar=%q icon=%q", got.Avatar, got.Icon)
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", got.Model)
	}
}

func TestHandleGetAgent_NotFound(t *testing.T) {
	s := agentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/ghost", nil)
	req.SetPathValue("name", "ghost")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

func TestHandleGetAgent_HidesBuilder(t *testing.T) {
	// Builder agent is registered in the doc but must not be exposed.
	s := agentTestServer(t, map[string]*dsl.Agent{
		"hera": {Name: "hera", Model: "claude-sonnet-4-6"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/hera", nil)
	req.SetPathValue("name", "hera")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for builder; body = %s", w.Code, w.Body.String())
	}
}

func TestHandleGetAgent_HidesClones(t *testing.T) {
	// Per-user clones use a colon in the name and are internal-only.
	s := agentTestServer(t, map[string]*dsl.Agent{
		"iris:Etienne": {Name: "iris:Etienne", Model: "claude-sonnet-4-6"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/iris:Etienne", nil)
	req.SetPathValue("name", "iris:Etienne")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for clone; body = %s", w.Code, w.Body.String())
	}
}

// TestHandleGetAgent_ComposedFieldsSurface checks that an agent persisted
// via the compose API surfaces its source tag, team, timestamps, and visual
// identity through the GET endpoint. These are the apex-host-mgmt gaps.
func TestHandleGetAgent_ComposedFieldsSurface(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"alex": {
			Name:           "alex",
			DisplayName:    "Alex",
			Model:          "claude-sonnet-4-6",
			Icon:           "Briefcase",
			AvatarGradient: []string{"#EF4444", "#DC2626"},
		},
	})
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.store.InsertComposedAgent(ComposedAgent{
		Name:           "alex",
		DisplayName:    "Alex",
		Model:          "claude-sonnet-4-6",
		Team:           []string{"riley"},
		Icon:           "Briefcase",
		AvatarGradient: []string{"#EF4444", "#DC2626"},
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("InsertComposedAgent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/alex", nil)
	req.SetPathValue("name", "alex")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Source != "composed" {
		t.Errorf("source = %q, want composed", got.Source)
	}
	if len(got.Team) != 1 || got.Team[0] != "riley" {
		t.Errorf("team = %v, want [riley]", got.Team)
	}
	if got.Icon != "Briefcase" {
		t.Errorf("icon = %q, want Briefcase", got.Icon)
	}
	if len(got.AvatarGradient) != 2 || got.AvatarGradient[0] != "#EF4444" {
		t.Errorf("avatar_gradient = %v", got.AvatarGradient)
	}
	if got.CreatedAt == nil || got.CreatedAt.IsZero() {
		t.Error("created_at not set")
	}
	if got.UpdatedAt == nil || got.UpdatedAt.IsZero() {
		t.Error("updated_at not set")
	}
}

// TestAgentLifecycle covers the mapping from the underlying vega.Process
// state to the high-level (AgentStatus, AgentHealth) the API surfaces.
// Pure function — no Process construction needed.
func TestAgentLifecycle(t *testing.T) {
	cases := []struct {
		name       string
		procStatus vega.Status
		errors     int
		hasProcess bool
		wantStatus AgentStatus
		wantHealth AgentHealth
	}{
		{"no process", "", 0, false, AgentStatusIdle, AgentHealthUnknown},
		{"pending", vega.StatusPending, 0, true, AgentStatusIdle, AgentHealthUnknown},
		{"running, no errors", vega.StatusRunning, 0, true, AgentStatusRunning, AgentHealthHealthy},
		{"running, with errors", vega.StatusRunning, 3, true, AgentStatusRunning, AgentHealthDegraded},
		{"completed clean", vega.StatusCompleted, 0, true, AgentStatusIdle, AgentHealthHealthy},
		{"completed with errors", vega.StatusCompleted, 2, true, AgentStatusIdle, AgentHealthDegraded},
		{"failed", vega.StatusFailed, 0, true, AgentStatusError, AgentHealthUnhealthy},
		{"timeout", vega.StatusTimeout, 0, true, AgentStatusError, AgentHealthUnhealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotHealth := agentLifecycle(tc.procStatus, tc.errors, tc.hasProcess)
			if gotStatus != tc.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, tc.wantStatus)
			}
			if gotHealth != tc.wantHealth {
				t.Errorf("health = %q, want %q", gotHealth, tc.wantHealth)
			}
		})
	}
}

// TestHandleGetAgent_StatusAndHealth checks the new fields are populated on
// every response, even for agents with no live process.
func TestHandleGetAgent_StatusAndHealth(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// dsl.NewInterpreter spawns processes lazily, so the live process puts
	// this agent in StatusRunning with zero errors → status=running,
	// health=healthy. The point is that some non-empty value lands.
	if got.Status == "" {
		t.Error("status field empty")
	}
	if got.Health == "" {
		t.Error("health field empty")
	}
}

// TestHandleListAgents_IncludesNewFields makes sure the list endpoint
// surfaces the same new fields (timestamps, icon, avatar_gradient).
func TestHandleListAgents_IncludesNewFields(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {
			Name:           "riley",
			Model:          "claude-sonnet-4-6",
			Icon:           "Sparkles",
			AvatarGradient: []string{"#A78BFA", "#7C3AED"},
		},
	})
	now := time.Now().UTC().Truncate(time.Second)
	_ = s.store.InsertComposedAgent(ComposedAgent{
		Name:           "riley",
		Model:          "claude-sonnet-4-6",
		Icon:           "Sparkles",
		AvatarGradient: []string{"#A78BFA", "#7C3AED"},
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	w := httptest.NewRecorder()
	s.handleListAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got []AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; got = %+v", len(got), got)
	}
	if got[0].Icon != "Sparkles" {
		t.Errorf("icon missing in list response: %+v", got[0])
	}
	if got[0].CreatedAt == nil {
		t.Errorf("created_at missing in list response")
	}
}
