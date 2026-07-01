package serve

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// Editing an agent's triggers via the REST API must persist them so the agent
// reacts after a restart — the Triggers editor's backend contract.
func TestUpdateAgentPersistsTriggers(t *testing.T) {
	store := newTestStore(t)
	// Seed a composed agent with no triggers.
	if err := store.InsertComposedAgent(ComposedAgent{Name: "watcher", Model: "m", System: "s"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := newTriggersTestServer(t, store)

	body := `{"triggers":[{"on":"agent.completed","where":"status == failed","prompt":"handle {{.Data.agent}}"}]}`
	req := httptest.NewRequest("PUT", "/api/v1/agents/watcher", strings.NewReader(body))
	req.SetPathValue("name", "watcher")
	w := httptest.NewRecorder()
	s.handleUpdateAgent(w, req)

	if w.Code != 200 {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	// Persisted?
	agents, err := store.ListComposedAgents()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got *ComposedAgent
	for i := range agents {
		if agents[i].Name == "watcher" {
			got = &agents[i]
		}
	}
	if got == nil || len(got.Triggers) != 1 {
		t.Fatalf("expected 1 persisted trigger, got %+v", got)
	}
	if got.Triggers[0].On != "agent.completed" || got.Triggers[0].Where != "status == failed" {
		t.Fatalf("trigger not persisted correctly: %+v", got.Triggers[0])
	}

	// And discoverable by the router (definitions carry them).
	if trigs := s.interp.ReactiveTriggers()["watcher"]; len(trigs) != 1 {
		t.Fatalf("router should see the new trigger, got %v", trigs)
	}

	// Response encodes cleanly.
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "updated" {
		t.Fatalf("unexpected response: %v", resp)
	}
}

func newTriggersTestServer(t *testing.T, store Store) *Server {
	t.Helper()
	doc, err := dsl.NewParser().Parse([]byte("name: t\nagents:\n  watcher:\n    model: m\n    system: s\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	interp, err := dsl.NewInterpreter(doc, dsl.WithLazySpawn())
	if err != nil {
		t.Fatalf("interp: %v", err)
	}
	return &Server{interp: interp, store: store, bus: nil}
}
