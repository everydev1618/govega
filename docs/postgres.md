# Postgres backend

govega ships with two persistence backends: **SQLite** (the zero-config
default — single file, embedded, no infrastructure) and **Postgres**
(for hosted multi-writer deployments where SQLite's single-writer model
and lack of row-level isolation start to bite). Same `Store` interface,
same wire shapes, same tests — pick at boot via `Config.DBKind`.

This doc covers when to switch, how to enable it, and what to expect on
the operational side.

## When to switch

Stay on **SQLite** when:

- One server process, one writer at a time.
- Single-user / self-hosted / dev. The convenience of "one file you can
  back up with `cp`" outweighs the operational tax of a separate DB.
- You're building locally and don't have a Postgres handy.

Switch to **Postgres** when:

- More than one apexvega process needs to share state (load-balanced
  hosting, blue/green deploys, horizontal read scaling).
- You need real concurrency. SQLITE_BUSY retries get expensive past a
  handful of writers; Postgres handles tens of thousands without
  ceremony.
- Multi-tenant — the data model is moving toward `workspace_id` on
  every row (refs #32 / Phase 2). Postgres makes per-tenant isolation
  trivial via row-level security; SQLite makes it your problem.
- You want serious search later — Postgres `tsvector` / `pg_trgm` are
  the natural upgrade path for the LIKE-based activity log and memory
  search.
- You want server-side aggregations to scale — agent spend rollups,
  task stats, etc. Postgres window functions + partitioning beat
  iterating snapshots in app code.

## Configuration

Two config fields select the backend:

```go
serve.Config{
    DBKind: serve.DBKindPostgres,
    DBURL:  "postgres://user:pass@host:5432/vega?sslmode=require",
    // DBPath is ignored when DBKind is "postgres".
}
```

Or via env / `vega serve` flags in your embedding harness:

```bash
export VEGA_DB_KIND=postgres
export VEGA_DB_URL='postgres://user:pass@host:5432/vega?sslmode=require'
```

Defaults to SQLite when `DBKind` is empty so existing deployments are
untouched.

## First boot

`store.Init()` runs the bundled schema with `CREATE TABLE IF NOT
EXISTS` for every table. Idempotent — running against an already-
populated database is a no-op. You can point a fresh apexvega at an
empty Postgres database with no manual migration step.

```bash
# Local dev — create a database and point apexvega at it.
createdb vega
VEGA_DB_KIND=postgres \
VEGA_DB_URL='postgres://localhost/vega?sslmode=disable' \
./bin/apex serve
```

The schema is in `serve/store_postgres_schema.go` (one Go string
constant). For ops dumps:

```bash
pg_dump --schema-only --no-owner vega > schema.sql
```

## Operational notes

- **Connection pooling** is handled by `database/sql` via the pgx
  stdlib driver. Tune via `db.SetMaxOpenConns` / `db.SetMaxIdleConns`
  if you need to — defaults are fine for most embeddings.
- **Migrations** today are additive: every CREATE uses IF NOT EXISTS
  and new columns ship as `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`
  hooks in `Init()`. Any destructive change will land behind a
  versioned migration tool when needed.
- **Backups** are standard `pg_dump` / `pg_basebackup`. No custom
  format.
- **Row-level security** is not yet enabled — `workspace_id` columns
  arrive with Phase 2 / WorkOS (#32). Until then a Postgres-backed
  deploy is single-tenant, same as SQLite.
- **Blob storage** — agent brain files are still stored inline as
  `BYTEA` for now. The follow-up phase (#61 phase 5) moves them to an
  object store (filesystem / S3-compatible) so the relational DB
  doesn't carry document content. No API shape change planned.

## Migrating an existing SQLite install

There is no built-in SQLite → Postgres export today. Two practical
paths:

1. **Start fresh.** Stop apexvega, drop the SQLite file, point at an
   empty Postgres. Composed agents will need to be re-created (or
   re-uploaded via `POST /api/v1/config/upload`). YAML-defined agents
   regenerate from the document on boot. Settings (including MCP
   credentials) need to be re-entered.
2. **Hand-roll an export.** `pgloader` handles SQLite → Postgres well
   if you don't mind installing it. The schema names match between
   backends so `pgloader sqlite://vega.db postgres://...` is usually
   enough, plus a couple of `ALTER` cleanups for `INTEGER` vs `BIGINT`
   column types.

When `#32` ships, the cutover doc gets a real migration tool. Until
then most installs that switch are early enough that path 1 is fine.

## Testing

Test code lives in `serve/store_dual_test.go` and `serve/store_dual_methods_test.go`.

```bash
# SQLite only (no Postgres needed).
go test ./serve

# Both backends.
createdb vega_test
VEGA_TEST_POSTGRES_URL='postgres://localhost/vega_test?sslmode=disable' \
    go test ./serve
```

Each dual-backend test runs as two subtests (`/sqlite` and `/postgres`).
Postgres tests run in private schemas that drop on cleanup, so parallel
runs don't collide. When `VEGA_TEST_POSTGRES_URL` isn't set the
Postgres subtests skip cleanly — CI without Postgres just runs SQLite.

## Trade-offs

| Concern | SQLite | Postgres |
|---|---|---|
| Setup | None — one file | Requires running instance |
| Concurrent writers | Serial | Parallel |
| Search | LIKE only today | LIKE today, FTS later |
| Blob storage | Inline BLOB | Inline BYTEA today; object store later |
| Multi-tenancy | App-level (Phase 2) | App-level + RLS once `workspace_id` lands |
| Backup | `cp vega.db` | `pg_dump` |
| Operational tax | Zero | One more service to monitor |

Neither is "right" universally — pick the one whose trade-offs match
the deploy. The Store interface keeps the choice reversible.
