package serve

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everydev1618/govega/events"
)

// The external webhook endpoint publishes signal.<name> onto the spine so
// outside systems can wake agents.
func TestHandleEmitEvent(t *testing.T) {
	s := &Server{bus: events.NewBus()}
	defer s.bus.Close()
	sub := s.bus.Subscribe(events.Durable, nil)
	defer sub.Close()

	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{"name":"deploy_ready","data":{"env":"prod"}}`))
	w := httptest.NewRecorder()
	s.handleEmitEvent(w, req)

	if w.Code != 202 {
		t.Fatalf("want 202 Accepted, got %d (%s)", w.Code, w.Body.String())
	}
	select {
	case e := <-sub.C:
		if e.Type != "signal.deploy_ready" {
			t.Fatalf("want signal.deploy_ready, got %q", e.Type)
		}
		if e.Data["env"] != "prod" || e.Data["source"] != "external" {
			t.Fatalf("event data not carried through: %v", e.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("webhook did not publish onto the spine")
	}
}

func TestHandleEmitEventRejectsEmptyName(t *testing.T) {
	s := &Server{bus: events.NewBus()}
	defer s.bus.Close()
	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{"data":{"x":1}}`))
	w := httptest.NewRecorder()
	s.handleEmitEvent(w, req)
	if w.Code != 400 {
		t.Fatalf("want 400 for missing name, got %d", w.Code)
	}
}

// The spine is persisted to the durable events log so history survives restart.
func TestPersistSpineEvents(t *testing.T) {
	s := &Server{store: newTestStore(t), bus: events.NewBus()}
	defer s.bus.Close()

	go s.persistSpineEvents(t.Context())
	time.Sleep(50 * time.Millisecond) // let the durable subscription attach

	s.bus.Publish(events.Event{Type: "signal.persisted", Data: map[string]any{"agent": "x"}})

	deadline := time.After(2 * time.Second)
	for {
		evs, _, err := s.store.SearchEvents(ActivityFilter{Type: "signal.persisted"})
		if err != nil {
			t.Fatalf("SearchEvents: %v", err)
		}
		if len(evs) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("spine event was not persisted to the durable log")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// The reactive-activity endpoint returns only reactive.* rows.
func TestHandleReactiveActivity(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	_ = store.InsertEvent(StoreEvent{Type: "reactive.fired", AgentName: "watcher", Timestamp: now})
	_ = store.InsertEvent(StoreEvent{Type: "reactive.gated:rate", AgentName: "watcher", Timestamp: now})
	_ = store.InsertEvent(StoreEvent{Type: "agent.completed", AgentName: "builder", Timestamp: now})
	_ = store.InsertEvent(StoreEvent{Type: "signal.deploy", AgentName: "", Timestamp: now})
	_ = store.InsertEvent(StoreEvent{Type: "process.completed", AgentName: "other", Timestamp: now}) // noise, excluded

	s := &Server{store: store}
	req := httptest.NewRequest("GET", "/api/v1/reactive/activity", nil)
	w := httptest.NewRecorder()
	s.handleReactiveActivity(w, req)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var body struct {
		Events []StoreEvent `json:"events"`
		Count  int          `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range body.Events {
		if e.Type == "process.completed" {
			t.Fatalf("non-reactive noise leaked into reactive activity: %q", e.Type)
		}
		seen[e.Type] = true
	}
	// Both the router audit and the spine families should be present.
	for _, want := range []string{"reactive.fired", "reactive.gated:rate", "agent.completed", "signal.deploy"} {
		if !seen[want] {
			t.Fatalf("expected %q in reactive activity, got types %v", want, seen)
		}
	}
}
