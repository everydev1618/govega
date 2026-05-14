package serve

import (
	"path/filepath"
	"testing"
)

// newTestStore returns a freshly-initialised SQLiteStore backed by a
// temp file. Using a real file (not :memory:) ensures the schema
// migration paths execute the same as in production. Used by every
// in-package test that needs a store.
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
