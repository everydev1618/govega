package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// memoryHTTPHarness wires the minimum a *Server needs for memory HTTP
// handler tests: store, interpreter, broker. The HTTP path uses
// X-Auth-User to scope reads — tests set the header on each request.
func memoryHTTPHarness(t *testing.T) *Server {
	t.Helper()
	doc := &dsl.Document{Agents: map[string]*dsl.Agent{}, Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"}}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("NewInterpreter: %v", err)
	}
	return &Server{
		store:   newTestStore(t),
		interp:  interp,
		broker:  NewEventBroker(),
		streams: map[string]*activeStream{},
	}
}

func TestHandleMemoryGraph_EmptyWiki(t *testing.T) {
	s := memoryHTTPHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got memoryGraphResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Errorf("empty wiki should return empty arrays; got %d nodes / %d edges", len(got.Nodes), len(got.Edges))
	}
}

func TestHandleMemoryGraph_NodesAndEdges(t *testing.T) {
	s := memoryHTTPHarness(t)
	pages := []MemoryPage{
		{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "see [[topics/sushi.md]] and [[profile.md]]"},
		{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "topics/sushi.md", Content: "omakase preferred"},
		{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "profile.md", Content: "founder"},
		// Another scope must NOT bleed in.
		{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "private.md", Content: "tony's notes"},
	}
	for _, p := range pages {
		mustUpsertPage(t, s.store, p)
	}
	mustReplaceLinks(t, s.store, "MEMORY.md", "topics/sushi.md", "profile.md")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got memoryGraphResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if len(got.Nodes) != 3 {
		t.Fatalf("nodes len = %d, want 3 (private.md must NOT leak in)", len(got.Nodes))
	}
	if len(got.Edges) != 2 {
		t.Errorf("edges len = %d, want 2 (MEMORY → sushi, MEMORY → profile)", len(got.Edges))
	}
	// Spot-check that bytes are populated.
	for _, n := range got.Nodes {
		if n.Path == "topics/sushi.md" && n.Bytes != len("omakase preferred") {
			t.Errorf("topics/sushi.md bytes = %d, want %d", n.Bytes, len("omakase preferred"))
		}
	}
}

func TestHandleMemoryGraph_DerivesClusterFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"MEMORY.md", "index"},
		{"profile.md", "profile"},
		{"decisions.md", "decisions"},
		{"topics/sushi.md", "topics"},
		{"people/sarah.md", "people"},
		{"legacy-items.md", "legacy"},
		{"legacy-journal.md", "legacy"},
		{"random.md", "other"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := clusterForPath(tc.path); got != tc.want {
				t.Errorf("clusterForPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestHandleMemoryPages_ListsMetadataNoContent(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "profile.md", Content: "founder",
	})
	mustUpsertPage(t, s.store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "topics/sushi.md", Content: "omakase",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/pages", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleListMemoryPages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got []memoryPageMetadata
	_ = json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, p := range got {
		// List endpoint must not return content (keeps payload small).
		if p.Bytes <= 0 {
			t.Errorf("bytes should be populated: %+v", p)
		}
	}
}

func TestHandleMemoryPage_SingleReturnsFullContent(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "topics/sushi.md", Content: "omakase preferred",
		Frontmatter: "tag: food",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/page?path=topics/sushi.md", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleGetMemoryPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got MemoryPage
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Content != "omakase preferred" {
		t.Errorf("content = %q", got.Content)
	}
	if got.Frontmatter != "tag: food" {
		t.Errorf("frontmatter = %q", got.Frontmatter)
	}
}

func TestHandleMemoryPage_404OnMissing(t *testing.T) {
	s := memoryHTTPHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/page?path=does-not-exist.md", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleGetMemoryPage(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleMemoryPage_400OnMissingPathParam(t *testing.T) {
	s := memoryHTTPHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/page", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleGetMemoryPage(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func mustReplaceLinks(t *testing.T, store Store, fromPath string, toPaths ...string) {
	t.Helper()
	if err := store.ReplaceMemoryLinks(MemoryScopeUser, "et", "et", fromPath, toPaths); err != nil {
		t.Fatalf("ReplaceMemoryLinks: %v", err)
	}
}

func mustReplaceScopedLinks(t *testing.T, store Store, scope MemoryScope, scopeID, fromPath string, toPaths ...string) {
	t.Helper()
	if err := store.ReplaceMemoryLinks(scope, scopeID, "et", fromPath, toPaths); err != nil {
		t.Fatalf("ReplaceMemoryLinks(%s/%s): %v", scope, scopeID, err)
	}
}

// TestHandleMemoryGraph_DefaultScopeUserBackCompat asserts that the
// default (no query params) request still returns just the user wiki —
// existing frontend callers must keep working.
func TestHandleMemoryGraph_DefaultScopeUserBackCompat(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "user idx"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "private.md", Content: "agent idx"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got memoryGraphResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if len(got.Nodes) != 1 || got.Nodes[0].Path != "MEMORY.md" {
		t.Errorf("default scope must be user only; got %+v", got.Nodes)
	}
	// Single-scope IDs should equal Path so existing callers don't break.
	if got.Nodes[0].ID != "MEMORY.md" {
		t.Errorf("single-scope ID = %q, want %q", got.Nodes[0].ID, "MEMORY.md")
	}
	if got.Nodes[0].Scope != "user" {
		t.Errorf("scope field = %q, want \"user\"", got.Nodes[0].Scope)
	}
}

// TestHandleMemoryGraph_ScopeAgentRequiresAgentParam asserts that
// scope=agent without ?agent= is a 400.
func TestHandleMemoryGraph_ScopeAgentRequiresAgentParam(t *testing.T) {
	s := memoryHTTPHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph?scope=agent", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestHandleMemoryGraph_ScopeAgentReturnsOnlyThatAgent asserts that
// ?scope=agent&agent=tony returns only tony's pages.
func TestHandleMemoryGraph_ScopeAgentReturnsOnlyThatAgent(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "user idx"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "notes.md", Content: "tony notes"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "mira", UserID: "et", Path: "notes.md", Content: "mira notes"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph?scope=agent&agent=tony", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got memoryGraphResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if len(got.Nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1 (only tony's notes.md)", len(got.Nodes))
	}
	if got.Nodes[0].Scope != "agent:tony" {
		t.Errorf("scope = %q, want \"agent:tony\"", got.Nodes[0].Scope)
	}
	if got.Nodes[0].Path != "notes.md" {
		t.Errorf("path = %q, want notes.md", got.Nodes[0].Path)
	}
}

