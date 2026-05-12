package serve

import (
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

// postgresMigrations is the embedded FS of timestamped SQL files under
// serve/migrations/postgres/ (refs govega#62). Each file uses goose's
// `-- +goose Up` / `-- +goose Down` markers. Adding a new schema change
// is a one-file diff: bump the numeric prefix, write the SQL.
//
//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

// runPostgresMigrations applies any pending migrations against the
// supplied connection. Idempotent — re-running on a current database
// is a no-op. Goose tracks applied versions in the goose_db_version
// table; check `make pg-shell` then `SELECT * FROM goose_db_version`
// to see where the DB is.
func (s *PostgresStore) runMigrations() error {
	goose.SetBaseFS(postgresMigrations)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	// goose logs verbosely by default; mute it so test output stays clean
	// and prod logs aren't drowned in "OK 0001_initial_schema.sql".
	goose.SetLogger(goose.NopLogger())

	if err := goose.Up(s.db, "migrations/postgres"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
