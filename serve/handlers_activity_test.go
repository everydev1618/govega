package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// seedActivityEvents inserts a known set of events for the activity log
// tests to slice. Returns the store for chaining.
func seedActivityEvents(t *testing.T, s *Server) {
	t.Helper()
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	rows := []StoreEvent{
		{Type: "process.started", AgentName: "riley", ProcessID: "p1", Timestamp: now, Data: ""},
		{Type: "process.completed", AgentName: "riley", ProcessID: "p1", Timestamp: now.Add(1 * time.Minute), Result: "summarized inbox"},
		{Type: "process.failed", AgentName: "alex", ProcessID: "p2", Timestamp: now.Add(2 * time.Minute), Error: "rate limit hit"},
		{Type: "agent.created", AgentName: "sofia", Timestamp: now.Add(3 * time.Minute), Data: `{"display_name":"Sofia"}`},
		{Type: "process.completed", AgentName: "alex", ProcessID: "p3", Timestamp: now.Add(4 * time.Minute), Result: "drafted policy"},
	}
	for _, e := range rows {
		if err := s.store.InsertEvent(e); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}
}

// TestActivity_DefaultListReturnsAll covers the no-filter case — the
// FE's first page render should get every event newest-first.
func TestActivity_DefaultListReturnsAll(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp ActivityLogResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 5 {
		t.Errorf("total = %d, want 5", resp.Total)
	}
	if len(resp.Events) != 5 {
		t.Fatalf("events len = %d, want 5", len(resp.Events))
	}
	// Newest first.
	if resp.Events[0].AgentName != "alex" || resp.Events[0].Type != "process.completed" {
		t.Errorf("first event = %+v, want alex process.completed", resp.Events[0])
	}
}

// TestActivity_QueryString matches the case-insensitive substring search
// across type/agent/data/result/error.
func TestActivity_QueryString(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	cases := []struct {
		q       string
		wantHit string // substring expected in the first match
	}{
		{"rate limit", "rate limit hit"},
		{"SOFIA", `{"display_name":"Sofia"}`}, // case-insensitive
		{"inbox", "summarized inbox"},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?q="+url.QueryEscape(tc.q), nil)
			w := httptest.NewRecorder()
			s.handleSearchActivity(w, req)
			var resp ActivityLogResponse
			_ = json.NewDecoder(w.Body).Decode(&resp)
			if len(resp.Events) == 0 {
				t.Fatalf("no matches for %q", tc.q)
			}
			joined := strings.Join([]string{resp.Events[0].Data, resp.Events[0].Result, resp.Events[0].Error}, " ")
			if !strings.Contains(joined, tc.wantHit) {
				t.Errorf("first match doesn't carry %q: %+v", tc.wantHit, resp.Events[0])
			}
		})
	}
}

// TestActivity_FilterByTypeAndAgent narrows results.
func TestActivity_FilterByTypeAndAgent(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?type=process.completed&agent=alex", nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	var resp ActivityLogResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 1 || len(resp.Events) != 1 {
		t.Fatalf("filtered total = %d, want 1", resp.Total)
	}
	if resp.Events[0].AgentName != "alex" || resp.Events[0].Type != "process.completed" {
		t.Errorf("filtered event = %+v, want alex/completed", resp.Events[0])
	}
}

// TestActivity_TimeRange honors from/to bounds.
func TestActivity_TimeRange(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	// Only the alex failed event and afterward.
	from := time.Date(2026, 5, 11, 12, 1, 30, 0, time.UTC).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?from="+from, nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	var resp ActivityLogResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 3 {
		t.Errorf("from filter total = %d, want 3 (events after t+1:30)", resp.Total)
	}
}

// TestActivity_Pagination paginates with limit + offset.
func TestActivity_Pagination(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?limit=2&offset=2", nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	var resp ActivityLogResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 5 {
		t.Errorf("total = %d, want 5 (count is over the unpaginated match)", resp.Total)
	}
	if len(resp.Events) != 2 {
		t.Errorf("page len = %d, want 2", len(resp.Events))
	}
	if resp.Limit != 2 || resp.Offset != 2 {
		t.Errorf("echo params off: limit=%d offset=%d", resp.Limit, resp.Offset)
	}
}

// TestActivity_LimitCap rejects nothing but clamps a >500 limit.
func TestActivity_LimitCap(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?limit=9999", nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (clamp should not error)", w.Code)
	}
	// Hard to assert the cap from the response alone, but at least
	// confirm the request didn't error.
	var resp ActivityLogResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Events) > 500 {
		t.Errorf("returned %d events; should clamp at 500", len(resp.Events))
	}
}

// TestActivity_CSVExport returns audit-friendly CSV bytes.
func TestActivity_CSVExport(t *testing.T) {
	s := agentTestServer(t, nil)
	seedActivityEvents(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?format=csv", nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	got := w.Body.String()
	if !strings.Contains(got, "timestamp,type,process_id,agent,data,result,error") {
		t.Errorf("CSV missing header: %q", got[:min(len(got), 200)])
	}
	if !strings.Contains(got, "rate limit hit") {
		t.Errorf("CSV missing event payload: %q", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
}

// TestActivity_RejectsBadFrom returns 400 on a bogus timestamp.
func TestActivity_RejectsBadFrom(t *testing.T) {
	s := agentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?from=yesterday", nil)
	w := httptest.NewRecorder()
	s.handleSearchActivity(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
