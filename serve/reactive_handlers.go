package serve

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/everydev1618/govega/events"
)

// emitEventRequest is the body of POST /api/v1/events.
type emitEventRequest struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data,omitempty"`
}

// handleEmitEvent is the external sensor entry point: an authenticated caller
// (webhook, integration, script) publishes a signal onto the reactive spine, so
// agents subscribed to signal.<name> can wake. This is how the outside world
// reaches reactive cognition without a bespoke integration per source.
func (s *Server) handleEmitEvent(w http.ResponseWriter, r *http.Request) {
	var req emitEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON body: " + err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "name is required"})
		return
	}
	data := req.Data
	if data == nil {
		data = map[string]any{}
	}
	data["source"] = "external"

	e := s.bus.Publish(events.Event{Type: "signal." + name, Data: data})
	writeJSON(w, http.StatusAccepted, map[string]string{"event": e.Type, "id": e.ID})
}

// handleReactiveActivity is the reactive view's data source: the router's
// decisions (reactive.fired / reactive.gated:*) and the spine events agents
// react to (agent.completed, signal.*, schedule.fired, memory.wrote), so an
// operator can see what the house made each agent think about — and what it
// ignored. Backed by the same events table as /api/v1/activity; process.* and
// other non-reactive noise is filtered out.
func (s *Server) handleReactiveActivity(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// Fetch a wider recent window so reactive events aren't crowded out by
	// process.* noise, then keep only the reactive families up to limit.
	fetchN := limit * 4
	if fetchN > 500 {
		fetchN = 500 // store clamps here anyway
	}
	evs, _, err := s.store.SearchEvents(ActivityFilter{Limit: fetchN})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]StoreEvent, 0, limit)
	for _, e := range evs {
		if isReactiveEventType(e.Type) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "count": len(out)})
}

// isReactiveEventType reports whether an event belongs to the reactive view:
// the router's audit decisions and the spine event families agents react to.
func isReactiveEventType(t string) bool {
	switch {
	case strings.HasPrefix(t, "reactive."),
		strings.HasPrefix(t, "signal."),
		t == "agent.completed",
		t == "agent.said",
		t == "schedule.fired",
		t == "memory.wrote":
		return true
	}
	return false
}
