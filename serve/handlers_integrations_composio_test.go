package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

func composioTestServer(t *testing.T) *Server {
	t.Helper()
	doc := &dsl.Document{
		Agents: map[string]*dsl.Agent{
			"riley": {Name: "riley", Model: "claude-sonnet-4-6"},
		},
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
		cfg: Config{
			Builder:      dsl.HeraConfig{Name: "hera"},
			Orchestrator: dsl.IrisConfig{Name: "iris"},
		},
	}
}

// fakeComposio stands in for backend.composio.dev. It records requests and
// serves canned v3 API responses.
func fakeComposio(t *testing.T, accounts []map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/connected_accounts":
			json.NewEncoder(w).Encode(map[string]any{"items": accounts})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/auth_configs":
			json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"id": "ac_test"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/connected_accounts/link":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["auth_config_id"] == "" || body["user_id"] == "" || body["callback_url"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "missing fields"})
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"redirect_url": "https://connect.composio.test/link/lk_test",
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/connected_accounts/"):
			if strings.HasSuffix(r.URL.Path, "ca_gone") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/tools":
			json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"slug": "GITHUB_CREATE_AN_ISSUE"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/mcp/servers":
			json.NewEncoder(w).Encode(map[string]string{
				"id":      "mcp_test",
				"mcp_url": "http://" + r.Host + "/v3/mcp/mcp_test",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestListIntegrations_NoBrokerConfigured(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "")
	s := composioTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
	w := httptest.NewRecorder()
	s.handleListIntegrations(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp []IntegrationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("expected catalog entries")
	}
	for _, item := range resp {
		if item.Connections == nil || len(item.Connections) != 0 {
			t.Fatalf("expected empty connections for %s, got %v", item.ID, item.Connections)
		}
	}
}

func TestListIntegrations_MergesAccounts(t *testing.T) {
	fake, _ := fakeComposio(t, []map[string]any{
		{
			"id":      "ca_1",
			"word_id": "github_word",
			"alias":   nil,
			"user_id": "tenant-test",
			"status":  "ACTIVE",
			"toolkit": map[string]string{"slug": "github"},
			"created_at": "2026-06-12T00:00:00Z",
		},
		{
			"id":      "ca_2",
			"word_id": "slack_word",
			"alias":   "My Slack",
			"user_id": "tenant-test",
			"status":  "INITIATED",
			"toolkit": map[string]string{"slug": "slack"},
		},
	})
	t.Setenv("COMPOSIO_API_KEY", "test-key")
	t.Setenv("COMPOSIO_BASE_URL", fake.URL)
	t.Setenv("COMPOSIO_USER_ID", "tenant-test")
	s := composioTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations", nil)
	w := httptest.NewRecorder()
	s.handleListIntegrations(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp []IntegrationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	find := func(id string) IntegrationResponse {
		for _, item := range resp {
			if item.ID == id {
				return item
			}
		}
		t.Fatalf("toolkit %s missing", id)
		return IntegrationResponse{}
	}

	github := find("github")
	if len(github.Connections) != 1 {
		t.Fatalf("github connections = %v", github.Connections)
	}
	if github.Connections[0].Status != "active" {
		t.Fatalf("github status = %s", github.Connections[0].Status)
	}
	if github.Connections[0].AccountLabel == nil || *github.Connections[0].AccountLabel != "github_word" {
		t.Fatalf("github label = %v, want word_id fallback", github.Connections[0].AccountLabel)
	}

	slack := find("slack")
	if len(slack.Connections) != 1 || slack.Connections[0].Status != "pending" {
		t.Fatalf("slack connections = %v", slack.Connections)
	}
	if slack.Connections[0].AccountLabel == nil || *slack.Connections[0].AccountLabel != "My Slack" {
		t.Fatalf("slack label = %v, want alias", slack.Connections[0].AccountLabel)
	}
}

func TestConnectIntegration_ReturnsRedirect(t *testing.T) {
	fake, paths := fakeComposio(t, nil)
	t.Setenv("COMPOSIO_API_KEY", "test-key")
	t.Setenv("COMPOSIO_BASE_URL", fake.URL)
	t.Setenv("COMPOSIO_USER_ID", "tenant-test")
	s := composioTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/connections",
		strings.NewReader(`{"callback_url":"https://acme.apexhost.ai/w/acme/account/integrations"}`))
	req.SetPathValue("toolkit", "github")
	w := httptest.NewRecorder()
	s.handleConnectIntegration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp ConnectIntegrationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RedirectURL != "https://connect.composio.test/link/lk_test" {
		t.Fatalf("redirect_url = %s", resp.RedirectURL)
	}
	joined := strings.Join(*paths, " | ")
	if !strings.Contains(joined, "POST /api/v3/connected_accounts/link") {
		t.Fatalf("link endpoint not called: %s", joined)
	}

	// Auth config id must be cached for the next connect.
	setting, err := s.store.GetSetting("composio/auth_config/github")
	if err != nil || setting == nil || setting.Value != "ac_test" {
		t.Fatalf("auth config not cached: %v, %v", setting, err)
	}
}

func TestConnectIntegration_Validation(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "test-key")
	s := composioTestServer(t)

	// Unknown toolkit → 404
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/nope/connections",
		strings.NewReader(`{"callback_url":"https://x"}`))
	req.SetPathValue("toolkit", "nope")
	w := httptest.NewRecorder()
	s.handleConnectIntegration(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown toolkit status = %d", w.Code)
	}

	// Missing callback_url → 400
	req = httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/connections",
		strings.NewReader(`{}`))
	req.SetPathValue("toolkit", "github")
	w = httptest.NewRecorder()
	s.handleConnectIntegration(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing callback status = %d", w.Code)
	}
}

