package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// TestToggleAgentTool_AddsAndRemoves is the govega#44 contract: PATCH on
// a single tool flips the enable state without a read-modify-write of
// the full tool array, eliminating the toggle race.
func TestToggleAgentTool_AddsAndRemoves(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6", Tools: []string{"read_file", "write_file"}},
	})

	// Disable read_file.
	body := mustJSON(t, ToggleAgentToolRequest{Enabled: false})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/riley/tools/read_file", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	req.SetPathValue("tool", "read_file")
	w := httptest.NewRecorder()
	s.handleToggleAgentTool(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp AgentToolsResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if slices.Contains(resp.Tools, "read_file") {
		t.Errorf("read_file still present after disable: %v", resp.Tools)
	}
	if !slices.Contains(resp.Tools, "write_file") {
		t.Errorf("write_file got dropped: %v", resp.Tools)
	}

	// Re-enable read_file.
	body = mustJSON(t, ToggleAgentToolRequest{Enabled: true})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/agents/riley/tools/read_file", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	req.SetPathValue("tool", "read_file")
	w = httptest.NewRecorder()
	s.handleToggleAgentTool(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enable status = %d", w.Code)
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !slices.Contains(resp.Tools, "read_file") {
		t.Errorf("read_file missing after re-enable: %v", resp.Tools)
	}
}

// TestToggleAgentTool_IdempotentReEnable confirms a redundant toggle
// is a 200 no-op, not a 4xx.
func TestToggleAgentTool_IdempotentReEnable(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"riley": {Name: "riley", Model: "claude-sonnet-4-6", Tools: []string{"read_file"}},
	})
	body := mustJSON(t, ToggleAgentToolRequest{Enabled: true})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/riley/tools/read_file", bytes.NewReader(body))
	req.SetPathValue("name", "riley")
	req.SetPathValue("tool", "read_file")
	w := httptest.NewRecorder()
	s.handleToggleAgentTool(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for no-op toggle", w.Code)
	}
}

// TestToggleAgentTool_ForbiddenOnBuilder confirms the builder meta-agent
// can't have its tools mutated via the API.
func TestToggleAgentTool_ForbiddenOnBuilder(t *testing.T) {
	s := agentTestServer(t, map[string]*dsl.Agent{
		"hera": {Name: "hera", Model: "claude-sonnet-4-6"},
	})
	body := mustJSON(t, ToggleAgentToolRequest{Enabled: false})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/hera/tools/create_agent", bytes.NewReader(body))
	req.SetPathValue("name", "hera")
	req.SetPathValue("tool", "create_agent")
	w := httptest.NewRecorder()
	s.handleToggleAgentTool(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for builder", w.Code)
	}
}

// TestToggleAgentTool_UnknownAgent returns 404.
func TestToggleAgentTool_UnknownAgent(t *testing.T) {
	s := agentTestServer(t, nil)
	body := mustJSON(t, ToggleAgentToolRequest{Enabled: true})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/ghost/tools/read_file", bytes.NewReader(body))
	req.SetPathValue("name", "ghost")
	req.SetPathValue("tool", "read_file")
	w := httptest.NewRecorder()
	s.handleToggleAgentTool(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unknown agent", w.Code)
	}
}

// TestMCPRegistry_IncludesIconAndCategory pins the metadata addition for
// govega#44 — well-known servers must surface icon + category so the FE
// can render the integrations grid without hand-rolling per-server
// metadata client-side.
func TestMCPRegistry_IncludesIconAndCategory(t *testing.T) {
	s := agentTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/registry", nil)
	w := httptest.NewRecorder()
	s.handleMCPRegistry(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp []MCPRegistryEntryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("registry empty")
	}
	want := map[string]struct {
		icon, category string
	}{
		"github": {"Github", "dev_tools"},
		"slack":  {"MessageSquare", "communication"},
		"gmail":  {"Mail", "communication"},
	}
	for _, entry := range resp {
		w, ok := want[entry.Name]
		if !ok {
			continue
		}
		if entry.Icon != w.icon {
			t.Errorf("%s icon = %q, want %q", entry.Name, entry.Icon, w.icon)
		}
		if entry.Category != w.category {
			t.Errorf("%s category = %q, want %q", entry.Name, entry.Category, w.category)
		}
	}
}
