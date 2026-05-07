package serve

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSQLiteStore_InitMigratesExistingMemoryItemsTable simulates the upgrade
// path: a database created against the pre-typed-memory schema (no `type`
// column on `memory_items`) is opened by current code. Init() must succeed
// without error. The previous bug was that the inline schema block included
//
//	CREATE INDEX ... ON memory_items(user_id, agent, type, content)
//
// which fired before the migration ALTER TABLE added the column, blowing up
// init for any existing apexvega database.
func TestSQLiteStore_InitMigratesExistingMemoryItemsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Step 1: stand up a database with the old shape — memory_items has
	// no `type` column. Use a row-level INSERT to prove the migration
	// preserves data.
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE memory_items (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    TEXT NOT NULL,
			agent      TEXT NOT NULL,
			topic      TEXT NOT NULL DEFAULT '',
			content    TEXT NOT NULL,
			tags       TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if _, err := legacy.Exec(
		`INSERT INTO memory_items (user_id, agent, topic, content, tags)
		 VALUES (?, ?, ?, ?, ?)`,
		"u1", "apex", "old-topic", "old-content", "old,tags",
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	// Step 2: open the same file with current code. Init() must succeed.
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(); err != nil {
		t.Fatalf("Init() against legacy database failed: %v", err)
	}

	// Step 3: the legacy row reads back with the migrated default type.
	items, err := store.SearchMemoryItems("u1", "apex", "old-content", 10)
	if err != nil {
		t.Fatalf("SearchMemoryItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 legacy row to read back, got %d", len(items))
	}
	if items[0].Type != MemoryTypeReference {
		t.Errorf("legacy row Type = %q, want %q", items[0].Type, MemoryTypeReference)
	}
	if items[0].Content != "old-content" {
		t.Errorf("legacy row Content = %q, want %q", items[0].Content, "old-content")
	}

	// Step 4: a fresh write against the migrated db works (exercises the
	// dedup index that's now created in the migration block).
	if _, err := store.InsertMemoryItem(MemoryItem{
		UserID:  "u1",
		Agent:   "apex",
		Type:    MemoryTypeFeedback,
		Content: "post-migration row",
	}); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
}
