package serve

import (
	"strings"
	"testing"
)

// backfillUserScopeLegacyItems catches the gap left by the v1 migration:
// any `memory_items` rows written via the legacy `remember` tool AFTER v1
// stamped are stranded in the legacy table and invisible to /memory. v2
// dumps them into a user-scope `legacy-items.md` page so they show up in
// the wiki UI. Pre-v1 rows are also included for completeness — v1 had
// written them to agent-scope (private), v2 mirrors them to user-scope.
//
// Refs govega#71 follow-up (Marcel/Brittany missing-from-/memory bug).

func TestBackfillV2_EmptyStoreStampsAndReturns(t *testing.T) {
	store := newTestStore(t)
	report, err := backfillUserScopeLegacyItems(store)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !report.JustApplied {
		t.Error("first run on empty store should still stamp the flag (JustApplied=true)")
	}
	if report.PagesWritten != 0 {
		t.Errorf("empty store: expected 0 pages written, got %d", report.PagesWritten)
	}
}

func TestBackfillV2_WritesUserScopePageWithLegacyItems(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "et", Agent: "charlie", Type: MemoryTypeReference,
		Topic: "contacts", Content: "Marcel — (506) 381-5616, marcel@example.com",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "et", Agent: "charlie", Type: MemoryTypeReference,
		Topic: "contacts", Content: "Brittany Cotton — (541) 601-7296, brittany@7ctos.com",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}

	report, err := backfillUserScopeLegacyItems(store)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !report.JustApplied {
		t.Error("first run should be JustApplied")
	}
	if report.UsersTouched != 1 {
		t.Errorf("UsersTouched = %d, want 1", report.UsersTouched)
	}

	page, err := store.GetMemoryPage(MemoryScopeUser, "et", "et", "legacy-items.md")
	if err != nil {
		t.Fatalf("GetMemoryPage: %v", err)
	}
	if page == nil {
		t.Fatal("user-scope legacy-items.md must exist after backfill")
	}
	for _, want := range []string{"Marcel", "Brittany", "contacts"} {
		if !strings.Contains(page.Content, want) {
			t.Errorf("legacy-items.md missing %q: %s", want, page.Content)
		}
	}
}

func TestBackfillV2_IsIdempotent(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "et", Agent: "charlie", Type: MemoryTypeUser,
		Topic: "preferences", Content: "prefers concise answers",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}

	first, err := backfillUserScopeLegacyItems(store)
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if !first.JustApplied {
		t.Error("first run should be JustApplied")
	}

	// Simulate a hand-edit (or Mira refile) on the user-scope legacy page.
	// A second backfill must NOT clobber it — that's the whole point of
	// the flag gate.
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "legacy-items.md", Content: "## Refiled by Mira\n\n- preferences moved into profile.md",
	})

	second, err := backfillUserScopeLegacyItems(store)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if !second.AlreadyApplied {
		t.Error("second run should be AlreadyApplied")
	}
	if second.PagesWritten != 0 {
		t.Errorf("second run wrote %d pages — must be 0 (idempotent)", second.PagesWritten)
	}

	got, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "legacy-items.md")
	if got == nil || !strings.Contains(got.Content, "Refiled by Mira") {
		t.Errorf("idempotent run clobbered refile work: %+v", got)
	}
}

func TestBackfillV2_SplitsByUserID(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "et", Agent: "charlie", Type: MemoryTypeReference,
		Topic: "contacts", Content: "Marcel",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "other", Agent: "charlie", Type: MemoryTypeReference,
		Topic: "contacts", Content: "Stranger",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}

	if _, err := backfillUserScopeLegacyItems(store); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	etPage, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "legacy-items.md")
	if etPage == nil || !strings.Contains(etPage.Content, "Marcel") {
		t.Errorf("et's page missing Marcel: %+v", etPage)
	}
	if etPage != nil && strings.Contains(etPage.Content, "Stranger") {
		t.Error("et's page leaked another user's data")
	}
	otherPage, _ := store.GetMemoryPage(MemoryScopeUser, "other", "other", "legacy-items.md")
	if otherPage == nil || !strings.Contains(otherPage.Content, "Stranger") {
		t.Errorf("other's page missing Stranger: %+v", otherPage)
	}
}
