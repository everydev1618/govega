package serve

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

// storeKind enumerates the backends a dual-backend test runs against.
type storeKind string

const (
	storeKindSQLite   storeKind = "sqlite"
	storeKindPostgres storeKind = "postgres"
)

// forEachStore runs fn once per available backend (refs govega#61
// phase 4). SQLite always runs. Postgres runs only when
// VEGA_TEST_POSTGRES_URL is set; otherwise the Postgres subtest is
// skipped so CI / dev without Postgres handy keeps working.
//
// Each subtest gets a fresh store — for Postgres that means a unique
// schema isolated from any other in-flight test runs.
func forEachStore(t *testing.T, fn func(t *testing.T, store Store)) {
	t.Helper()
	t.Run(string(storeKindSQLite), func(t *testing.T) {
		store := newTestStore(t)
		fn(t, store)
	})
	t.Run(string(storeKindPostgres), func(t *testing.T) {
		store := newPostgresTestStore(t)
		if store == nil {
			t.Skip("VEGA_TEST_POSTGRES_URL not set; skipping Postgres-backed test")
		}
		fn(t, store)
	})
}

// newPostgresTestStore opens a fresh, isolated Postgres-backed store
// for one test. Each call gets its own schema so tests can't see each
// other's rows. Returns nil when VEGA_TEST_POSTGRES_URL isn't set so
// callers (forEachStore) can skip rather than fail.
//
// The schema is dropped on test cleanup so a long suite run doesn't
// litter the database with per-test relics.
func newPostgresTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	url := os.Getenv("VEGA_TEST_POSTGRES_URL")
	if url == "" {
		return nil
	}
	store, err := NewPostgresStore(url)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	schema := "vega_test_" + randHex(6)
	// Pin every subsequent query in this connection pool to a private
	// schema so the table definitions don't clash across parallel runs.
	if _, err := store.db.Exec("CREATE SCHEMA " + schema); err != nil {
		store.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	if _, err := store.db.Exec("SET search_path TO " + schema); err != nil {
		_, _ = store.db.Exec("DROP SCHEMA " + schema + " CASCADE")
		store.Close()
		t.Fatalf("set search_path: %v", err)
	}
	if err := store.Init(); err != nil {
		_, _ = store.db.Exec("DROP SCHEMA " + schema + " CASCADE")
		store.Close()
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec("DROP SCHEMA " + schema + " CASCADE")
		store.Close()
	})
	return store
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("rand.Read: %w", err))
	}
	return hex.EncodeToString(b)
}
