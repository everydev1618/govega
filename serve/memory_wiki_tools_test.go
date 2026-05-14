package serve

import (
	"context"
	"strings"
	"testing"

	"github.com/everydev1618/govega/dsl"
)

// Tests for the wiki memory tools (govega#71). These exercise the
// tools end-to-end against a real SQLiteStore + interpreter, hitting
// the same paths agents will take at runtime.

// wikiToolsHarness assembles the minimum scaffolding needed to call
// the registered tools: an interpreter to register them on, a real
// store, and a context that carries (store, userID, agent) the way
// ContextWithMemory does in production.
func wikiToolsHarness(t *testing.T) (*dsl.Interpreter, Store, context.Context) {
	t.Helper()
	doc := &dsl.Document{
		Agents:   map[string]*dsl.Agent{},
		Settings: &dsl.Settings{DefaultModel: "claude-sonnet-4-6"},
	}
	interp, err := dsl.NewInterpreter(doc)
	if err != nil {
		t.Fatalf("dsl.NewInterpreter: %v", err)
	}
	store := newTestStore(t)
	RegisterWikiMemoryTools(interp)
	ctx := ContextWithMemory(context.Background(), store, "et", "tony")
	return interp, store, ctx
}

func runTool(t *testing.T, interp *dsl.Interpreter, ctx context.Context, name string, params map[string]any) string {
	t.Helper()
	out, err := interp.Tools().Execute(ctx, name, params)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestExtractMemoryLinks(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"empty", "", nil},
		{"wikilink only", "see [[topics/sushi.md]] for more", []string{"topics/sushi.md"}},
		{"markdown link", "see [sushi](topics/sushi.md) for more", []string{"topics/sushi.md"}},
		{"both, dedup", "[[a.md]] and [a](a.md)", []string{"a.md"}},
		{"external url skipped", "[anthropic](https://anthropic.com)", nil},
		{"mailto skipped", "[mail](mailto:foo@bar.com)", nil},
		{"anchor skipped", "[ref](#section)", nil},
		{"order preserved", "[[a.md]] then [[b.md]] then [[a.md]]", []string{"a.md", "b.md"}},
		{"trailing punctuation stripped", "see [s](topics/sushi.md).", []string{"topics/sushi.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMemoryLinks(tc.content)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q (full = %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestResolveScope(t *testing.T) {
	cases := []struct {
		raw         string
		wantScope   MemoryScope
		wantScopeID string
		wantErr     bool
	}{
		{"", MemoryScopeUser, "et", false},
		{"user", MemoryScopeUser, "et", false},
		{"shared", MemoryScopeUser, "et", false},
		{"agent", MemoryScopeAgent, "tony", false},
		{"private", MemoryScopeAgent, "tony", false},
		{"User", MemoryScopeUser, "et", false}, // case-insensitive
		{"nonsense", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			scope, scopeID, err := resolveScope(tc.raw, "et", "tony")
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scope != tc.wantScope || scopeID != tc.wantScopeID {
				t.Errorf("got scope=%s scopeID=%s, want %s/%s", scope, scopeID, tc.wantScope, tc.wantScopeID)
			}
		})
	}
}

func TestMemoryWriteCreatesPageAndExtractsLinks(t *testing.T) {
	interp, store, ctx := wikiToolsHarness(t)

	out := runTool(t, interp, ctx, "memory_write", map[string]any{
		"page":    "MEMORY.md",
		"content": "Index page. See [[topics/sushi.md]] and [apex](topics/apex.md).",
	})
	if !strings.Contains(out, "MEMORY.md") {
		t.Errorf("tool output missing path: %q", out)
	}

	got, err := store.GetMemoryPage(MemoryScopeUser, "et", "et", "MEMORY.md")
	if err != nil || got == nil {
		t.Fatalf("page not stored: %v / %+v", err, got)
	}
	links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
	wantSet := map[string]bool{"topics/sushi.md": false, "topics/apex.md": false}
	for _, l := range links {
		if l.FromPath != "MEMORY.md" {
			t.Errorf("unexpected from_path: %+v", l)
		}
		if _, ok := wantSet[l.ToPath]; ok {
			wantSet[l.ToPath] = true
		}
	}
	for to, ok := range wantSet {
		if !ok {
			t.Errorf("missing extracted link → %s; got = %+v", to, links)
		}
	}
}

func TestMemoryWriteScopeAgent(t *testing.T) {
	interp, store, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{
		"page":    "current-work.md",
		"content": "Hana is on sushi.",
		"scope":   "agent",
	})
	// Should NOT appear in shared scope.
	if p, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "current-work.md"); p != nil {
		t.Errorf("agent-scope page leaked into user scope: %+v", p)
	}
	// Should appear in agent scope under scope_id = the calling agent.
	p, err := store.GetMemoryPage(MemoryScopeAgent, "tony", "et", "current-work.md")
	if err != nil || p == nil {
		t.Fatalf("agent-scope page not stored: %v / %+v", err, p)
	}
}

func TestMemoryReadReturnsPageContentWithFrontmatter(t *testing.T) {
	interp, _, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{
		"page":        "profile.md",
		"content":     "lives in Toronto",
		"frontmatter": "type: profile",
	})
	out := runTool(t, interp, ctx, "memory_read", map[string]any{"page": "profile.md"})
	if !strings.Contains(out, "lives in Toronto") {
		t.Errorf("missing body in read output: %q", out)
	}
	if !strings.Contains(out, "type: profile") {
		t.Errorf("missing frontmatter in read output: %q", out)
	}
}

