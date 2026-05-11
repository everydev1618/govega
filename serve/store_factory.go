package serve

import (
	"fmt"
)

// openConfiguredStore opens the persistence backend selected by cfg.DBKind
// (refs govega#61). Returns the store unininitialised — the caller calls
// Init() to create the schema.
//
//   - DBKindSQLite (default when empty): zero-config single-file store
//     at cfg.DBPath. The right choice for dev, single-user self-hosting,
//     and hobby installs.
//   - DBKindPostgres: connects via cfg.DBURL. Use this for hosted,
//     multi-writer deployments where SQLite's single-writer model and
//     lack of row-level isolation bite.
func openConfiguredStore(cfg Config) (Store, error) {
	switch cfg.DBKind {
	case "", DBKindSQLite:
		return NewSQLiteStore(cfg.DBPath)
	case DBKindPostgres:
		if cfg.DBURL == "" {
			return nil, fmt.Errorf("postgres backend selected but DBURL is empty")
		}
		return NewPostgresStore(cfg.DBURL)
	default:
		return nil, fmt.Errorf("unknown db kind %q (expected sqlite or postgres)", cfg.DBKind)
	}
}
