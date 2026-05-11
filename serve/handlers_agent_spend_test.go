package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everydev1618/govega/dsl"
)

// TestAgentSpend_CurrentMonthRollup pins the govega#47 contract: the
// per-agent spend endpoint sums cost_usd across the latest snapshot of
// every process for the agent within the queried period. apex-host-mgmt
// uses this to render its observed_spend column without fanning out
// /processes per agent.
func TestAgentSpend_CurrentMonthRollup(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
	})

	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 5, 12, 0, 0, 0, time.UTC)
	lastMonth := thisMonth.AddDate(0, -1, 0)

	// Seed three snapshots: two in this month (one is a later snapshot
	// of the same process so we exercise the "latest only" rule), one
	// in the prior month so the period filter excludes it.
	insert := func(processID string, started time.Time, cost float64) {
		t.Helper()
		if err := s.store.InsertProcessSnapshot(ProcessSnapshot{
			ProcessID: processID,
			AgentName: "riley",
			Status:    "completed",
			CostUSD:   cost,
			StartedAt: started,
		}); err != nil {
			t.Fatalf("InsertProcessSnapshot: %v", err)
		}
	}
	insert("p1", thisMonth, 1.0)
	insert("p1", thisMonth, 3.0) // later snapshot — should overwrite earlier
	insert("p2", thisMonth, 2.5)
	insert("p3", lastMonth, 99.0) // excluded by period

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/spend?period=current", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgentSpend(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentSpendResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := 3.0 + 2.5 // latest of p1 plus p2; p3 excluded
	if got.ObservedSpend != want {
		t.Errorf("observed_spend = %v, want %v", got.ObservedSpend, want)
	}
	if got.Period != "current" {
		t.Errorf("period = %q, want current", got.Period)
	}
	if got.PeriodStart.Day() != 1 {
		t.Errorf("period_start should be first of month: %v", got.PeriodStart)
	}
}

// TestAgentSpend_PeriodAll returns lifetime spend.
func TestAgentSpend_PeriodAll(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
	})
	now := time.Now().UTC()
	old := time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC)
	_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
		ProcessID: "old", AgentName: "riley", Status: "completed", CostUSD: 5.0, StartedAt: old,
	})
	_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
		ProcessID: "now", AgentName: "riley", Status: "completed", CostUSD: 1.5, StartedAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/spend?period=all", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgentSpend(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got AgentSpendResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.ObservedSpend != 6.5 {
		t.Errorf("lifetime spend = %v, want 6.5", got.ObservedSpend)
	}
}

// TestAgentSpend_PerAgentScoping makes sure one agent's spend doesn't
// leak into another's rollup.
func TestAgentSpend_PerAgentScoping(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
		"alex":  {Name: "alex", Model: "claude-sonnet-4-6"},
	})
	now := time.Now().UTC()
	_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
		ProcessID: "r1", AgentName: "riley", Status: "completed", CostUSD: 4.0, StartedAt: now,
	})
	_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
		ProcessID: "a1", AgentName: "alex", Status: "completed", CostUSD: 7.0, StartedAt: now,
	})

	for _, tc := range []struct {
		agent string
		want  float64
	}{
		{"riley", 4.0},
		{"alex", 7.0},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+tc.agent+"/spend?period=all", nil)
		req.SetPathValue("name", tc.agent)
		w := httptest.NewRecorder()
		s.handleGetAgentSpend(w, req)
		var got AgentSpendResponse
		_ = json.NewDecoder(w.Body).Decode(&got)
		if got.ObservedSpend != tc.want {
			t.Errorf("%s observed_spend = %v, want %v", tc.agent, got.ObservedSpend, tc.want)
		}
	}
}

// TestAgentSpend_UnknownAgent returns 404.
func TestAgentSpend_UnknownAgent(t *testing.T) {
	s := agentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/ghost/spend", nil)
	req.SetPathValue("name", "ghost")
	w := httptest.NewRecorder()
	s.handleGetAgentSpend(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestAgentSpend_RejectsUnknownPeriod returns 400.
func TestAgentSpend_RejectsUnknownPeriod(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/spend?period=last_year", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgentSpend(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
