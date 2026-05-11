package serve

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// --- Activity log — refs govega#33 ---
//
// The /events SSE stream is unfiltered and live-only; the user has
// nowhere to retrace history when something goes wrong. This endpoint
// gives them a queryable view over the `events` table — the foundation
// for the activity log screen called for in #33. Inbox-vs-Tasks
// repositioning lives separately.

// handleSearchActivity exposes /api/v1/activity. Query params:
//
//	q       — case-insensitive substring across type/agent/data/result/error
//	type    — filter by event type (e.g. "process.failed")
//	agent   — filter by agent name
//	from    — RFC 3339 lower bound on timestamp (inclusive)
//	to      — RFC 3339 upper bound on timestamp (exclusive)
//	limit   — default 100, max 500
//	offset  — pagination cursor
//	format  — "json" (default) or "csv" for audit export
func (s *Server) handleSearchActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := ActivityFilter{
		Query: q.Get("q"),
		Type:  q.Get("type"),
		Agent: q.Get("agent"),
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid 'from' (RFC 3339 required): " + err.Error()})
			return
		}
		filter.From = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid 'to' (RFC 3339 required): " + err.Error()})
			return
		}
		filter.To = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "limit must be a non-negative integer"})
			return
		}
		filter.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "offset must be a non-negative integer"})
			return
		}
		filter.Offset = n
	}

	events, total, err := s.store.SearchEvents(filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if events == nil {
		events = []StoreEvent{}
	}

	if strings.EqualFold(q.Get("format"), "csv") {
		writeActivityCSV(w, events)
		return
	}

	effectiveLimit := filter.Limit
	if effectiveLimit <= 0 {
		effectiveLimit = 100
	}
	writeJSON(w, http.StatusOK, ActivityLogResponse{
		Events: events,
		Total:  total,
		Limit:  effectiveLimit,
		Offset: filter.Offset,
	})
}

// writeActivityCSV writes a CSV download of the matched events. Audit-
// friendly column order: time first, then identity, then payload.
func writeActivityCSV(w http.ResponseWriter, events []StoreEvent) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="activity.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"timestamp", "type", "process_id", "agent", "data", "result", "error"})
	for _, e := range events {
		_ = cw.Write([]string{
			e.Timestamp.UTC().Format(time.RFC3339),
			e.Type,
			e.ProcessID,
			e.AgentName,
			e.Data,
			e.Result,
			e.Error,
		})
	}
	cw.Flush()
}
