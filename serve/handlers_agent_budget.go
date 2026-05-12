package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	vega "github.com/everydev1618/govega"
)

// --- Per-agent budget caps + enforcement — refs govega#47 ---
//
// Per Cody's spec on the issue:
//
//   - Per-agent only for v1 (workspace caps compose on top later when
//     WorkOS tenancy lands).
//   - Soft alert at soft_alert_threshold (default 0.8) — UI warning
//     band only, no behavioral change.
//   - Hard cutoff when observed_spend >= budget_cap AND enabled AND
//     budget_cap != null. If any of those is false, no cutoff.
//   - Billing period is always calendar-month UTC.
//   - In-flight conversation behavior: finish the current turn
//     cleanly (we don't interrupt mid-response anyway — the running
//     LLM call completes), then refuse subsequent turns with the
//     "budget cap reached" message. Enforcement lives at the chat
//     handler entry; see handlers_chat_budget.go for that wiring.

// handleGetAgentBudget returns the agent's current budget plus a
// fresh observed_spend rollup for the calendar-month-UTC period.
//
// GET /api/v1/agents/{name}/budget
func (s *Server) handleGetAgentBudget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}
	resp, err := s.buildAgentBudgetResponse(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateAgentBudget is the cap-set / threshold-tune / enable
// toggle endpoint. Partial — omitted fields stay.
//
// PUT /api/v1/agents/{name}/budget
func (s *Server) handleUpdateAgentBudget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}

	var req UpdateAgentBudgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body"})
		return
	}

	existing, err := s.store.GetAgentBudget(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	merged := AgentBudget{AgentName: name, SoftAlertThreshold: 0.8}
	if existing != nil {
		merged = *existing
	}
	if req.BudgetCap != nil {
		// FE sends a non-null cap to set, or omits the key to leave it.
		// To clear the cap explicitly, FE sends "budget_cap": null —
		// but the JSON decoder leaves that as a nil *float64 same as
		// omitted. Resolution: we treat a literally-present cap of 0
		// as "clear" (FE convention) since a 0 cap is meaningless
		// otherwise.
		if *req.BudgetCap <= 0 {
			merged.BudgetCap = nil
		} else {
			cap := *req.BudgetCap
			merged.BudgetCap = &cap
		}
	}
	if req.SoftAlertThreshold != nil {
		t := *req.SoftAlertThreshold
		if t < 0 || t > 1 {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "soft_alert_threshold must be between 0 and 1"})
			return
		}
		merged.SoftAlertThreshold = t
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
	}

	if err := s.store.UpsertAgentBudget(merged); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	resp, err := s.buildAgentBudgetResponse(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// buildAgentBudgetResponse composes the budget row + period bounds +
// current-period spend rollup into the wire shape. Shared by GET and
// PUT — they both return the same "current full state" response so
// the FE doesn't need a follow-up read.
func (s *Server) buildAgentBudgetResponse(agentName string) (*AgentBudgetResponse, error) {
	row, err := s.store.GetAgentBudget(agentName)
	if err != nil {
		return nil, err
	}
	periodStart, periodEnd := currentMonthUTC()
	spend, err := s.store.AgentSpendInPeriod(agentName, periodStart, periodEnd)
	if err != nil {
		return nil, err
	}
	out := &AgentBudgetResponse{
		Agent:              agentName,
		SoftAlertThreshold: 0.8,
		PeriodStart:        periodStart,
		PeriodEnd:          periodEnd,
		ObservedSpend:      spend,
	}
	if row != nil {
		out.BudgetCap = row.BudgetCap
		out.SoftAlertThreshold = row.SoftAlertThreshold
		out.Enabled = row.Enabled
	}
	return out, nil
}

// currentMonthUTC returns the bounds of the current calendar month in
// UTC — the single source of truth for the budget period boundary.
func currentMonthUTC() (start, end time.Time) {
	now := time.Now().UTC()
	start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	return start, end
}

// agentOverBudget reports whether agentName has tripped the hard
// cutoff — observed spend in the current month exceeds the configured
// cap, the cap is non-null, and enforcement is enabled. Used by the
// chat handler entrypoints to refuse subsequent turns with the
// budget-cap message. Refs govega#47.
func (s *Server) agentOverBudget(agentName string) bool {
	row, err := s.store.GetAgentBudget(agentName)
	if err != nil || row == nil || !row.Enabled || row.BudgetCap == nil {
		return false
	}
	start, end := currentMonthUTC()
	spend, err := s.store.AgentSpendInPeriod(agentName, start, end)
	if err != nil {
		return false
	}
	return spend >= *row.BudgetCap
}

// budgetCapMessage is the canned response surfaced when an agent is
// over budget. Mirrors the Claude-Code-style "I can't help further
// this period" framing per Cody's spec — no silent failures.
const budgetCapMessage = "I'm at my monthly spending limit for this period. Raise the budget cap or wait for the next billing cycle to continue."

// writeBudgetCapStream emits an SSE response that mirrors a real
// streamed completion — one text_delta carrying the canned message,
// then a done event — so the FE renderer doesn't need a special-case
// for budget-blocked turns. Used by handleChatStream after a cap trip.
func (s *Server) writeBudgetCapStream(w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fall back to a plain JSON response when streaming isn't
		// available (tests with httptest.NewRecorder hit this).
		writeJSON(w, http.StatusOK, map[string]string{"response": budgetCapMessage})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	deltaData, _ := json.Marshal(vega.ChatEvent{
		Type:  vega.ChatEventTextDelta,
		Delta: budgetCapMessage,
	})
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", vega.ChatEventTextDelta, deltaData)
	flusher.Flush()

	doneData, _ := json.Marshal(vega.ChatEvent{Type: vega.ChatEventDone})
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
	flusher.Flush()
}
