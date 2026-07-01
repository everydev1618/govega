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

// handleReactiveActivity is a convenience view over the audit trail: the
// router's decisions (reactive.fired / reactive.gated:*) and the spine events,
// so an operator can see what the house made each agent think about — and what
// it ignored. Backed by the same events table as /api/v1/activity.
func (s *Server) handleReactiveActivity(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// Query is a case-insensitive LIKE; "reactive." matches the router's audit
	// rows. We then keep only reactive.* / spine event types.
	evs, _, err := s.store.SearchEvents(ActivityFilter{Query: "reactive.", Limit: limit})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]StoreEvent, 0, len(evs))
	for _, e := range evs {
		if strings.HasPrefix(e.Type, "reactive.") {
			out = append(out, e)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "count": len(out)})
}
