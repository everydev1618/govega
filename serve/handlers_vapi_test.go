package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// vapiTestServer wires the minimum the Vapi integration handlers need:
// real SQLite store, a real Interpreter with an empty Document, an empty
// streams map. ConnectBuiltinServer("vapi") just registers tools — it
// doesn't actually call api.vapi.ai, so no httptest backend is needed here.
func vapiTestServer(t *testing.T) *Server {
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
		streams: map[string]*activeStream{},
	}
}

func TestVapiStatusInitiallyDisconnected(t *testing.T) {
	s := vapiTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/vapi", nil)
	w := httptest.NewRecorder()
	s.handleVapiStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got VapiStatus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Connected || got.HasAPIKey || got.HasDefaultPhoneNumber {
		t.Errorf("expected fully-disconnected snapshot, got %+v", got)
	}
}

func TestVapiConfigureMissingAPIKey400(t *testing.T) {
	s := vapiTestServer(t)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/vapi", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleVapiConfigure(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "api_key") {
		t.Errorf("expected api_key mention in error body, got %s", w.Body.String())
	}
}

func TestVapiConfigureInvalidJSON400(t *testing.T) {
	s := vapiTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/vapi", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	s.handleVapiConfigure(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestVapiConfigurePersistsAndConnects(t *testing.T) {
	s := vapiTestServer(t)

	body, _ := json.Marshal(map[string]string{
		"api_key":                 "sk-test-vapi",
		"default_phone_number_id": "pn_default_xyz",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/vapi", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleVapiConfigure(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got VapiStatus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Connected {
		t.Errorf("expected Connected=true, got %+v", got)
	}
	if !got.HasAPIKey {
		t.Errorf("expected HasAPIKey=true")
	}
	if !got.HasDefaultPhoneNumber {
		t.Errorf("expected HasDefaultPhoneNumber=true")
	}

	// The MCP tools should now be registered with the vapi__ prefix.
	tools := s.interp.Tools()
	if !tools.BuiltinServerConnected("vapi") {
		t.Error("expected builtin vapi server to be connected after configure")
	}

	// Settings should be namespaced and the api_key should be marked sensitive.
	settings, err := s.store.ListSettings()
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	foundAPIKey := false
	for _, st := range settings {
		if st.Key == "mcp:vapi:VAPI_API_KEY" {
			foundAPIKey = true
			if st.Value != "sk-test-vapi" {
				t.Errorf("VAPI_API_KEY value=%q, want sk-test-vapi", st.Value)
			}
			if !st.Sensitive {
				t.Error("VAPI_API_KEY setting should be marked sensitive")
			}
		}
	}
	if !foundAPIKey {
		t.Error("expected mcp:vapi:VAPI_API_KEY setting to be persisted")
	}

	// Disable should disconnect and drop the keys.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/vapi", nil)
	delW := httptest.NewRecorder()
	s.handleVapiDisable(delW, delReq)

	if delW.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", delW.Code, delW.Body.String())
	}
	var afterDel VapiStatus
	_ = json.Unmarshal(delW.Body.Bytes(), &afterDel)
	if afterDel.Connected || afterDel.HasAPIKey || afterDel.HasDefaultPhoneNumber {
		t.Errorf("expected fully-disconnected snapshot after disable, got %+v", afterDel)
	}
	if tools.BuiltinServerConnected("vapi") {
		t.Error("expected builtin vapi server to be disconnected after disable")
	}
}
