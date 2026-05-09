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
		name      string
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
