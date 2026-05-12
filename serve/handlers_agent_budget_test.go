package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everydev1618/govega/dsl"
)

// budgetTestServer is the minimum a budget-handler test needs:
// real interpreter so the agent exists, store for budget rows +
// process snapshots, broker.
func budgetTestServer(t *testing.T, agentName string) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents:   map[string]*dsl.Agent{agentName: {Name: agentName, Model: "claude-sonnet-4-6"}},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
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

// TestAgentBudget_GetReturnsDefaultsForMissingRow pins the empty-state
// shape — no budget row means no cap, default threshold, disabled,
// current calendar-month UTC period, zero observed spend.
func TestAgentBudget_GetReturnsDefaultsForMissingRow(t *testing.T) {
	s := budgetTestServer(t, "riley")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/riley/budget", nil)
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleGetAgentBudget(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentBudgetResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.BudgetCap != nil {
		t.Errorf("budget_cap = %v, want nil", got.BudgetCap)
	}
	if got.SoftAlertThreshold != 0.8 {
		t.Errorf("soft_alert_threshold = %v, want 0.8", got.SoftAlertThreshold)
	}
	if got.Enabled {
		t.Error("enabled should default to false")
	}
	if got.ObservedSpend != 0 {
		t.Errorf("observed_spend = %v, want 0", got.ObservedSpend)
	}
	// Period bounds must be the current calendar month UTC.
	now := time.Now().UTC()
	wantStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !got.PeriodStart.Equal(wantStart) {
		t.Errorf("period_start = %v, want %v", got.PeriodStart, wantStart)
	}
}

// TestAgentBudget_PutSetsCapAndReadback exercises the round-trip:
// PUT writes, GET reads back, observed_spend reflects current period.
func TestAgentBudget_PutSetsCapAndReadback(t *testing.T) {
	s := budgetTestServer(t, "riley")

	// Seed a snapshot so observed_spend is non-zero.
	_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
		ProcessID: "p1", AgentName: "riley", Status: "completed",
		CostUSD: 3.5, StartedAt: time.Now().UTC(),
	})

	cap := 10.0
	threshold := 0.5
	enabled := true
	body := mustJSON(t, UpdateAgentBudgetRequest{
		BudgetCap: &cap, SoftAlertThreshold: &threshold, Enabled: &enabled,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/riley/budget", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleUpdateAgentBudget(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}
	var got AgentBudgetResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.BudgetCap == nil || *got.BudgetCap != 10.0 {
		t.Errorf("budget_cap = %v, want 10.0", got.BudgetCap)
	}
	if got.SoftAlertThreshold != 0.5 || !got.Enabled {
		t.Errorf("threshold/enabled wrong: %+v", got)
	}
	if got.ObservedSpend != 3.5 {
		t.Errorf("observed_spend = %v, want 3.5", got.ObservedSpend)
	}
}

// TestAgentBudget_PutClearCap confirms FE convention: sending cap <= 0
// clears the cap (treats it as "no cap configured").
func TestAgentBudget_PutClearCap(t *testing.T) {
	s := budgetTestServer(t, "riley")
	cap := 10.0
	en := true
	body := mustJSON(t, UpdateAgentBudgetRequest{BudgetCap: &cap, Enabled: &en})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/riley/budget", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	s.handleUpdateAgentBudget(httptest.NewRecorder(), req)

	// Clear by sending 0.
	zero := 0.0
	body = mustJSON(t, UpdateAgentBudgetRequest{BudgetCap: &zero})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/agents/riley/budget", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleUpdateAgentBudget(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT clear status = %d", w.Code)
	}
	var got AgentBudgetResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.BudgetCap != nil {
		t.Errorf("budget_cap = %v, want nil after clear", got.BudgetCap)
	}
}

// TestAgentBudget_PutRejectsBadThreshold guards the [0,1] bound on
// the soft-alert threshold.
func TestAgentBudget_PutRejectsBadThreshold(t *testing.T) {
	s := budgetTestServer(t, "riley")
	bad := 1.5
	body := mustJSON(t, UpdateAgentBudgetRequest{SoftAlertThreshold: &bad})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/riley/budget", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleUpdateAgentBudget(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for out-of-range threshold", w.Code)
	}
}

// TestAgentOverBudget covers the four enforcement conditions —
// cap+enabled+spent_over fires the cutoff; any one of those missing
// or unset means no cutoff.
func TestAgentOverBudget(t *testing.T) {
	s := budgetTestServer(t, "riley")
	insertSnapshot := func(cost float64) {
		t.Helper()
		_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
			ProcessID: "p" + t.Name(), AgentName: "riley", Status: "completed",
			CostUSD: cost, StartedAt: time.Now().UTC(),
		})
	}

	// No row at all → no cutoff.
	if s.agentOverBudget("riley") {
		t.Error("no budget row should not trigger cutoff")
	}

	// Cap set but disabled → no cutoff.
	cap := 5.0
	_ = s.store.UpsertAgentBudget(AgentBudget{AgentName: "riley", BudgetCap: &cap, SoftAlertThreshold: 0.8, Enabled: false})
	insertSnapshot(10.0) // over cap
	if s.agentOverBudget("riley") {
		t.Error("disabled enforcement should not trigger cutoff")
	}

	// Enabled + cap + under spend → no cutoff.
	_ = s.store.UpsertAgentBudget(AgentBudget{AgentName: "riley", BudgetCap: &cap, SoftAlertThreshold: 0.8, Enabled: true})
	// Wipe + reseed with under-cap spend.
	if err := s.store.ResetData(); err != nil {
		t.Fatalf("ResetData: %v", err)
	}
	_ = s.store.UpsertAgentBudget(AgentBudget{AgentName: "riley", BudgetCap: &cap, SoftAlertThreshold: 0.8, Enabled: true})
	insertSnapshot(2.0) // under
	if s.agentOverBudget("riley") {
		t.Error("spend below cap should not trigger cutoff")
	}

	// Enabled + cap + over spend → cutoff.
	insertSnapshot(99.0)
	if !s.agentOverBudget("riley") {
		t.Error("spend over cap with enabled should trigger cutoff")
	}
}

// TestChatHandler_BlockedWhenOverBudget covers the user-visible side:
// /api/v1/agents/{name}/chat returns the canned budget message and
// persists a user/assistant pair, without invoking the LLM.
func TestChatHandler_BlockedWhenOverBudget(t *testing.T) {
	s := budgetTestServer(t, "riley")

	// Set cap = 1.0, enabled, with 5.0 spend → cutoff trips.
	cap := 1.0
	_ = s.store.UpsertAgentBudget(AgentBudget{AgentName: "riley", BudgetCap: &cap, SoftAlertThreshold: 0.8, Enabled: true})
	_ = s.store.InsertProcessSnapshot(ProcessSnapshot{
		ProcessID: "p1", AgentName: "riley", Status: "completed",
		CostUSD: 5.0, StartedAt: time.Now().UTC(),
	})

	body := []byte(`{"message":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/riley/chat", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	w := httptest.NewRecorder()
	s.handleChat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.NewDecoder(w.Body).Decode(&got)
	if !strings.Contains(got["response"], "spending limit") {
		t.Errorf("response = %q, want budget cap message", got["response"])
	}

	// Verify the user + assistant messages landed in chat history.
	msgs, _ := s.store.ListChatMessages("riley")
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("expected user+assistant pair persisted; got %+v", msgs)
	}
}
