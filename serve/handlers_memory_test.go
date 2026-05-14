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
