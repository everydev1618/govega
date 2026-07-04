package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// TestTaskStatsByAssignee covers the store-level aggregation. Pure store
// test — no handler, no server.
func TestTaskStatsByAssignee(t *testing.T) {
	store := newTestStore(t)
	// riley: 2 active, 3 completed, 1 canceled → success 3/4 = 0.75
	for i, status := range []string{"todo", "doing", "done", "done", "done", "canceled"} {
		if err := store.InsertTask(Task{
			ID:       "riley-" + string(rune('a'+i)),
			Title:    "T",
			Status:   status,
			Assignee: "riley",
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// alex: 1 active, 0 completed → success rate is nil
	_ = store.InsertTask(Task{ID: "alex-a", Title: "T", Status: "doing", Assignee: "alex"})
	// unassigned task (empty assignee) — must not appear in any agent's stats
	_ = store.InsertTask(Task{ID: "u1", Title: "T", Status: "todo", Assignee: ""})

	stats, err := store.TaskStatsByAssignee()
	if err != nil {
		t.Fatalf("TaskStatsByAssignee: %v", err)
	}

	got := stats["riley"]
	if got.AssignedTasks != 2 {
		t.Errorf("riley assigned = %d, want 2", got.AssignedTasks)
	}
	if got.CompletedTasks != 3 {
		t.Errorf("riley completed = %d, want 3", got.CompletedTasks)
	}
	if got.SuccessRate == nil || *got.SuccessRate != 0.75 {
		t.Errorf("riley success_rate = %v, want 0.75", got.SuccessRate)
	}

	got = stats["alex"]
	if got.AssignedTasks != 1 || got.CompletedTasks != 0 {
		t.Errorf("alex stats wrong: %+v", got)
	}
	if got.SuccessRate != nil {
		t.Errorf("alex success_rate = %v, want nil (no terminal tasks)", got.SuccessRate)
	}

	if _, ok := stats[""]; ok {
		t.Error("unassigned tasks leaked into stats map under empty key")
	}
}

// TestHandleGetAgent_TaskStats checks the stats are surfaced on the
// single-agent endpoint.
func TestHandleGetAgent_TaskStats(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
	})
	_ = s.store.InsertTask(Task{ID: "t1", Title: "T", Status: "doing", Assignee: "riley"})
	_ = s.store.InsertTask(Task{ID: "t2", Title: "T", Status: "done", Assignee: "riley"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got AgentResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Stats.AssignedTasks != 1 {
		t.Errorf("assigned = %d, want 1", got.Stats.AssignedTasks)
	}
	if got.Stats.CompletedTasks != 1 {
		t.Errorf("completed = %d, want 1", got.Stats.CompletedTasks)
	}
}

// TestReportsTo_Inversion checks reports_to is derived from inverting team
// arrays in the document. Iris.team = [riley, alex] → riley.reports_to =
// ["iris"], alex.reports_to = ["iris"], iris.reports_to = [].
func TestReportsTo_Inversion(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"iris":  {Name: "iris", Model: "claude-sonnet-4-6", Team: []string{"riley", "alex"}},
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
		"alex":  {Name: "alex", Model: "claude-sonnet-4-6"},
		// Multi-supervisor: jordan reports to both iris (via separate team)
		// and a finance lead.
		"finance-lead": {Name: "finance-lead", Model: "claude-sonnet-4-6", Team: []string{"jordan"}},
		"jordan":       {Name: "jordan", Model: "claude-sonnet-4-6"},
	})

	cases := []struct {
		name       string
		wantSorted []string
	}{
		{"riley", []string{"iris"}},
		{"alex", []string{"iris"}},
		{"iris", nil},
		{"jordan", []string{"finance-lead"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+tc.name, nil)
			req.SetPathValue("name", tc.name)
			w := httptest.NewRecorder()
			s.handleGetAgent(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			var got AgentResponse
			_ = json.NewDecoder(w.Body).Decode(&got)
			if len(got.ReportsTo) != len(tc.wantSorted) {
				t.Errorf("reports_to = %v, want %v", got.ReportsTo, tc.wantSorted)
				return
			}
			// Order isn't guaranteed (map iteration), so compare as set.
			seen := map[string]bool{}
			for _, r := range got.ReportsTo {
				seen[r] = true
			}
			for _, want := range tc.wantSorted {
				if !seen[want] {
					t.Errorf("reports_to missing %q; got %v", want, got.ReportsTo)
				}
			}
		})
	}
}

// TestHandleUpdateAgent_PromotesYAMLAgent verifies the central #42 contract:
// a PUT against an agent that's defined in YAML (present in doc.Agents but
// absent from composed_agents) succeeds, gets promoted to composed_agents,
// and stamps an updated_at timestamp the frontend can render.
func TestHandleUpdateAgent_PromotesYAMLAgent(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"aria": {
			Name:        "aria",
			DisplayName: "Aria",
			Title:       "Orchestrator",
			Model:       "claude-sonnet-4-6",
			System:      "You are Aria, the orchestrator.",
		},
	})

	// Confirm precondition: aria isn't in composed_agents yet.
	composed, _ := s.store.ListComposedAgents()
	for _, a := range composed {
		if a.Name == "aria" {
			t.Fatalf("aria already in composed_agents; test invariant broken")
		}
	}

	newSystem := "You are Aria, the orchestrator. Edited by user."
	body := mustJSON(t, UpdateAgentRequest{System: &newSystem})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/aria", bytes.NewReader(body))
	req.SetPathValue("name", "aria")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Aria must now be in composed_agents with the new system prompt and
	// the YAML metadata (display_name, title) preserved.
	composed, _ = s.store.ListComposedAgents()
	var found *ComposedAgent
	for i := range composed {
		if composed[i].Name == "aria" {
			found = &composed[i]
			break
		}
	}
	if found == nil {
		t.Fatal("aria not promoted to composed_agents")
	}
	if found.System != newSystem {
		t.Errorf("system = %q, want %q", found.System, newSystem)
	}
	if found.DisplayName != "Aria" {
		t.Errorf("display_name not preserved from YAML: %q", found.DisplayName)
	}
	if found.Title != "Orchestrator" {
		t.Errorf("title not preserved from YAML: %q", found.Title)
	}
	if found.Model != "claude-sonnet-4-6" {
		t.Errorf("model not preserved from YAML: %q", found.Model)
	}
	if found.UpdatedAt.IsZero() {
		t.Error("updated_at not stamped on promote")
	}
}

