package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// channelsTestServer wires the minimum a *Server needs to drive the
// channel HTTP handlers: a real SQLite store, a broker so we can subscribe
// and assert lifecycle events, and an interpreter (DSL tool path tests
// register channel tools against it).
func channelsTestServer(t *testing.T) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents:   map[string]*dsl.Agent{},
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
	}
}

func findEvent(events []BrokerEvent, eventType string) *BrokerEvent {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}

// TestHandleCreateChannel_EmitsBrokerEvent pins the fix for the FE bug
// where Tony creates a channel server-side but the sidebar never reflects
// it. govega must publish channel.created on the broker so connected SSE
// clients can reconcile.
func TestHandleCreateChannel_EmitsBrokerEvent(t *testing.T) {
	s := channelsTestServer(t)
	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	body := `{"name":"design-review","description":"design crit","team":["riley"],"mode":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreateChannel(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	got := drainBroker(sub)
	ev := findEvent(got, "channel.created")
	if ev == nil {
		t.Fatalf("missing channel.created event; got = %+v", got)
	}
	data, ok := ev.Data.(map[string]any)
	if !ok {
		t.Fatalf("event Data not a map: %T", ev.Data)
	}
	if name, _ := data["name"].(string); name != "design-review" {
		t.Errorf("event name = %q, want design-review", name)
	}
}

func TestHandleDeleteChannel_EmitsBrokerEvent(t *testing.T) {
	s := channelsTestServer(t)
	if err := s.store.CreateChannel("ch_x", "design-review", "", "system", []string{"riley"}, ""); err != nil {
		t.Fatalf("seed CreateChannel: %v", err)
	}

	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/channels/design-review", nil)
	req.SetPathValue("name", "design-review")
	w := httptest.NewRecorder()
	s.handleDeleteChannel(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	got := drainBroker(sub)
	ev := findEvent(got, "channel.deleted")
	if ev == nil {
		t.Fatalf("missing channel.deleted event; got = %+v", got)
	}
	data, _ := ev.Data.(map[string]any)
	if name, _ := data["name"].(string); name != "design-review" {
		t.Errorf("event name = %q, want design-review", name)
	}
}

func TestHandleUpdateChannel_EmitsBrokerEvent(t *testing.T) {
	s := channelsTestServer(t)
	if err := s.store.CreateChannel("ch_x", "old-name", "old desc", "system", []string{"riley"}, ""); err != nil {
		t.Fatalf("seed CreateChannel: %v", err)
	}

	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	newName := "new-name"
	patch := UpdateChannelRequest{Name: &newName}
	bodyBytes, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/channels/old-name", strings.NewReader(string(bodyBytes)))
	req.SetPathValue("name", "old-name")
	w := httptest.NewRecorder()
	s.handleUpdateChannel(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	got := drainBroker(sub)
	ev := findEvent(got, "channel.updated")
	if ev == nil {
		t.Fatalf("missing channel.updated event; got = %+v", got)
	}
	data, _ := ev.Data.(map[string]any)
	if name, _ := data["name"].(string); name != "new-name" {
		t.Errorf("event name = %q, want new-name", name)
	}
}

func TestHandleUpdateChannelTeam_EmitsBrokerEvent(t *testing.T) {
	s := channelsTestServer(t)
	if err := s.store.CreateChannel("ch_x", "design-review", "", "system", []string{"riley"}, ""); err != nil {
		t.Fatalf("seed CreateChannel: %v", err)
	}

	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	body := `{"team":["riley","sofia"]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/design-review/team", strings.NewReader(body))
	req.SetPathValue("name", "design-review")
	w := httptest.NewRecorder()
	s.handleUpdateChannelTeam(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	got := drainBroker(sub)
	ev := findEvent(got, "channel.team_updated")
	if ev == nil {
		t.Fatalf("missing channel.team_updated event; got = %+v", got)
	}
	data, _ := ev.Data.(map[string]any)
	if name, _ := data["name"].(string); name != "design-review" {
		t.Errorf("event name = %q, want design-review", name)
	}
	team, _ := data["team"].([]string)
	if len(team) != 2 {
		t.Errorf("event team len = %d, want 2 (got %v)", len(team), team)
	}
}

// TestCreateChannelTool_EmitsBrokerEvent covers the DSL tool path —
// agents (e.g. Tony) create channels via the `create_channel` tool, not
// via HTTP. Without a lifecycle callback wired through
// dsl.RegisterChannelTools, those creates would be invisible to SSE
// clients even after the HTTP path is fixed.
func TestCreateChannelTool_EmitsBrokerEvent(t *testing.T) {
	s := channelsTestServer(t)
	// Pre-register a teammate so the team list resolves.
	s.interp.Document().Agents["riley"] = &dsl.Agent{Name: "riley", Model: "claude-sonnet-4-6"}

	// Wire channel tools with the same lifecycle callback the real server
	// uses. Implementation must invoke this callback on successful create.
	postCb, reactiveCb, lifecycleCb := s.buildChannelCallbacks()
	dsl.RegisterChannelTools(s.interp, s.store, postCb, reactiveCb, lifecycleCb)

	sub := s.broker.Subscribe()
	defer s.broker.Unsubscribe(sub)

	_, err := s.interp.Tools().Execute(context.Background(), "create_channel", map[string]any{
		"name": "design-review",
		"team": []any{"riley"},
	})
	if err != nil {
		t.Fatalf("create_channel tool: %v", err)
	}

	got := drainBroker(sub)
	ev := findEvent(got, "channel.created")
	if ev == nil {
		t.Fatalf("missing channel.created event; got = %+v", got)
	}
	data, _ := ev.Data.(map[string]any)
	if name, _ := data["name"].(string); name != "design-review" {
		t.Errorf("event name = %q, want design-review", name)
	}
}
