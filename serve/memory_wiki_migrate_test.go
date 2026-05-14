package serve

import (
	"strings"
	"testing"
)

// migrateToWikiMemory moves legacy user_memory + memory_items rows into
// the wiki. Tests cover the layer→page mapping, scope split, and the
// idempotency flag. Refs govega#71.

func TestMigrateWikiMemory_EmptyStoreIsNoOp(t *testing.T) {
	store := newTestStore(t)
	report, err := migrateToWikiMemory(store)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if report.PagesWritten != 0 {
		t.Errorf("expected 0 pages written, got %d", report.PagesWritten)
	}
	if !report.AlreadyApplied && !report.JustApplied {
		t.Error("flag should be one of AlreadyApplied / JustApplied after a run")
	}
}

func TestMigrateWikiMemory_IsIdempotent(t *testing.T) {
	store := newTestStore(t)
	mustUpsertUserMemory(t, store, "et", "tony", "profile", `{"role":"founder"}`)

	first, err := migrateToWikiMemory(store)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if !first.JustApplied {
		t.Error("first run should be JustApplied")
	}
	if first.PagesWritten == 0 {
		t.Error("first run should write at least one page")
	}

	// Mutate a wiki page to confirm the second run doesn't overwrite it.
	mustUpsertPage(t, store, MemoryPage{
		Scope: MemoryScopeUser, ScopeID: "et", UserID: "et",
		Path: "profile.md", Content: "post-migration hand edit",
	})

	second, err := migrateToWikiMemory(store)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if !second.AlreadyApplied {
		t.Error("second run should be AlreadyApplied")
	}
	if second.PagesWritten != 0 {
		t.Errorf("second run wrote %d pages — must be 0 (idempotent)", second.PagesWritten)
	}

	// Confirm the hand-edit survived.
	got, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "profile.md")
	if got == nil || !strings.Contains(got.Content, "post-migration hand edit") {
		t.Errorf("idempotent run clobbered hand edit: %+v", got)
	}
}

func TestMigrateWikiMemory_UserMemoryLayersMapCorrectly(t *testing.T) {
	store := newTestStore(t)
	mustUpsertUserMemory(t, store, "et", "tony", "profile", `{"role":"founder"}`)
	mustUpsertUserMemory(t, store, "et", "tony", "topics", `{"sushi":"opening a restaurant"}`)
	mustUpsertUserMemory(t, store, "et", "tony", "notes", `{"favorite":"omakase"}`)
	mustUpsertUserMemory(t, store, "et", "tony", "journal", `[{"date":"2026-05-01","challenge":"hiring","advice":"go slow"}]`)

	if _, err := migrateToWikiMemory(store); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// profile / topics / notes → shared user wiki
	for _, page := range []string{"profile.md", "topics.md", "notes.md", "MEMORY.md"} {
		p, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", page)
		if p == nil {
			t.Errorf("missing shared page %q after migration", page)
		}
	}
	// journal → per-agent wiki
	jp, _ := store.GetMemoryPage(MemoryScopeAgent, "tony", "et", "legacy-journal.md")
	if jp == nil || !strings.Contains(jp.Content, "hiring") {
		t.Errorf("journal not migrated to agent wiki: %+v", jp)
	}
	// Shared pages should NOT carry journal content.
	sharedProfile, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "profile.md")
	if strings.Contains(sharedProfile.Content, "hiring") {
		t.Errorf("journal leaked into shared profile.md: %q", sharedProfile.Content)
	}
}

func TestMigrateWikiMemory_MemoryItemsToLegacyItems(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "et", Agent: "tony", Type: MemoryTypeUser, Topic: "preferences", Content: "prefers concise answers",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID: "et", Agent: "tony", Type: MemoryTypeProject, Topic: "sushi", Content: "opening in Q4",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}

	if _, err := migrateToWikiMemory(store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	itemsPage, _ := store.GetMemoryPage(MemoryScopeAgent, "tony", "et", "legacy-items.md")
	if itemsPage == nil {
		t.Fatal("legacy-items.md not created")
	}
	if !strings.Contains(itemsPage.Content, "prefers concise") || !strings.Contains(itemsPage.Content, "opening in Q4") {
		t.Errorf("legacy items content missing: %q", itemsPage.Content)
	}
}

func TestMigrateWikiMemory_MultipleAgentsAndUsers(t *testing.T) {
	store := newTestStore(t)
	mustUpsertUserMemory(t, store, "et", "tony", "profile", `{"role":"founder"}`)
	mustUpsertUserMemory(t, store, "et", "hana", "journal", `[{"date":"2026-05-01","challenge":"first cohort"}]`)
	mustUpsertUserMemory(t, store, "kassidy", "tony", "profile", `{"role":"engineer"}`)

	if _, err := migrateToWikiMemory(store); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Per-user shared scope is isolated.
	etProfile, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "profile.md")
	kasProfile, _ := store.GetMemoryPage(MemoryScopeUser, "kassidy", "kassidy", "profile.md")
	if etProfile == nil || kasProfile == nil {
		t.Fatal("per-user shared pages missing")
	}
	if strings.Contains(etProfile.Content, "engineer") {
		t.Errorf("kassidy's profile leaked into et's: %q", etProfile.Content)
	}
	if strings.Contains(kasProfile.Content, "founder") {
		t.Errorf("et's profile leaked into kassidy's: %q", kasProfile.Content)
	}

	// Per-agent journal isolation: tony has none for et; hana has one.
	tonyJournal, _ := store.GetMemoryPage(MemoryScopeAgent, "tony", "et", "legacy-journal.md")
	if tonyJournal != nil {
		t.Errorf("tony shouldn't have a legacy-journal.md for et: %+v", tonyJournal)
	}
	hanaJournal, _ := store.GetMemoryPage(MemoryScopeAgent, "hana", "et", "legacy-journal.md")
	if hanaJournal == nil || !strings.Contains(hanaJournal.Content, "first cohort") {
		t.Errorf("hana's journal for et missing or wrong: %+v", hanaJournal)
	}
}

func TestMigrateWikiMemory_IndexLinksToCreatedPages(t *testing.T) {
	store := newTestStore(t)
	mustUpsertUserMemory(t, store, "et", "tony", "profile", `{"role":"founder"}`)
	mustUpsertUserMemory(t, store, "et", "tony", "notes", `{"favorite":"omakase"}`)

	if _, err := migrateToWikiMemory(store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index, _ := store.GetMemoryPage(MemoryScopeUser, "et", "et", "MEMORY.md")
	if index == nil {
		t.Fatal("MEMORY.md not created")
	}
	if !strings.Contains(index.Content, "profile.md") || !strings.Contains(index.Content, "notes.md") {
		t.Errorf("index doesn't link to created pages: %q", index.Content)
	}
	// Links should be extracted into the graph.
	links, _ := store.ListMemoryLinks(MemoryScopeUser, "et", "et")
	wantSet := map[string]bool{"profile.md": false, "notes.md": false}
	for _, l := range links {
		if _, ok := wantSet[l.ToPath]; ok {
			wantSet[l.ToPath] = true
		}
	}
	for to, ok := range wantSet {
		if !ok {
			t.Errorf("link → %s not extracted; got %+v", to, links)
		}
	}
}

func mustUpsertUserMemory(t *testing.T, store Store, userID, agent, layer, content string) {
	t.Helper()
	if err := store.UpsertUserMemory(userID, agent, layer, content); err != nil {
		t.Fatalf("UpsertUserMemory: %v", err)
	}
}
