package serve

import (
	"context"
	"strings"
	"testing"
)

// TestRecallLedger_TracksMemoryReads confirms a memory_read call
// during a turn appends an entry to the ledger attached to that
// turn's context. This is what feeds the "remembered from X" pill
// in the chat UI.
func TestRecallLedger_TracksMemoryReads(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "topics/sushi.md", Content: "omakase preferred",
	})

	ledger := NewRecallLedger()
	ctx := ContextWithRecall(context.Background(), ledger)
	ctx = ContextWithMemory(ctx, store, "et", "tony")

	// Invoke the memory_read tool's underlying logic. We don't go
	// through the interpreter — exercising the fn directly is enough
	// because the ledger lives on context.
	got, err := readMemoryPageForRecall(ctx, "topics/sushi.md", "")
	if err != nil {
		t.Fatalf("memory_read: %v", err)
	}
	if !strings.Contains(got, "omakase") {
		t.Errorf("content not returned: %q", got)
	}

	entries := ledger.Entries()
	if len(entries) != 1 {
		t.Fatalf("ledger entries = %d, want 1", len(entries))
	}
	got0 := entries[0]
	if got0.Path != "topics/sushi.md" {
		t.Errorf("path = %q, want topics/sushi.md", got0.Path)
	}
	if got0.Scope != MemoryScopeUser {
		t.Errorf("scope = %q, want user", got0.Scope)
	}
	if got0.Source != RecallSourceRead {
		t.Errorf("source = %q, want read", got0.Source)
	}
}

// TestRecallLedger_TracksActiveInjection ensures that when a note
// is auto-injected via its `active: true` flag, it shows up in the
// ledger too — so the user sees recall even when the agent didn't
// explicitly call memory_read.
func TestRecallLedger_TracksActiveInjection(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeAgent, ScopeID: "tony", UserID: "et",
		Path:        "sessions/2026-05-17-162305.md",
		Content:     "Etienne wants pills.",
		Frontmatter: "active: true",
	})

	ledger := NewRecallLedger()
	ctx := ContextWithRecall(context.Background(), ledger)

	got := formatWikiMemoryForInjectionWithCtx(ctx, store, "et", "tony")
	if !strings.Contains(got, "Etienne wants pills") {
		t.Errorf("injection missing active body: %q", got)
	}

	entries := ledger.Entries()
	if len(entries) == 0 {
		t.Fatal("ledger should have recorded the active injection")
	}
	found := false
	for _, e := range entries {
		if e.Source == RecallSourceActive && e.Path == "sessions/2026-05-17-162305.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("active injection not recorded; entries: %+v", entries)
	}
}

// TestRecallLedger_NilSafe verifies that the ledger plumbing is
// optional — calls without a ledger on context must not panic and
// must not interfere with the existing behavior.
func TestRecallLedger_NilSafe(t *testing.T) {
	store := newTestStore(t)
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "MEMORY.md", Content: "shared",
	})

	got := formatWikiMemoryForInjection(store, "et", "tony")
	if !strings.Contains(got, "shared") {
		t.Errorf("non-recall path broke: %q", got)
	}
}