func TestMemoryReadMissingPageIsSoft(t *testing.T) {
	interp, _, ctx := wikiToolsHarness(t)
	out := runTool(t, interp, ctx, "memory_read", map[string]any{"page": "nope.md"})
	if !strings.Contains(strings.ToLower(out), "no page") {
		t.Errorf("missing page should be soft, got: %q", out)
	}
}

func TestMemoryAppendCreatesThenExtends(t *testing.T) {
	interp, store, ctx := wikiToolsHarness(t)

	runTool(t, interp, ctx, "memory_append", map[string]any{"page": "log.md", "content": "day 1"})
	runTool(t, interp, ctx, "memory_append", map[string]any{"page": "log.md", "content": "day 2 [[entry-2.md]]"})

	got, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "log.md")
	if got == nil || !strings.Contains(got.Content, "day 1") || !strings.Contains(got.Content, "day 2") {
		t.Fatalf("append did not concat correctly: %+v", got)
	}
	// Link from the second append should be extracted.
	links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
	found := false
	for _, l := range links {
		if l.FromPath == "log.md" && l.ToPath == "entry-2.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("append did not re-extract links: %+v", links)
	}
}

func TestMemoryEditReplacesAndUpdatesLinks(t *testing.T) {
	interp, store, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{
		"page": "profile.md", "content": "lives in Toronto [[city.md]]",
	})
	runTool(t, interp, ctx, "memory_edit", map[string]any{
		"page": "profile.md", "old": "Toronto [[city.md]]", "new": "Montreal [[other-city.md]]",
	})
	got, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "profile.md")
	if got == nil || !strings.Contains(got.Content, "Montreal") {
		t.Errorf("edit didn't apply: %+v", got)
	}
	links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
	// city.md should be gone; other-city.md should be present.
	for _, l := range links {
		if l.ToPath == "city.md" {
			t.Errorf("stale link to city.md survived edit: %+v", links)
		}
	}
	foundNew := false
	for _, l := range links {
		if l.ToPath == "other-city.md" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("new link to other-city.md missing: %+v", links)
	}
}

func TestMemoryEditFailsWhenOldNotFound(t *testing.T) {
	interp, _, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "p.md", "content": "abc"})
	_, err := interp.Tools().Execute(ctx, "memory_edit", map[string]any{
		"page": "p.md", "old": "zzz", "new": "yyy",
	})
	if err == nil {
		t.Errorf("expected error for missing 'old', got nil")
	}
}

func TestMemoryRenameFailsOnCollision(t *testing.T) {
	interp, _, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "a.md", "content": "a"})
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "b.md", "content": "b"})
	_, err := interp.Tools().Execute(ctx, "memory_rename", map[string]any{
		"from": "a.md", "to": "b.md",
	})
	if err == nil {
		t.Errorf("expected collision error, got nil")
	}
}

func TestMemoryRenameRewritesLinks(t *testing.T) {
	interp, store, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{
		"page": "MEMORY.md", "content": "[[old.md]]",
	})
	runTool(t, interp, ctx, "memory_write", map[string]any{
		"page": "old.md", "content": "stuff",
	})
	runTool(t, interp, ctx, "memory_rename", map[string]any{
		"from": "old.md", "to": "new.md",
	})
	links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
	for _, l := range links {
		if l.FromPath == "old.md" || l.ToPath == "old.md" {
			t.Errorf("stale link survived rename: %+v", l)
		}
	}
}

func TestMemoryDeleteCascadesLinks(t *testing.T) {
	interp, store, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "MEMORY.md", "content": "[[a.md]]"})
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "a.md", "content": "a"})
	runTool(t, interp, ctx, "memory_delete", map[string]any{"page": "a.md"})

	if p, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "a.md"); p != nil {
		t.Errorf("page survived delete: %+v", p)
	}
	links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
	for _, l := range links {
		if l.FromPath == "a.md" || l.ToPath == "a.md" {
			t.Errorf("link to/from deleted page survived: %+v", l)
		}
	}
}

func TestMemoryListAndSearch(t *testing.T) {
	interp, _, ctx := wikiToolsHarness(t)
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "MEMORY.md", "content": "idx"})
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "topics/sushi.md", "content": "sushi notes"})
	runTool(t, interp, ctx, "memory_write", map[string]any{"page": "topics/apex.md", "content": "apex notes"})

	listOut := runTool(t, interp, ctx, "memory_list", map[string]any{"prefix": "topics/"})
	if !strings.Contains(listOut, "topics/sushi.md") || !strings.Contains(listOut, "topics/apex.md") {
		t.Errorf("list missing expected paths: %q", listOut)
	}
	if strings.Contains(listOut, "MEMORY.md") {
		t.Errorf("prefix filter didn't exclude MEMORY.md: %q", listOut)
	}

	searchOut := runTool(t, interp, ctx, "memory_search", map[string]any{"query": "sushi"})
	if !strings.Contains(searchOut, "topics/sushi.md") {
		t.Errorf("search missed sushi page: %q", searchOut)
	}
}