// TestHandleUpdateAgent_SecondPUTAfterPromote confirms that once promoted,
// a YAML agent behaves identically to a composed one — subsequent PUTs work
// without re-promote logic firing.
func TestHandleUpdateAgent_SecondPUTAfterPromote(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"aria": {Name: "aria", Model: "claude-sonnet-4-6", System: "v1"},
	})

	first := "v2"
	body := mustJSON(t, UpdateAgentRequest{System: &first})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/aria", bytes.NewReader(body))
	req.SetPathValue("name", "aria")
	s.handleUpdateAgent(httptest.NewRecorder(), req)

	second := "v3"
	body = mustJSON(t, UpdateAgentRequest{System: &second})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/agents/aria", bytes.NewReader(body))
	req.SetPathValue("name", "aria")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, body = %s", w.Code, w.Body.String())
	}
	composed, _ := s.store.ListComposedAgents()
	for _, a := range composed {
		if a.Name == "aria" && a.System != "v3" {
			t.Errorf("system = %q after second PUT, want v3", a.System)
		}
	}
}

// TestHandleUpdateAgent_BuilderStill403 confirms promote-on-PUT doesn't
// accidentally widen the door — builder still can't be updated even
// though it's a YAML agent that "doesn't exist in composed_agents."
func TestHandleUpdateAgent_BuilderStill403(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"hera": {Name: "hera", Model: "claude-sonnet-4-6"},
	})
	newSystem := "hijack attempt"
	body := mustJSON(t, UpdateAgentRequest{System: &newSystem})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/hera", bytes.NewReader(body))
	req.SetPathValue("name", "hera")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

