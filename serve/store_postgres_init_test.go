package serve

import (
	"os"
	"testing"
)

// TestPostgresInit_CreatesSchema runs against a real Postgres instance
// when VEGA_TEST_POSTGRES_URL is set (refs govega#61). Local dev path:
//
//	createdb vega_phase1_test
//	VEGA_TEST_POSTGRES_URL=postgres://localhost/vega_phase1_test?sslmode=disable \
//	    go test ./serve -run TestPostgresInit
//
// Skipped when the env var isn't set so CI / dev runs that don't have
// Postgres handy don't fail.
func TestPostgresInit_CreatesSchema(t *testing.T) {
	url := os.Getenv("VEGA_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("VEGA_TEST_POSTGRES_URL not set; skipping Postgres-backed test")
	}
	store, err := NewPostgresStore(url)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()

	// First Init creates the schema.
	if err := store.Init(); err != nil {
		t.Fatalf("Init (fresh): %v", err)
	}
	// Re-init must be a no-op — the CREATE IF NOT EXISTS pattern guards
	// every table. This is the upgrade case (server restart against an
	// already-populated DB).
	if err := store.Init(); err != nil {
		t.Fatalf("Init (idempotent): %v", err)
	}

	// Sanity-check that every table the SQLite schema declares also
	// exists on Postgres. If a Phase-2 schema addition lands on SQLite
	// and forgets the Postgres side, this test catches it before the
	// method ports in Phase 3 silently fall back to errors.
	wantTables := []string{
		"events", "process_snapshots", "workflow_runs", "composed_agents",
		"chat_messages", "user_memory", "scheduled_jobs", "agent_brain_files",
		"memory_items", "workspace_files", "settings", "mcp_servers",
		"channels", "channel_messages", "channel_reads", "chat_reads",
		"inbox_items", "prompt_history", "tasks", "task_comments", "task_processes",
	}
	for _, table := range wantTables {
		var exists bool
		if err := store.db.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s missing from Postgres schema", table)
		}
	}
}

// TestStoreFactory_DefaultsToSQLite confirms that an empty/sqlite DBKind
// still opens a SQLite store — preserving the zero-config dev experience.
func TestStoreFactory_DefaultsToSQLite(t *testing.T) {
	cfg := Config{DBPath: t.TempDir() + "/test.db"}
	store, err := openConfiguredStore(cfg)
	if err != nil {
		t.Fatalf("openConfiguredStore default: %v", err)
	}
	defer store.Close()
	if _, ok := store.(*SQLiteStore); !ok {
		t.Errorf("default factory returned %T, want *SQLiteStore", store)
	}
}

// TestStoreFactory_PostgresRequiresURL confirms misconfiguration fails
// loudly rather than silently falling back.
func TestStoreFactory_PostgresRequiresURL(t *testing.T) {
	cfg := Config{DBKind: DBKindPostgres}
	if _, err := openConfiguredStore(cfg); err == nil {
		t.Error("expected error when Postgres selected without DBURL")
	}
}

// TestStoreFactory_RejectsUnknownKind catches typos in config.
func TestStoreFactory_RejectsUnknownKind(t *testing.T) {
	cfg := Config{DBKind: "mysql"}
	if _, err := openConfiguredStore(cfg); err == nil {
		t.Error("expected error for unknown db kind")
	}
}
