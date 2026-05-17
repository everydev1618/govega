package serve

import (
	"context"
	"strings"
	"testing"
	"time"

	vega "github.com/everydev1618/govega"
)

// TestWikiCompactionSink_WritesNoteAndIndexLink verifies the sink
// returned by Server.wikiCompactionSink actually persists the summary
// as a dated session note in the agent's private wiki *and* surfaces
// the new note in MEMORY.md so the next chat turn's auto-injection
// will pick it up. End-to-end smoke test for govega#100.
func TestWikiCompactionSink_WritesNoteAndIndexLink(t *testing.T) {
	store := newTestStore(t)
	s := &Server{store: store}

	const userID = "et"
	const agent = "tony"
	summary := "## Facts about the user\n- Name: Etienne\n\n## Decisions\n- We picked the wiki-as-compaction-target approach."

	sink := s.wikiCompactionSink(userID, agent)
	meta := vega.CompactionMeta{
		ProcessID:    "proc-abc",
		AgentName:    agent,
		DroppedCount: 30,
		KeptCount:    12,
		DroppedAt:    time.Date(2026, 5, 17, 16, 23, 5, 0, time.UTC),
	}
	if err := sink(context.Background(), summary, meta); err != nil {
		t.Fatalf("sink: %v", err)
	}

	expectedPath := "sessions/2026-05-17-162305.md"
	page, err := store.GetMemoryPage(MemoryScopeAgent, agent, userID, expectedPath)
	if err != nil {
		t.Fatalf("GetMemoryPage(session): %v", err)
	}
	if page == nil {
		t.Fatalf("session note not written at %q", expectedPath)
	}
	if !strings.Contains(page.Content, "Etienne") {
		t.Errorf("session note content missing summary; got %q", page.Content)
	}
	if !strings.Contains(page.Frontmatter, "auto-compaction") {
		t.Errorf("session note frontmatter missing provenance tag; got %q", page.Frontmatter)
	}

	idx, err := store.GetMemoryPage(MemoryScopeAgent, agent, userID, "MEMORY.md")
	if err != nil {
		t.Fatalf("GetMemoryPage(MEMORY.md): %v", err)
	}
	if idx == nil {
		t.Fatalf("MEMORY.md was not created")
	}
	if !strings.Contains(idx.Content, "## Past sessions") {
		t.Errorf("MEMORY.md missing heading; got %q", idx.Content)
	}
	if !strings.Contains(idx.Content, "[["+expectedPath+"]]") {
		t.Errorf("MEMORY.md missing session link; got %q", idx.Content)
	}
}

// TestWikiCompactionSink_PrependsToExistingIndex verifies that when
// MEMORY.md already contains a "## Past sessions" section, a new
// link is inserted immediately after the heading (most-recent first)
// rather than appended at the bottom.
func TestWikiCompactionSink_PrependsToExistingIndex(t *testing.T) {
	store := newTestStore(t)
	s := &Server{store: store}

	const userID = "et"
	const agent = "tony"
	existing := "## Past sessions\n- [[sessions/older.md]] — 2026-05-10 12:00 UTC\n"
	if err := store.UpsertMemoryPage(MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: agent, UserID: userID,
		Path: "MEMORY.md", Content: existing,
	}); err != nil {
		t.Fatalf("seed MEMORY.md: %v", err)
	}

	sink := s.wikiCompactionSink(userID, agent)
	meta := vega.CompactionMeta{
		AgentName: agent,
		DroppedAt: time.Date(2026, 5, 17, 16, 23, 5, 0, time.UTC),
	}
	if err := sink(context.Background(), "newer summary", meta); err != nil {
		t.Fatalf("sink: %v", err)
	}

	idx, _ := store.GetMemoryPage(MemoryScopeAgent, agent, userID, "MEMORY.md")
	newerPath := "sessions/2026-05-17-162305.md"
	olderPath := "sessions/older.md"
	newerIdx := strings.Index(idx.Content, newerPath)
	olderIdx := strings.Index(idx.Content, olderPath)
	if newerIdx < 0 || olderIdx < 0 {
		t.Fatalf("both session links should be present; got %q", idx.Content)
	}
	if newerIdx > olderIdx {
		t.Errorf("newer link should appear before older link; got newer=%d older=%d in %q", newerIdx, olderIdx, idx.Content)
	}
}