// TestHandleUpdateAgent_TrueUnknownStill404 confirms agents that exist in
// neither YAML nor composed_agents still return 404.
func TestHandleUpdateAgent_TrueUnknownStill404(t *testing.T) {
	s := agentTestServer(t, nil)
	newSystem := "edit"
	body := mustJSON(t, UpdateAgentRequest{System: &newSystem})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/ghost", bytes.NewReader(body))
	req.SetPathValue("name", "ghost")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

// TestHandleUpdateAgent_PartialPUT_PreservesGradient reproduces #40 BUG 1.
// PUT with {"icon": "Cpu"} (no avatar_gradient field) should leave the
// previously-set gradient intact. Cody reports the second PUT clears it.
func TestHandleUpdateAgent_PartialPUT_PreservesGradient(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"ceo": {
			Name:  "ceo",
			Model: "claude-sonnet-4-6",
			Icon:  "Sparkles",
		},
	})

	// First PUT: set the gradient.
	body1, _ := json.Marshal(map[string]any{
		"avatar_gradient": []string{"#0EA5E9", "#22D3EE"},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/ceo", bytes.NewReader(body1))
	req.SetPathValue("name", "ceo")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	// Second PUT: change icon only — gradient should survive.
	body2, _ := json.Marshal(map[string]any{"icon": "Cpu"})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/agents/ceo", bytes.NewReader(body2))
	req.SetPathValue("name", "ceo")
	w = httptest.NewRecorder()
	s.handleUpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	// GET: must return the gradient AND the new icon.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agents/ceo", nil)
	req.SetPathValue("name", "ceo")
	w = httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Icon != "Cpu" {
		t.Errorf("icon = %q, want Cpu (#40 BUG 2 — icon write didn't persist)", got.Icon)
	}
	if len(got.AvatarGradient) != 2 || got.AvatarGradient[0] != "#0EA5E9" {
		t.Errorf("avatar_gradient = %v, want [#0EA5E9, #22D3EE] (#40 BUG 1 — partial PUT cleared it)", got.AvatarGradient)
	}
}

// TestHandleGetAgent_IsOrchestratorFlag closes #49 Q3 — frontends need a
// canonical signal for "this agent is the tenant's orchestrator" that
// works regardless of what the tenant renamed Iris to.
func TestHandleGetAgent_IsOrchestratorFlag(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"kai":   {Name: "kai", Model: "claude-sonnet-4-6"},
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
	})
	// Tenant renamed orchestrator to "kai".
	s.cfg.Orchestrator = dsl.IrisConfig{Name: "kai"}

	cases := []struct {
		name             string
		wantOrchestrator bool
	}{
		{"kai", true},
		{"riley", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+tc.name, nil)
			req.SetPathValue("name", tc.name)
			w := httptest.NewRecorder()
			s.handleGetAgent(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			var got AgentResponse
			_ = json.NewDecoder(w.Body).Decode(&got)
			if got.IsOrchestrator != tc.wantOrchestrator {
				t.Errorf("is_orchestrator = %v, want %v", got.IsOrchestrator, tc.wantOrchestrator)
			}
		})
	}
}

// TestHandleGetAgent_OrchestratorTitle covers govega#59: the orchestrator
// (Iris by default, ARIA in tenants) must surface a populated `title` field
// — Cody flagged that the FE was reading it as empty / coming through on
// the description field instead. IrisAgent now sets def.Title from
// IrisConfig.Title (default "Orchestrator").
func TestHandleGetAgent_OrchestratorTitle(t *testing.T) {
	def := dsl.IrisAgent(dsl.DefaultIrisConfig())
	s := agentTestServer(t, map[string]*dsl.Agent{
		"iris": def,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/iris", nil)
	req.SetPathValue("name", "iris")
	w := httptest.NewRecorder()
	s.handleGetAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "Orchestrator" {
		t.Errorf("title = %q, want %q (govega#59 — title was empty)", got.Title, "Orchestrator")
	}
	if !got.IsOrchestrator {
		t.Errorf("is_orchestrator = false, want true")
	}
}

// TestHandleUpdateAgent_OrchestratorRenamePersistsAsSetting covers
// govega#58: renaming the orchestrator via PUT must survive a restart.
// The orchestrator is injected programmatically from cfg.Orchestrator
// (env-derived) on every boot, so persisting the rename to composed_agents
// produces a duplicate entry on the next boot instead of overriding the
// programmatic injection. Persist as settings instead, and on Start apply
// those settings to cfg before injectIris.
//
// This test pins the storage contract: a PUT that renames the orchestrator
// writes the new identity to the settings table under stable keys and does
// NOT create a composed_agent record.
func TestHandleUpdateAgent_OrchestratorRenamePersistsAsSetting(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"aria": {Name: "aria", DisplayName: "ARIA", Title: "Orchestrator", Model: "claude-sonnet-4-6", IsMeta: true},
	})
	s.cfg.Orchestrator = dsl.IrisConfig{Name: "aria", DisplayName: "ARIA", Title: "Orchestrator"}

	newName := "atlas"
	newDisplay := "Atlas"
	body := mustJSON(t, UpdateAgentRequest{Name: &newName, DisplayName: &newDisplay})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/aria", bytes.NewReader(body))
	req.SetPathValue("name", "aria")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Override settings must be written so a restart picks up the new name.
	wantSettings := map[string]string{
		orchestratorNameSettingKey:        "atlas",
		orchestratorDisplayNameSettingKey: "Atlas",
	}
	for key, want := range wantSettings {
		got, err := s.store.GetSetting(key)
		if err != nil {
			t.Fatalf("GetSetting %s: %v", key, err)
		}
		if got == nil || got.Value != want {
			t.Errorf("setting %s = %v, want %q", key, got, want)
		}
	}

	// In-memory cfg must follow the rename so isOrchestrator + dispatch
	// routing immediately reflect the new identity.
	if s.cfg.Orchestrator.Name != "atlas" {
		t.Errorf("cfg.Orchestrator.Name = %q, want atlas", s.cfg.Orchestrator.Name)
	}
	if s.cfg.Orchestrator.DisplayName != "Atlas" {
		t.Errorf("cfg.Orchestrator.DisplayName = %q, want Atlas", s.cfg.Orchestrator.DisplayName)
	}

	// Renamed orchestrator must NOT land in composed_agents — that'd
	// duplicate it on restart since injectIris also runs unconditionally.
	composed, _ := s.store.ListComposedAgents()
	for _, a := range composed {
		if a.Name == "atlas" || a.Name == "aria" {
			t.Errorf("orchestrator leaked into composed_agents: %q", a.Name)
		}
	}

	// The interpreter must now expose the renamed agent and have removed
	// the old slug.
	if _, ok := s.interp.Document().Agents["atlas"]; !ok {
		t.Error("interpreter has no agent named atlas after rename")
	}
	if _, ok := s.interp.Document().Agents["aria"]; ok {
		t.Error("interpreter still has old aria agent after rename")
	}
}

