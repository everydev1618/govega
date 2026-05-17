package serve

import (
	"context"
	"strings"
	"testing"
	"time"

	vega "github.com/everydev1618/govega"
)

// TestInjection_IncludesActiveNoteBodies verifies that pages whose
// frontmatter carries `active: true` get their *bodies* appended to
// the injected prompt context — not just the index entry. This is
// what lets a freshly compacted session note keep its substance
// available on the next turn without an explicit memory_read call.
func TestInjection_IncludesActiveNoteBodies(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "MEMORY.md", Content: "user index",
	})
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
		Path: "MEMORY.md", Content: "agent index",
	})
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
		Path:        "sessions/2026-05-17-162305.md",
		Content:     "## Facts about the user\n- Name: Etienne\n- Lives in: Toronto",
		Frontmatter: "active: true\nat: 2026-05-17T16:23:05Z",
	})

	got := formatWikiMemoryForInjection(store, "et", "tony")

	if !strings.Contains(got, "Name: Etienne") {
		t.Errorf("active note body missing from injection; got %q", got)
	}
	if !strings.Contains(got, "sessions/2026-05-17-162305.md") {
		t.Errorf("active note path label missing; got %q", got)
	}
}

// TestInjection_InactiveNotesNotIncluded confirms only the index of
// an inactive note ships in the prompt — its body must NOT be pulled
// in unless the agent calls memory_read explicitly. Without this
// check, every page in the wiki would balloon the prompt.
func TestInjection_InactiveNotesNotIncluded(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
		Path:        "sessions/older.md",
		Content:     "VERY OLD DETAILS that should not appear",
		Frontmatter: "active: false",
	})

	got := formatWikiMemoryForInjection(store, "et", "tony")
	if strings.Contains(got, "VERY OLD DETAILS") {
		t.Errorf("inactive note body leaked into injection; got %q", got)
	}
}

// TestInjection_ActiveBodyCap caps how many active bodies inject so
// a runaway tag spree can't blow out the prompt budget. The newest
// active notes win.
func TestInjection_ActiveBodyCap(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < activeBodyInjectionCap+3; i++ {
		ts := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		mustUpsertPage(t, store, MemoryPage{
			Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
			Path:        "sessions/" + ts.Format("2006-01-02-150405") + ".md",
			Content:     "marker-" + ts.Format("05"),
			Frontmatter: "active: true",
		})
	}

	got := formatWikiMemoryForInjection(store, "et", "tony")

	// Count distinct marker- occurrences — should equal the cap.
	count := strings.Count(got, "marker-")
	if count != activeBodyInjectionCap {
		t.Errorf("active body count = %d, want cap %d (got %q)", count, activeBodyInjectionCap, got)
	}
}

// TestCompactionSink_FlagsNewNoteActiveAndDemotesPrior is the core
// of the "never forgets" loop: the most recent compacted session
// note carries `active: true` so its body auto-injects on the next
// turn, and the prior session note gets demoted so we don't stack
// active bodies forever.
func TestCompactionSink_FlagsNewNoteActiveAndDemotesPrior(t *testing.T) {
	store := newTestStore(t)
	s := &Server{store: store}

	const userID = "et"
	const agent = "tony"

	// Seed a previous "active" session note so we can verify it gets demoted.
	prior := "sessions/2026-05-10-120000.md"
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: agent, UserID: userID,
		Path: prior, Content: "old session", Frontmatter: "active: true\nsource: prior",
	})

	sink := s.wikiCompactionSink(userID, agent)
	meta := vega.CompactionMeta{
		AgentName: agent,
		DroppedAt: time.Date(2026, 5, 17, 16, 23, 5, 0, time.UTC),
	}
	if err := sink(context.Background(), "fresh summary", meta); err != nil {
		t.Fatalf("sink: %v", err)
	}

	newest := "sessions/2026-05-17-162305.md"
	newPage, _ := store.GetMemoryPage(MemoryScopeAgent, agent, userID, newest)
	if newPage == nil {
		t.Fatalf("new session note not written")
	}
	if !strings.Contains(newPage.Frontmatter, "active: true") {
		t.Errorf("new session note must carry active:true; got frontmatter %q", newPage.Frontmatter)
	}

	priorPage, _ := store.GetMemoryPage(MemoryScopeAgent, agent, userID, prior)
	if priorPage == nil {
		t.Fatalf("prior session note vanished")
	}
	if strings.Contains(priorPage.Frontmatter, "active: true") {
		t.Errorf("prior session note should have been demoted; got frontmatter %q", priorPage.Frontmatter)
	}
	// Demoted note must still preserve its original frontmatter except for
	// the active flag — losing provenance ("source: prior") would be a bug.
	if !strings.Contains(priorPage.Frontmatter, "source: prior") {
		t.Errorf("demote dropped unrelated frontmatter keys; got %q", priorPage.Frontmatter)
	}
}
