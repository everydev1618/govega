package serve

import (
	"strings"
	"testing"
)

func TestFormatWikiMemoryForInjection_EmptyReturnsBlank(t *testing.T) {
	store := newTestStore(t)
	got := formatWikiMemoryForInjection(store, "et", "tony")
	if got != "" {
		t.Errorf("empty wiki should return \"\", got %q", got)
	}
}

func TestFormatWikiMemoryForInjection_UserScopeOnly(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "MEMORY.md", Content: "lives in Toronto",
	})
	got := formatWikiMemoryForInjection(store, "et", "tony")
	if !strings.Contains(got, "lives in Toronto") {
		t.Errorf("missing content: %q", got)
	}
	if !strings.Contains(got, "shared across") {
		t.Errorf("missing shared-scope header: %q", got)
	}
	if strings.Contains(got, "private working notes") {
		t.Errorf("agent header leaked in: %q", got)
	}
}

func TestFormatWikiMemoryForInjection_BothScopesConcatenated(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "MEMORY.md", Content: "shared fact",
	})
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
		Path: "MEMORY.md", Content: "private note",
	})
	got := formatWikiMemoryForInjection(store, "et", "tony")
	sharedIdx := strings.Index(got, "shared fact")
	privateIdx := strings.Index(got, "private note")
	if sharedIdx < 0 || privateIdx < 0 {
		t.Fatalf("both contents not present: %q", got)
	}
	if sharedIdx >= privateIdx {
		t.Errorf("shared scope should come first; got order: shared@%d private@%d in %q", sharedIdx, privateIdx, got)
	}
}

func TestFormatWikiMemoryForInjection_AgentScopeIsolatedPerAgent(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
		Path: "MEMORY.md", Content: "tony's private notes",
	})
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "hana", UserID: "et",
		Path: "MEMORY.md", Content: "hana's private notes",
	})
	// When tony is the calling agent, hana's private notes must not appear.
	got := formatWikiMemoryForInjection(store, "et", "tony")
	if !strings.Contains(got, "tony's private notes") {
		t.Errorf("tony's notes missing: %q", got)
	}
	if strings.Contains(got, "hana's private notes") {
		t.Errorf("hana's notes leaked into tony's injection: %q", got)
	}
}

func TestFormatWikiMemoryForInjection_TruncatesLongPages(t *testing.T) {
	store := newTestStore(t)
	lines := make([]string, wikiInjectionLineLimit+50)
	for i := range lines {
		lines[i] = "row " + strings.Repeat("x", 5)
	}
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "MEMORY.md", Content: strings.Join(lines, "\n"),
	})
	got := formatWikiMemoryForInjection(store, "et", "tony")
	if !strings.Contains(got, "50 more lines truncated") {
		t.Errorf("missing truncation marker: ...%s", got[max(0, len(got)-200):])
	}
}

func TestFormatWikiMemoryForInjection_WhitespaceOnlyTreatedAsEmpty(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "MEMORY.md", Content: "   \n  \n",
	})
	got := formatWikiMemoryForInjection(store, "et", "tony")
	if got != "" {
		t.Errorf("whitespace-only page should not inject: %q", got)
	}
}