// TestApplyOrchestratorOverrides_FromSettings covers the boot-time side of
// the contract: when settings carry an override, the helper rewrites
// cfg.Orchestrator so injectIris uses the persisted identity.
func TestApplyOrchestratorOverrides_FromSettings(t *testing.T) {
	store := newTestStore(t)
	if err := store.UpsertSetting(Setting{Key: orchestratorNameSettingKey, Value: "atlas"}); err != nil {
		t.Fatalf("UpsertSetting name: %v", err)
	}
	if err := store.UpsertSetting(Setting{Key: orchestratorDisplayNameSettingKey, Value: "Atlas"}); err != nil {
		t.Fatalf("UpsertSetting display_name: %v", err)
	}
	if err := store.UpsertSetting(Setting{Key: orchestratorTitleSettingKey, Value: "Chief of Staff"}); err != nil {
		t.Fatalf("UpsertSetting title: %v", err)
	}

	cfg := dsl.IrisConfig{Name: "aria", DisplayName: "ARIA", Title: "Orchestrator"}
	got := applyOrchestratorOverrides(cfg, store)
	if got.Name != "atlas" {
		t.Errorf("Name = %q, want atlas", got.Name)
	}
	if got.DisplayName != "Atlas" {
		t.Errorf("DisplayName = %q, want Atlas", got.DisplayName)
	}
	if got.Title != "Chief of Staff" {
		t.Errorf("Title = %q, want Chief of Staff", got.Title)
	}
}

// TestApplyOrchestratorOverrides_NoSettingsIsPassthrough confirms the
// helper is a no-op when no overrides are persisted — the env-derived
// cfg is returned unchanged.
func TestApplyOrchestratorOverrides_NoSettingsIsPassthrough(t *testing.T) {
	store := newTestStore(t)
	cfg := dsl.IrisConfig{Name: "aria", DisplayName: "ARIA", Title: "Orchestrator"}
	got := applyOrchestratorOverrides(cfg, store)
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("expected passthrough, got %+v", got)
	}
}

// TestHandleUpdateAgent_OrchestratorRenameRejectsBuilderConflict guards
// against accidentally clobbering the builder by renaming the orchestrator
// to its slug.
func TestHandleUpdateAgent_OrchestratorRenameRejectsBuilderConflict(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"aria": {Name: "aria", Model: "claude-sonnet-4-6", IsMeta: true},
		"hera": {Name: "hera", Model: "claude-sonnet-4-6", IsMeta: true},
	})
	s.cfg.Orchestrator = dsl.IrisConfig{Name: "aria"}
	s.cfg.Builder = dsl.HeraConfig{Name: "hera"}

	conflict := "hera"
	body := mustJSON(t, UpdateAgentRequest{Name: &conflict})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/aria", bytes.NewReader(body))
	req.SetPathValue("name", "aria")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for rename collision with builder; body = %s", w.Code, w.Body.String())
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
