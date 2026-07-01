package serve

import (
	"strings"
	"testing"

	"github.com/everydev1618/govega/events"
)

// A reactive wake that produced output must leave a trace in the agent's
// memory (self-learning write-back, §3.4) — regardless of how short the burst
// was, so it never depends on the 40-message compaction threshold.
func TestConsolidateReactiveWake_WritesNote(t *testing.T) {
	store := newTestStore(t)
	s := &Server{store: store}

	const agent = "night-watch"
	e := events.Event{
		Type: "agent.completed",
		Data: map[string]any{"agent": "builder", "status": "failed"},
	}
	s.consolidateReactiveWake(t.Context(), agent, e, "Noted the builder failure for the morning digest.")

	pages, err := store.ListMemoryPages(MemoryScopeAgent, agent, reactiveOwnerUserID, "sessions/reactive-")
	if err != nil {
		t.Fatalf("ListMemoryPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("want exactly 1 reactive session note, got %d", len(pages))
	}
	p := pages[0]
	if !strings.Contains(p.Content, "builder failure") {
		t.Errorf("note should capture the outcome, got: %q", p.Content)
	}
	if !strings.Contains(p.Content, "agent.completed") {
		t.Errorf("note should record the triggering event type, got: %q", p.Content)
	}
	if !strings.Contains(p.Frontmatter, "source: reactive-wake") {
		t.Errorf("note should be tagged as a reactive wake, got frontmatter: %q", p.Frontmatter)
	}
	if !strings.Contains(p.Frontmatter, "active: true") {
		t.Errorf("note should be active so it injects next wake, got: %q", p.Frontmatter)
	}
}

// A no-op reaction (no output produced) is dropped, not journaled — the
// importance gate that keeps the wiki from filling with noise (§3.4).
func TestConsolidateReactiveWake_SkipsNoOp(t *testing.T) {
	store := newTestStore(t)
	s := &Server{store: store}

	const agent = "night-watch"
	e := events.Event{Type: "agent.completed", Data: map[string]any{"status": "ok"}}
	s.consolidateReactiveWake(t.Context(), agent, e, "   ")

	pages, err := store.ListMemoryPages(MemoryScopeAgent, agent, reactiveOwnerUserID, "sessions/reactive-")
	if err != nil {
		t.Fatalf("ListMemoryPages: %v", err)
	}
	if len(pages) != 0 {
		t.Fatalf("a no-op wake should not be consolidated, but %d note(s) were written", len(pages))
	}
}
