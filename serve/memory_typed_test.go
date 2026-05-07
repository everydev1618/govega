package serve

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestStore returns a freshly-initialised SQLiteStore backed by a temp file.
// Using a real file (not :memory:) ensures the schema migration paths execute
// the same as in production.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestMemoryItem_TypeRoundTrip checks the new Type field survives a write/read
// cycle and is searchable.
func TestMemoryItem_TypeRoundTrip(t *testing.T) {
	store := newTestStore(t)

	id, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "riley",
		Type:    MemoryTypeUser,
		Content: "User prefers concise replies.",
		Tags:    "tone,brevity",
	})
	if err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	items, err := store.SearchMemoryItems("u1", "riley", "concise", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Type != MemoryTypeUser {
		t.Errorf("Type = %q, want %q", items[0].Type, MemoryTypeUser)
	}
}

// TestMemoryItem_LegacyRowsDefaultToReference verifies the schema migration
// gives existing rows (inserted before the type column existed) the
// `reference` default rather than an empty string. The migration path uses
// `ALTER TABLE ... ADD COLUMN type TEXT NOT NULL DEFAULT 'reference'`, so any
// row created without an explicit type also defaults to `reference`.
func TestMemoryItem_LegacyRowsDefaultToReference(t *testing.T) {
	store := newTestStore(t)

	// Insert WITHOUT specifying Type — simulates an old caller.
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "apex",
		Content: "old-untyped row",
	}); err != nil {
		t.Fatalf("InsertMemoryItem: %v", err)
	}

	items, err := store.SearchMemoryItems("u1", "apex", "old-untyped", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Type != MemoryTypeReference {
		t.Errorf("legacy row Type = %q, want %q", items[0].Type, MemoryTypeReference)
	}
}

// TestMemoryItem_DedupSameTypeAndContent verifies that two writes of the same
// (user_id, agent, type, content) tuple produce a single row rather than
// duplicates. Tags are merged (union of comma-separated values) and
// updated_at advances on the second write.
func TestMemoryItem_DedupSameTypeAndContent(t *testing.T) {
	store := newTestStore(t)

	first, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "apex",
		Type:    MemoryTypeFeedback,
		Content: "Stop sending end-of-turn summaries.",
		Tags:    "tone",
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Sleep just long enough for SQLite's CURRENT_TIMESTAMP to advance.
	time.Sleep(1100 * time.Millisecond)

	second, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "apex",
		Type:    MemoryTypeFeedback,
		Content: "Stop sending end-of-turn summaries.",
		Tags:    "verbosity,tone",
	})
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}

	if first != second {
		t.Errorf("dedup broken: second insert returned id=%d, want %d (same row)", second, first)
	}

	items, err := store.SearchMemoryItems("u1", "apex", "summaries", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 row after dedup, got %d", len(items))
	}

	// Tags should be the union of "tone" and "verbosity,tone".
	got := items[0].Tags
	if !strings.Contains(got, "tone") || !strings.Contains(got, "verbosity") {
		t.Errorf("tags = %q, expected merged set containing both 'tone' and 'verbosity'", got)
	}
	// Same tag should not duplicate.
	if strings.Count(got, "tone") != 1 {
		t.Errorf("tags = %q, 'tone' appears more than once after merge", got)
	}

	// updated_at should have advanced past created_at.
	if !items[0].UpdatedAt.After(items[0].CreatedAt) {
		t.Errorf("updated_at (%v) should be after created_at (%v) following the second write",
			items[0].UpdatedAt, items[0].CreatedAt)
	}
}

// TestMemoryItem_DifferentTypesNotDeduped checks that the same content under
// different types stays as separate rows — they really are different memories
// (e.g. a user fact vs. a project fact that happen to share wording).
func TestMemoryItem_DifferentTypesNotDeduped(t *testing.T) {
	store := newTestStore(t)

	idA, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "apex",
		Type:    MemoryTypeProject,
		Content: "Postgres is the primary store.",
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	idB, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "apex",
		Type:    MemoryTypeReference,
		Content: "Postgres is the primary store.",
	})
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}

	if idA == idB {
		t.Fatalf("rows of different types must remain separate; got the same id %d", idA)
	}

	items, err := store.SearchMemoryItems("u1", "apex", "Postgres", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 rows (one per type), got %d", len(items))
	}
}

// TestSearchMemoryItems_FilterByType verifies the typed search path returns
// only matching types when a filter is supplied.
func TestSearchMemoryItems_FilterByType(t *testing.T) {
	store := newTestStore(t)

	cases := []MemoryItem{
		{UserID: "u1", Agent: "apex", Type: MemoryTypeUser, Content: "user fact about postgres"},
		{UserID: "u1", Agent: "apex", Type: MemoryTypeProject, Content: "project uses postgres 16"},
		{UserID: "u1", Agent: "apex", Type: MemoryTypeReference, Content: "postgres docs at /api/db"},
	}
	for _, c := range cases {
		if _, err := store.InsertMemoryItem(c); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	got, err := store.SearchMemoryItemsByType("u1", "apex", "postgres", MemoryTypeProject, 10)
	if err != nil {
		t.Fatalf("SearchMemoryItemsByType: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row matching Type=project, got %d", len(got))
	}
	if got[0].Type != MemoryTypeProject {
		t.Errorf("Type = %q, want %q", got[0].Type, MemoryTypeProject)
	}
}

// TestMemoryType_Validate guards the four-valued enum so a typo
// ("Project" / "users") at the boundary fails fast rather than silently
// becoming a fifth type that nothing else recognises.
func TestMemoryType_Validate(t *testing.T) {
	valid := []MemoryType{MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference}
	for _, v := range valid {
		if err := v.Validate(); err != nil {
			t.Errorf("Validate(%q) returned error: %v", v, err)
		}
	}

	invalid := []MemoryType{"", "User", "Project", "preference", "REFERENCE"}
	for _, v := range invalid {
		if err := v.Validate(); err == nil {
			t.Errorf("Validate(%q) should have errored", v)
		}
	}
}
