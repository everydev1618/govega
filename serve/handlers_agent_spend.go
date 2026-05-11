package serve

import (
	"net/http"
	"time"
)

// --- Per-agent spend rollup — refs govega#47 ---
//
// apex-host-mgmt's agents list currently fans out /api/v1/processes per
// page render and sums cost_usd client-side to populate the
// "observed_spend" column — O(processes) per pageview. This endpoint
// gives the FE a single-roundtrip rollup it can call per agent without
// downloading every process record.

// handleGetAgentSpend returns the agent's observed spend for the queried
// period. `period=current` (default) is the current calendar month UTC;
// `period=all` returns lifetime spend.
//
// GET /api/v1/agents/{name}/spend?period=current|all
func (s *Server) handleGetAgentSpend(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.brainAgentExists(name) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "agent not found"})
		return
	}
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "current"
	}
	var from, to time.Time
	switch period {
	case "current":
		now := time.Now().UTC()
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		to = from.AddDate(0, 1, 0)
	case "all":
		// unbounded — leave from/to zero
	default:
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "period must be 'current' or 'all'"})
		return
	}
	total, err := s.store.AgentSpendInPeriod(name, from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, AgentSpendResponse{
		Agent:         name,
		ObservedSpend: total,
		Period:        period,
		PeriodStart:   from,
		PeriodEnd:     to,
	})
}
