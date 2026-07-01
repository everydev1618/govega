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

// A no-op reaction is dropped, not journaled — the importance gate that keeps
// the wiki from filling with noise (§3.4). Covers both empty output and short
// dismissal phrases.
func TestConsolidateReactiveWake_SkipsNoOp(t *testing.T) {
	for _, result := range []string{"   ", "Nothing to do here.", "No action needed."} {
		store := newTestStore(t)
		s := &Server{store: store}
		const agent = "night-watch"
		e := events.Event{Type: "agent.completed", Data: map[string]any{"status": "ok"}}
		s.consolidateReactiveWake(t.Context(), agent, e, result)

		pages, err := store.ListMemoryPages(MemoryScopeAgent, agent, reactiveOwnerUserID, "sessions/reactive-")
		if err != nil {
			t.Fatalf("ListMemoryPages: %v", err)
		}
		if len(pages) != 0 {
			t.Fatalf("no-op result %q should not consolidate, but %d note(s) written", result, len(pages))
		}
	}
}

// After enough reactions to the same event type, the pattern is promoted into
// the always-injected MEMORY.md — repeated experience becoming disposition
// (§3.6 / D4). The promotion line is updated in place, not duplicated.
func TestReactivePromotionIntoMemoryMD(t *testing.T) {
	store := newTestStore(t)
	s := &Server{store: store}
	const agent = "night-watch"
	e := events.Event{Type: "agent.completed", Data: map[string]any{"status": "failed"}}

	memHas := func() (string, bool) {
		p, _ := store.GetMemoryPage(MemoryScopeAgent, agent, reactiveOwnerUserID, "MEMORY.md")
		if p == nil {
			return "", false
		}
		return p.Content, strings.Contains(p.Content, "Recurring") && strings.Contains(p.Content, "agent.completed")
	}

	// Below threshold: no promotion yet.
	for range reactivePromotionThreshold - 1 {
		s.consolidateReactiveWake(t.Context(), agent, e, "Noted the failure.")
	}
	if _, ok := memHas(); ok {
		t.Fatal("promoted too early — below the recurrence threshold")
	}

	// Crossing the threshold promotes.
	s.consolidateReactiveWake(t.Context(), agent, e, "Noted the failure.")
	content, ok := memHas()
	if !ok {
		t.Fatalf("expected a Recurring line in MEMORY.md after threshold, got: %q", content)
	}

	// Another reaction updates in place — exactly one Recurring line.
	s.consolidateReactiveWake(t.Context(), agent, e, "Noted the failure.")
	content, _ = memHas()
	if got := strings.Count(content, "Recurring:"); got != 1 {
		t.Fatalf("promotion should update in place, found %d Recurring lines:\n%s", got, content)
	}
}