func TestDisconnectIntegration_Idempotent(t *testing.T) {
	fake, _ := fakeComposio(t, nil)
	t.Setenv("COMPOSIO_API_KEY", "test-key")
	t.Setenv("COMPOSIO_BASE_URL", fake.URL)
	s := composioTestServer(t)

	// Normal delete
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/github/connections/ca_1", nil)
	req.SetPathValue("toolkit", "github")
	req.SetPathValue("connection_id", "ca_1")
	w := httptest.NewRecorder()
	s.handleDisconnectIntegration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Provider already deleted it → still ok
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/github/connections/ca_gone", nil)
	req.SetPathValue("toolkit", "github")
	req.SetPathValue("connection_id", "ca_gone")
	w = httptest.NewRecorder()
	s.handleDisconnectIntegration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("idempotent status = %d, body = %s", w.Code, w.Body.String())
	}
}

// TestTeardownConnectionMCP_ResetsAgents — agents snapshot their toolset at
// spawn (govega#57 bug class), so wiring an MCP server up or down must reset
// live agent processes or they keep a stale schema until restart. Teardown is
// the testable half (sync needs a real MCP handshake); both share the same
// reset call.
func TestTeardownConnectionMCP_ResetsAgents(t *testing.T) {
	t.Setenv("COMPOSIO_API_KEY", "test-key")
	s := composioTestServer(t)

	before, err := s.interp.EnsureAgent("riley")
	if err != nil {
		t.Fatalf("EnsureAgent: %v", err)
	}

	// Persisted mapping from the connection to its MCP server name — the
	// state syncConnectionMCP leaves behind.
	if err := s.store.UpsertSetting(Setting{Key: composioConnectionKey("ca_1"), Value: "composio_github"}); err != nil {
		t.Fatalf("UpsertSetting: %v", err)
	}

	s.teardownConnectionMCP("ca_1")

	after, err := s.interp.EnsureAgent("riley")
	if err != nil {
		t.Fatalf("EnsureAgent after teardown: %v", err)
	}
	if after == before {
		t.Fatal("expected agent processes to be reset after MCP teardown")
	}

	// No mapping → no-op → no reset.
	unchanged, _ := s.interp.EnsureAgent("riley")
	s.teardownConnectionMCP("ca_unknown")
	still, _ := s.interp.EnsureAgent("riley")
	if unchanged != still {
		t.Fatal("teardown of an unknown connection must not reset agents")
	}
}