// TestHandleMemoryGraph_ScopeAllUnionsEverything asserts that
// scope=all merges user + every agent wiki, with unique IDs.
func TestHandleMemoryGraph_ScopeAllUnionsEverything(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "shared"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "MEMORY.md", Content: "tony idx"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "mira", UserID: "et", Path: "MEMORY.md", Content: "mira idx"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph?scope=all", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got memoryGraphResponse
	_ = json.NewDecoder(w.Body).Decode(&got)
	if len(got.Nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(got.Nodes))
	}
	// Three nodes with the same path "MEMORY.md" must end up with distinct IDs.
	idSet := map[string]bool{}
	scopes := map[string]bool{}
	for _, n := range got.Nodes {
		if idSet[n.ID] {
			t.Errorf("duplicate node ID %q", n.ID)
		}
		idSet[n.ID] = true
		scopes[n.Scope] = true
		if n.Path != "MEMORY.md" {
			t.Errorf("node path = %q, want MEMORY.md", n.Path)
		}
	}
	for _, want := range []string{"user", "agent:tony", "agent:mira"} {
		if !scopes[want] {
			t.Errorf("missing scope %q in response", want)
		}
	}
}

// TestHandleMemoryGraph_GhostNodesForDanglingEdges asserts that an
// edge pointing to a non-existent page yields a ghost node so the
// frontend doesn't have to synthesize one.
func TestHandleMemoryGraph_GhostNodesForDanglingEdges(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "see [[topics/missing.md]]"})
	mustReplaceLinks(t, s.store, "MEMORY.md", "topics/missing.md")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)
	var got memoryGraphResponse
	_ = json.NewDecoder(w.Body).Decode(&got)

	var ghost *memoryGraphNode
	for i := range got.Nodes {
		if got.Nodes[i].Path == "topics/missing.md" {
			ghost = &got.Nodes[i]
			break
		}
	}
	if ghost == nil {
		t.Fatalf("expected ghost node for topics/missing.md; got nodes = %+v", got.Nodes)
	}
	if !ghost.Ghost {
		t.Errorf("ghost.Ghost = false, want true")
	}
	if ghost.Cluster != "ghost" {
		t.Errorf("ghost.Cluster = %q, want \"ghost\"", ghost.Cluster)
	}
	if ghost.Bytes != 0 {
		t.Errorf("ghost.Bytes = %d, want 0", ghost.Bytes)
	}
	if len(got.Edges) != 1 {
		t.Errorf("edges len = %d, want 1", len(got.Edges))
	}
	if got.Edges[0].To != ghost.ID {
		t.Errorf("edge.To = %q, want ghost.ID %q", got.Edges[0].To, ghost.ID)
	}
}

// TestHandleMemoryGraph_ScopeAllEdgesUseScopedIDs asserts that under
// scope=all, edges reference the prefixed node IDs, not raw paths.
func TestHandleMemoryGraph_ScopeAllEdgesUseScopedIDs(t *testing.T) {
	s := memoryHTTPHarness(t)
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "MEMORY.md", Content: "see [[a.md]]"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeUser, ScopeID: "et", UserID: "et", Path: "a.md", Content: "a"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "MEMORY.md", Content: "see [[a.md]]"})
	mustUpsertPage(t, s.store, MemoryPage{Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et", Path: "a.md", Content: "a"})
	mustReplaceLinks(t, s.store, "MEMORY.md", "a.md")
	mustReplaceScopedLinks(t, s.store, MemoryScopeAgent, "tony", "MEMORY.md", "a.md")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/graph?scope=all", nil)
	req.Header.Set("X-Auth-User", "et")
	w := httptest.NewRecorder()
	s.handleMemoryGraph(w, req)
	var got memoryGraphResponse
	_ = json.NewDecoder(w.Body).Decode(&got)

	// Build set of node IDs we expect to see referenced by edges.
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	if len(got.Edges) != 2 {
		t.Fatalf("edges len = %d, want 2", len(got.Edges))
	}
	for _, e := range got.Edges {
		if !ids[e.From] {
			t.Errorf("edge.from %q is not a known node ID", e.From)
		}
		if !ids[e.To] {
			t.Errorf("edge.to %q is not a known node ID", e.To)
		}
	}
}
