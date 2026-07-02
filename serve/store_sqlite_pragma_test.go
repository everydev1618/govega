package serve

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestSQLitePragmasApplyPoolWide verifies busy_timeout and foreign_keys are
// set on EVERY pooled connection, not just the one that happened to run the
// Exec("PRAGMA ...") at startup. A connection without busy_timeout returns
// SQLITE_BUSY immediately under write concurrency — the root cause of the
// intermittent lock errors the 3-retry hack papers over.
func TestSQLitePragmasApplyPoolWide(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "pragmas.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Hold several connections open simultaneously to force the pool to
	// create distinct underlying connections.
	const n = 4
	conns := make([]*sql.Conn, 0, n)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < n; i++ {
		c, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		conns = append(conns, c)
	}

	for i, c := range conns {
		var busy int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatalf("conn %d: query busy_timeout: %v", i, err)
		}
		if busy < 30000 {
			t.Errorf("conn %d: busy_timeout = %d, want >= 30000 (pragma not pool-wide)", i, busy)
		}

		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("conn %d: query foreign_keys: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, fk)
		}

		var journal string
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatalf("conn %d: query journal_mode: %v", i, err)
		}
		if journal != "wal" {
			t.Errorf("conn %d: journal_mode = %q, want wal", i, journal)
		}
	}
}
