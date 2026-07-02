package serve

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	"github.com/everydev1618/govega/serve/peering"
	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using modernc.org/sqlite (pure Go).
type SQLiteStore struct {
	db *sql.DB
}

// PeeringStore returns a peering.Store backed by the same database. Used
// by the orchestrator-to-orchestrator federation layer to share storage
// without bloating the main Store interface.
func (s *SQLiteStore) PeeringStore() peering.Store {
	return peering.NewSQLiteStorage(s.db)
}

// sqliteDSN builds a DSN that applies the pragmas on every pooled
// connection. Exec("PRAGMA ...") only configures the single connection
// that happens to run it — any other connection in database/sql's pool
// keeps busy_timeout=0 and fails immediately with SQLITE_BUSY under
// write concurrency. DSN-encoded pragmas are applied by the driver at
// connection setup, pool-wide.
//
//   - busy_timeout: writers wait up to 30s for the lock instead of erroring
//   - journal_mode=WAL: concurrent readers during writes (persistent, but
//     repeated per-connection is harmless and covers fresh files)
//   - foreign_keys: enforce FK constraints (off by default in SQLite)
func sqliteDSN(path string) string {
	pragmas := "_pragma=busy_timeout(30000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)"
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + pragmas
}

// NewSQLiteStore opens or creates a SQLite database at the given path.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	// Verify the DSN pragmas took effect (a typo'd pragma fails silently
	// on some driver versions) — one probe query forces a connection.
	var busy int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy <= 0 {
		db.Close()
		return nil, fmt.Errorf("sqlite busy_timeout pragma not applied (got %d): %v", busy, err)
	}
	return &SQLiteStore{db: db}, nil
}

// SweepRetention deletes rows older than the per-table retention windows.
// Timestamps are bound as time.Time — the driver stores and compares them
// in RFC3339 text form consistently with the insert paths.
func (s *SQLiteStore) SweepRetention(policy RetentionPolicy) (RetentionSweepResult, error) {
	var res RetentionSweepResult
	now := time.Now().UTC()

	sweep := func(query string, retention time.Duration, out *int64) error {
		if retention <= 0 {
			return nil
		}
		r, err := s.db.Exec(query, now.Add(-retention))
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		*out = n
		return nil
	}

	if err := sweep(`DELETE FROM events WHERE timestamp < ?`, policy.Events, &res.Events); err != nil {
		return res, fmt.Errorf("sweep events: %w", err)
	}
	if err := sweep(`DELETE FROM process_snapshots WHERE snapshot_at < ?`, policy.Snapshots, &res.Snapshots); err != nil {
		return res, fmt.Errorf("sweep process_snapshots: %w", err)
	}
	if err := sweep(`DELETE FROM chat_messages WHERE created_at < ?`, policy.ChatMessages, &res.ChatMessages); err != nil {
		return res, fmt.Errorf("sweep chat_messages: %w", err)
	}
	return res, nil
}

// Init creates the schema tables.
func (s *SQLiteStore) Init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS events (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		type        TEXT NOT NULL,
		process_id  TEXT NOT NULL DEFAULT '',
		agent_name  TEXT NOT NULL DEFAULT '',
		timestamp   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		data        TEXT NOT NULL DEFAULT '',
		result      TEXT NOT NULL DEFAULT '',
		error       TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS process_snapshots (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		process_id    TEXT NOT NULL,
		agent_name    TEXT NOT NULL DEFAULT '',
		status        TEXT NOT NULL DEFAULT '',
		parent_id     TEXT NOT NULL DEFAULT '',
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cost_usd      REAL NOT NULL DEFAULT 0,
		started_at    DATETIME,
		completed_at  DATETIME,
		snapshot_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS workflow_runs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id     TEXT NOT NULL UNIQUE,
		workflow   TEXT NOT NULL,
		inputs     TEXT NOT NULL DEFAULT '{}',
		status     TEXT NOT NULL DEFAULT 'running',
		result     TEXT NOT NULL DEFAULT '',
		started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS composed_agents (
		name            TEXT PRIMARY KEY,
		display_name    TEXT NOT NULL DEFAULT '',
		title           TEXT NOT NULL DEFAULT '',
		description     TEXT NOT NULL DEFAULT '',
		avatar          TEXT NOT NULL DEFAULT '',
		icon            TEXT NOT NULL DEFAULT '',
		avatar_gradient TEXT NOT NULL DEFAULT '[]',
		model           TEXT NOT NULL DEFAULT '',
		persona         TEXT NOT NULL DEFAULT '',
		skills          TEXT NOT NULL DEFAULT '[]',
		tools           TEXT NOT NULL DEFAULT '[]',
		team            TEXT NOT NULL DEFAULT '[]',
		system          TEXT NOT NULL DEFAULT '',
		temperature     REAL,
		triggers        TEXT NOT NULL DEFAULT '[]',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS chat_messages (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		agent           TEXT NOT NULL,
		role            TEXT NOT NULL,
		content         TEXT NOT NULL,
		tool_activities TEXT NOT NULL DEFAULT '[]',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS user_memory (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    TEXT NOT NULL,
		agent      TEXT NOT NULL,
		layer      TEXT NOT NULL,
		content    TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_user_memory_unique
		ON user_memory(user_id, agent, layer);

	CREATE TABLE IF NOT EXISTS scheduled_jobs (
		name       TEXT PRIMARY KEY,
		cron       TEXT NOT NULL,
		agent_name TEXT NOT NULL,
		message    TEXT NOT NULL,
		enabled    BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS memory_items (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    TEXT NOT NULL,
		agent      TEXT NOT NULL,
		type       TEXT NOT NULL DEFAULT 'reference',
		topic      TEXT NOT NULL DEFAULT '',
		content    TEXT NOT NULL,
		tags       TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_memory_items_user_agent ON memory_items(user_id, agent);
	CREATE INDEX IF NOT EXISTS idx_memory_items_topic ON memory_items(user_id, agent, topic);
	-- idx_memory_items_dedup is created in the migration block below, after
	-- ALTER TABLE has added the type column on pre-existing databases.

	CREATE TABLE IF NOT EXISTS workspace_files (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		path        TEXT NOT NULL,
		agent       TEXT NOT NULL DEFAULT '',
		process_id  TEXT NOT NULL DEFAULT '',
		operation   TEXT NOT NULL DEFAULT 'write',
		description TEXT NOT NULL DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_workspace_files_agent ON workspace_files(agent);

	CREATE TABLE IF NOT EXISTS settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		sensitive  INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS mcp_servers (
		name       TEXT PRIMARY KEY,
		config     TEXT NOT NULL DEFAULT '{}',
		disabled   INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS channels (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL UNIQUE,
		description TEXT DEFAULT '',
		team        TEXT DEFAULT '[]',
		mode        TEXT DEFAULT '',
		created_by  TEXT NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS channel_messages (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id      TEXT NOT NULL,
		thread_id       INTEGER,
		agent           TEXT DEFAULT '',
		role            TEXT NOT NULL,
		content         TEXT NOT NULL,
		metadata        TEXT DEFAULT '{}',
		tool_activities TEXT NOT NULL DEFAULT '[]',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_channel_messages_channel ON channel_messages(channel_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_channel_messages_thread ON channel_messages(thread_id);

	CREATE TABLE IF NOT EXISTS agent_inbox (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		from_agent      TEXT NOT NULL,
		subject         TEXT NOT NULL,
		body            TEXT NOT NULL DEFAULT '',
		priority        TEXT NOT NULL DEFAULT 'normal',
		status          TEXT NOT NULL DEFAULT 'pending',
		resolution      TEXT DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		resolved_at     DATETIME,
		triage_count    INTEGER NOT NULL DEFAULT 0,
		last_triaged_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_agent_inbox_status ON agent_inbox(status, created_at);

	CREATE TABLE IF NOT EXISTS inbox_replies (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		inbox_id  INTEGER NOT NULL,
		role      TEXT NOT NULL,
		agent     TEXT NOT NULL DEFAULT '',
		content   TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (inbox_id) REFERENCES agent_inbox(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_inbox_replies_inbox ON inbox_replies(inbox_id, created_at);

	CREATE TABLE IF NOT EXISTS prompt_history (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		prompt     TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS channel_read_cursors (
		channel_id TEXT NOT NULL,
		user_id    TEXT NOT NULL DEFAULT 'default',
		last_read_id INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (channel_id, user_id)
	);

	CREATE TABLE IF NOT EXISTS chat_read_cursors (
		agent   TEXT NOT NULL,
		user_id TEXT NOT NULL DEFAULT 'default',
		last_read_id INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (agent, user_id)
	);

	-- Wiki-style memory pages (govega#71). Replaces the typed
	-- user_memory / memory_items system in steps 2+. Path is a logical
	-- slash-separated address ("MEMORY.md", "topics/sushi.md"), NOT a
	-- filesystem path.
	CREATE TABLE IF NOT EXISTS memory_pages (
		scope       TEXT NOT NULL,
		scope_id    TEXT NOT NULL,
		user_id     TEXT NOT NULL,
		path        TEXT NOT NULL,
		content     TEXT NOT NULL DEFAULT '',
		frontmatter TEXT NOT NULL DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (scope, scope_id, user_id, path)
	);
	CREATE INDEX IF NOT EXISTS idx_memory_pages_updated
		ON memory_pages(scope, scope_id, user_id, updated_at DESC);

	-- Directed link edges between memory pages. Extracted from content
	-- on every write so the graph endpoint is cheap.
	CREATE TABLE IF NOT EXISTS memory_links (
		scope     TEXT NOT NULL,
		scope_id  TEXT NOT NULL,
		user_id   TEXT NOT NULL,
		from_path TEXT NOT NULL,
		to_path   TEXT NOT NULL,
		PRIMARY KEY (scope, scope_id, user_id, from_path, to_path)
	);
	CREATE INDEX IF NOT EXISTS idx_memory_links_to
		ON memory_links(scope, scope_id, user_id, to_path);

	CREATE INDEX IF NOT EXISTS idx_events_process ON events(process_id);
	CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_snapshots_process ON process_snapshots(process_id);
	CREATE INDEX IF NOT EXISTS idx_snapshots_at ON process_snapshots(snapshot_at);
	CREATE INDEX IF NOT EXISTS idx_chat_created ON chat_messages(created_at);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_id ON workflow_runs(run_id);
	CREATE INDEX IF NOT EXISTS idx_chat_agent ON chat_messages(agent);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Peering tables for orchestrator-to-orchestrator federation. Kept in
	// the peering package so DDL lives next to the access methods (and the
	// Postgres goose migration in serve/migrations/postgres/00004_peering.sql).
	if err := peering.ApplySQLiteSchema(s.db); err != nil {
		return fmt.Errorf("apply peering schema: %w", err)
	}

	// Migrate: add tools column to composed_agents if missing (added after initial release).
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN tools TEXT NOT NULL DEFAULT '[]'`)

	// Migrate: add disabled column to mcp_servers if missing.
	s.db.Exec(`ALTER TABLE mcp_servers ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`)

	// Migrate: add display_name and title columns to composed_agents if missing.
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`)
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN title TEXT NOT NULL DEFAULT ''`)

	// Migrate: add avatar column to composed_agents if missing.
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`)

	// Migrate: add visual identity (icon, avatar_gradient) and updated_at
	// columns to composed_agents. Backfill updated_at from created_at so
	// existing rows surface a sensible "last touched" timestamp.
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN icon TEXT NOT NULL DEFAULT ''`)
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN avatar_gradient TEXT NOT NULL DEFAULT '[]'`)
	if _, err := s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN updated_at DATETIME`); err == nil {
		s.db.Exec(`UPDATE composed_agents SET updated_at = created_at WHERE updated_at IS NULL`)
	}

	// Migrate: add description column for the user-facing body text.
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN description TEXT NOT NULL DEFAULT ''`)

	// Migrate: add reactive triggers column (reactive-agents Phase 2).
	s.db.Exec(`ALTER TABLE composed_agents ADD COLUMN triggers TEXT NOT NULL DEFAULT '[]'`)

	// Migrate: add tool_activities column to chat_messages + channel_messages.
	// Stores a JSON array of completed tool calls captured during the
	// streaming turn, so loaded history reproduces the live timeline.
	s.db.Exec(`ALTER TABLE chat_messages ADD COLUMN tool_activities TEXT NOT NULL DEFAULT '[]'`)
	s.db.Exec(`ALTER TABLE channel_messages ADD COLUMN tool_activities TEXT NOT NULL DEFAULT '[]'`)

	// Collapse legacy per-user clone chat rows (agent='base:userID') back
	// into their base agent's timeline. Vega is single-user-per-bot now —
	// one Telegram chat, one web UI, one timeline.
	if err := s.collapseCloneChatMessages(); err != nil {
		return fmt.Errorf("collapse clone chat messages: %w", err)
	}

	// Migrate: add mode column to channels if missing.
	s.db.Exec(`ALTER TABLE channels ADD COLUMN mode TEXT NOT NULL DEFAULT ''`)

	// Migrate: add updated_at column. Backfill from created_at so existing
	// rows surface a sensible value rather than null/epoch.
	if _, err := s.db.Exec(`ALTER TABLE channels ADD COLUMN updated_at DATETIME`); err == nil {
		s.db.Exec(`UPDATE channels SET updated_at = created_at WHERE updated_at IS NULL`)
	}

	// Migrate: add sender column to channel_messages for multi-user identity.
	s.db.Exec(`ALTER TABLE channel_messages ADD COLUMN sender TEXT DEFAULT ''`)

	// Migrate: add triage_count + last_triaged_at to agent_inbox. The
	// triage_count is incremented every time the orchestrator's list_inbox
	// tool returns the item — after N reads without a resolution the
	// store auto-ages the item to status='resolved' so the orchestrator
	// stops paying token cost on items it can't decide. See
	// TriageInboxItems in this package.
	s.db.Exec(`ALTER TABLE agent_inbox ADD COLUMN triage_count INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE agent_inbox ADD COLUMN last_triaged_at DATETIME`)

	// Agent budgets — per-agent monthly cap + enforcement state
	// (refs govega#47). period_start/period_end aren't stored — period
	// is always the current calendar month UTC per Cody's spec, so
	// they're derived at read time. observed_spend is the sum over
	// process_snapshots; not stored either.
	s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_budgets (
		agent_name           TEXT PRIMARY KEY,
		budget_cap           REAL,
		soft_alert_threshold REAL NOT NULL DEFAULT 0.8,
		enabled              BOOLEAN NOT NULL DEFAULT 0,
		created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)

	// Agent brain — per-agent knowledge attachments (refs govega#43).
	// Stored as SQLite blobs because the MVP is a pure attachment list
	// (no RAG indexing yet); volumes are small enough that the simplicity
	// of one table beats an object-store dependency.
	s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_brain_files (
		id          TEXT PRIMARY KEY,
		agent_name  TEXT NOT NULL,
		name        TEXT NOT NULL,
		mime_type   TEXT NOT NULL DEFAULT '',
		size_bytes  INTEGER NOT NULL DEFAULT 0,
		content     BLOB NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_brain_files_agent ON agent_brain_files(agent_name)`)

	// Migrate: extend scheduled_jobs to carry routine identity (refs
	// govega#52). `id` becomes the stable key returned to the FE; `name`
	// stays as the routing/lookup key DSL tools use. For legacy rows
	// (created before this migration) we backfill id = name so the FE
	// can still address them.
	s.db.Exec(`ALTER TABLE scheduled_jobs ADD COLUMN id TEXT NOT NULL DEFAULT ''`)
	s.db.Exec(`ALTER TABLE scheduled_jobs ADD COLUMN title TEXT NOT NULL DEFAULT ''`)
	s.db.Exec(`ALTER TABLE scheduled_jobs ADD COLUMN schedule_json TEXT NOT NULL DEFAULT ''`)
	s.db.Exec(`ALTER TABLE scheduled_jobs ADD COLUMN last_run_at DATETIME`)
	if _, err := s.db.Exec(`ALTER TABLE scheduled_jobs ADD COLUMN updated_at DATETIME`); err == nil {
		s.db.Exec(`UPDATE scheduled_jobs SET updated_at = created_at WHERE updated_at IS NULL`)
	}
	// Backfill id from name for legacy rows so the new GET-by-id path
	// resolves them.
	s.db.Exec(`UPDATE scheduled_jobs SET id = name WHERE id = ''`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_agent ON scheduled_jobs(agent_name)`)
	s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduled_jobs_id ON scheduled_jobs(id)`)

	// Migrate: add type column to memory_items so existing rows fall back
	// to the catch-all 'reference' type instead of an empty string.
	s.db.Exec(`ALTER TABLE memory_items ADD COLUMN type TEXT NOT NULL DEFAULT 'reference'`)
	// The dedup index is created in the CREATE TABLE block above for fresh
	// databases; CREATE IF NOT EXISTS handles the upgrade case.
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_items_dedup ON memory_items(user_id, agent, type, content)`)

	if err := s.initTaskSchema(); err != nil {
		return err
	}

	return nil
}

// Close closes the database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// parseSQLiteTime parses a datetime string returned by modernc.org/sqlite
// from a subquery (where column type info is lost). SQLite stores
// DATETIME columns as ISO 8601-ish text; the driver returns the raw
// text rather than a time.Time when the destination is sql.NullTime,
// hence this manual parse. Handles the most common formats with and
// without microseconds and zone.
func parseSQLiteTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse SQLite datetime %q", s)
}

// InsertEvent records an orchestration event.
func (s *SQLiteStore) InsertEvent(e StoreEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO events (type, process_id, agent_name, timestamp, data, result, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Type, e.ProcessID, e.AgentName, e.Timestamp, e.Data, e.Result, e.Error,
	)
	return err
}

// InsertProcessSnapshot records a process state snapshot.
func (s *SQLiteStore) InsertProcessSnapshot(snap ProcessSnapshot) error {
	_, err := s.db.Exec(
		`INSERT INTO process_snapshots
		 (process_id, agent_name, status, parent_id, input_tokens, output_tokens, cost_usd, started_at, completed_at, snapshot_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.ProcessID, snap.AgentName, snap.Status, snap.ParentID,
		snap.InputTokens, snap.OutputTokens, snap.CostUSD,
		snap.StartedAt, snap.CompletedAt, snap.SnapshotAt,
	)
	return err
}

// InsertWorkflowRun records a workflow execution.
func (s *SQLiteStore) InsertWorkflowRun(r WorkflowRun) error {
	_, err := s.db.Exec(
		`INSERT INTO workflow_runs (run_id, workflow, inputs, status, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		r.RunID, r.Workflow, r.Inputs, r.Status, r.StartedAt,
	)
	return err
}

// UpdateWorkflowRun updates a workflow run status and result.
func (s *SQLiteStore) UpdateWorkflowRun(runID string, status string, result string) error {
	_, err := s.db.Exec(
		`UPDATE workflow_runs SET status = ?, result = ? WHERE run_id = ?`,
		status, result, runID,
	)
	return err
}

// ListEvents returns recent events, newest first.
func (s *SQLiteStore) ListEvents(limit int) ([]StoreEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, type, process_id, agent_name, timestamp, data, result, error
		 FROM events ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []StoreEvent
	for rows.Next() {
		var e StoreEvent
		if err := rows.Scan(&e.ID, &e.Type, &e.ProcessID, &e.AgentName, &e.Timestamp, &e.Data, &e.Result, &e.Error); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ListProcessSnapshots returns the latest snapshot per process.
func (s *SQLiteStore) ListProcessSnapshots() ([]ProcessSnapshot, error) {
	rows, err := s.db.Query(
		`SELECT ps.id, ps.process_id, ps.agent_name, ps.status, ps.parent_id,
		        ps.input_tokens, ps.output_tokens, ps.cost_usd,
		        ps.started_at, ps.completed_at, ps.snapshot_at
		 FROM process_snapshots ps
		 INNER JOIN (
		   SELECT process_id, MAX(id) as max_id FROM process_snapshots GROUP BY process_id
		 ) latest ON ps.id = latest.max_id
		 ORDER BY ps.started_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []ProcessSnapshot
	for rows.Next() {
		var snap ProcessSnapshot
		var completedAt sql.NullTime
		if err := rows.Scan(
			&snap.ID, &snap.ProcessID, &snap.AgentName, &snap.Status, &snap.ParentID,
			&snap.InputTokens, &snap.OutputTokens, &snap.CostUSD,
			&snap.StartedAt, &completedAt, &snap.SnapshotAt,
		); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			snap.CompletedAt = &completedAt.Time
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}

// ListWorkflowRuns returns recent workflow runs.
func (s *SQLiteStore) ListWorkflowRuns(limit int) ([]WorkflowRun, error) {
	rows, err := s.db.Query(
		`SELECT id, run_id, workflow, inputs, status, result, started_at
		 FROM workflow_runs ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []WorkflowRun
	for rows.Next() {
		var r WorkflowRun
		if err := rows.Scan(&r.ID, &r.RunID, &r.Workflow, &r.Inputs, &r.Status, &r.Result, &r.StartedAt); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// InsertComposedAgent persists a composed agent definition. CreatedAt is
// preserved on update (UPSERT path); UpdatedAt is set to NOW if zero so
// callers can omit it on the happy path.
func (s *SQLiteStore) InsertComposedAgent(a ComposedAgent) error {
	skillsJSON, _ := json.Marshal(a.Skills)
	toolsJSON, _ := json.Marshal(a.Tools)
	teamJSON, _ := json.Marshal(a.Team)
	gradJSON, _ := json.Marshal(a.AvatarGradient)
	triggersJSON, _ := json.Marshal(a.Triggers)
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO composed_agents (name, display_name, title, description, avatar, icon, avatar_gradient, model, persona, skills, tools, team, system, temperature, triggers, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   display_name    = excluded.display_name,
		   title           = excluded.title,
		   description     = excluded.description,
		   avatar          = excluded.avatar,
		   icon            = excluded.icon,
		   avatar_gradient = excluded.avatar_gradient,
		   model           = excluded.model,
		   persona         = excluded.persona,
		   skills          = excluded.skills,
		   tools           = excluded.tools,
		   team            = excluded.team,
		   system          = excluded.system,
		   temperature     = excluded.temperature,
		   triggers        = excluded.triggers,
		   updated_at      = excluded.updated_at`,
		a.Name, a.DisplayName, a.Title, a.Description, a.Avatar, a.Icon, string(gradJSON), a.Model, a.Persona, string(skillsJSON), string(toolsJSON), string(teamJSON), a.System, a.Temperature, string(triggersJSON), a.CreatedAt, a.UpdatedAt,
	)
	return err
}

// ListComposedAgents returns all composed agents.
func (s *SQLiteStore) ListComposedAgents() ([]ComposedAgent, error) {
	rows, err := s.db.Query(
		`SELECT name, display_name, title, description, avatar, icon, avatar_gradient, model, persona, skills, tools, team, system, temperature, triggers, created_at, updated_at
		 FROM composed_agents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []ComposedAgent
	for rows.Next() {
		var a ComposedAgent
		var skillsJSON, toolsJSON, teamJSON, gradJSON, triggersJSON string
		var temp sql.NullFloat64
		var updatedAt sql.NullTime
		if err := rows.Scan(&a.Name, &a.DisplayName, &a.Title, &a.Description, &a.Avatar, &a.Icon, &gradJSON, &a.Model, &a.Persona, &skillsJSON, &toolsJSON, &teamJSON, &a.System, &temp, &triggersJSON, &a.CreatedAt, &updatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(skillsJSON), &a.Skills)
		json.Unmarshal([]byte(toolsJSON), &a.Tools)
		json.Unmarshal([]byte(teamJSON), &a.Team)
		json.Unmarshal([]byte(gradJSON), &a.AvatarGradient)
		json.Unmarshal([]byte(triggersJSON), &a.Triggers)
		if temp.Valid {
			a.Temperature = &temp.Float64
		}
		if updatedAt.Valid {
			a.UpdatedAt = updatedAt.Time
		} else {
			// Backfill: pre-migration rows have no updated_at.
			a.UpdatedAt = a.CreatedAt
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// DeleteComposedAgent removes a composed agent by name.
func (s *SQLiteStore) DeleteComposedAgent(name string) error {
	result, err := s.db.Exec(`DELETE FROM composed_agents WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// InsertChatMessage persists a chat message for an agent. Pass nil
// activities for user messages or for assistant messages with no tool
// calls. The streaming path passes the result of CollectToolActivities
// so reloaded history reproduces the live tool-call timeline.
func (s *SQLiteStore) InsertChatMessage(agent, role, content string, activities []vega.ToolActivity) error {
	activitiesJSON := []byte("[]")
	if len(activities) > 0 {
		if b, err := json.Marshal(activities); err == nil {
			activitiesJSON = b
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO chat_messages (agent, role, content, tool_activities) VALUES (?, ?, ?, ?)`,
		agent, role, content, string(activitiesJSON),
	)
	return err
}

// ListChatMessages returns all chat messages for an agent, oldest first.
func (s *SQLiteStore) ListChatMessages(agent string) ([]ChatMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, tool_activities, created_at FROM chat_messages WHERE agent = ? ORDER BY id ASC`, agent,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var activitiesJSON string
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &activitiesJSON, &m.CreatedAt); err != nil {
			return nil, err
		}
		if activitiesJSON != "" && activitiesJSON != "[]" {
			_ = json.Unmarshal([]byte(activitiesJSON), &m.ToolActivities)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteChatMessages removes all chat messages for an agent.
func (s *SQLiteStore) DeleteChatMessages(agent string) error {
	_, err := s.db.Exec(`DELETE FROM chat_messages WHERE agent = ?`, agent)
	return err
}

// collapseCloneChatMessages folds rows written under the retired per-user
// clone naming scheme (agent='base:userID') back into the base agent's
// history (agent='base'). Vega is now single-user-per-bot: one Telegram
// account talks to one agent, the same agent the web UI talks to, and
// there is no clone fan-out. Idempotent — rows without a ':' in agent are
// left alone.
func (s *SQLiteStore) collapseCloneChatMessages() error {
	_, err := s.db.Exec(`
		UPDATE chat_messages
		SET agent = substr(agent, 1, instr(agent, ':') - 1)
		WHERE instr(agent, ':') > 0
	`)
	return err
}

// UpsertUserMemory creates or replaces a memory layer for a user+agent.
func (s *SQLiteStore) UpsertUserMemory(userID, agent, layer, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_memory (user_id, agent, layer, content, created_at, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id, agent, layer)
		 DO UPDATE SET content = excluded.content, updated_at = CURRENT_TIMESTAMP`,
		userID, agent, layer, content,
	)
	return err
}

// GetUserMemory returns all memory layers for a user+agent.
func (s *SQLiteStore) GetUserMemory(userID, agent string) ([]UserMemory, error) {
	rows, err := s.db.Query(
		`SELECT user_id, agent, layer, content, created_at, updated_at
		 FROM user_memory WHERE user_id = ? AND agent = ? ORDER BY layer ASC`,
		userID, agent,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []UserMemory
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.UserID, &m.Agent, &m.Layer, &m.Content, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// DeleteUserMemory removes all memory for a user+agent.
func (s *SQLiteStore) DeleteUserMemory(userID, agent string) error {
	_, err := s.db.Exec(`DELETE FROM user_memory WHERE user_id = ? AND agent = ?`, userID, agent)
	return err
}

// ListAllUserMemory returns every row in user_memory. Migration helper
// (govega#71). Iteration order is (user_id, agent, layer) ascending so
// the migration's grouping logic sees adjacent rows.
func (s *SQLiteStore) ListAllUserMemory() ([]UserMemory, error) {
	rows, err := s.db.Query(
		`SELECT user_id, agent, layer, content, created_at, updated_at
		 FROM user_memory ORDER BY user_id, agent, layer`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserMemory{}
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.UserID, &m.Agent, &m.Layer, &m.Content, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListAllMemoryItems returns every row in memory_items. Migration
// helper (govega#71). Ordered by (user_id, agent, topic, created_at).
func (s *SQLiteStore) ListAllMemoryItems() ([]MemoryItem, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items ORDER BY user_id, agent, topic, created_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryItem{}
	for rows.Next() {
		var m MemoryItem
		var typeStr string
		if err := rows.Scan(&m.ID, &m.UserID, &m.Agent, &typeStr, &m.Topic, &m.Content, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Type = MemoryType(typeStr)
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Wiki memory (govega#71) ---

// UpsertMemoryPage inserts a page if absent, or replaces Content +
// Frontmatter + advances updated_at on conflict. CreatedAt is preserved
// across updates by leaving created_at out of the DO UPDATE clause.
func (s *SQLiteStore) UpsertMemoryPage(p MemoryPage) error {
	_, err := s.db.Exec(
		`INSERT INTO memory_pages (scope, scope_id, user_id, path, content, frontmatter, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(scope, scope_id, user_id, path)
		 DO UPDATE SET
		   content = excluded.content,
		   frontmatter = excluded.frontmatter,
		   updated_at = CURRENT_TIMESTAMP`,
		string(p.Scope), p.ScopeID, p.UserID, p.Path, p.Content, p.Frontmatter,
	)
	return err
}

// GetMemoryPage returns one page, or nil when not found.
func (s *SQLiteStore) GetMemoryPage(scope MemoryScope, scopeID, userID, path string) (*MemoryPage, error) {
	row := s.db.QueryRow(
		`SELECT scope, scope_id, user_id, path, content, frontmatter, created_at, updated_at
		 FROM memory_pages
		 WHERE scope = ? AND scope_id = ? AND user_id = ? AND path = ?`,
		string(scope), scopeID, userID, path,
	)
	var p MemoryPage
	var scopeStr string
	if err := row.Scan(&scopeStr, &p.ScopeID, &p.UserID, &p.Path, &p.Content, &p.Frontmatter, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	p.Scope = MemoryScope(scopeStr)
	return &p, nil
}

// ListMemoryPages returns every page under (scope, scopeID, userID),
// optionally filtered by path prefix. Ordered by updated_at DESC.
func (s *SQLiteStore) ListMemoryPages(scope MemoryScope, scopeID, userID, pathPrefix string) ([]MemoryPage, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if pathPrefix == "" {
		rows, err = s.db.Query(
			`SELECT scope, scope_id, user_id, path, content, frontmatter, created_at, updated_at
			 FROM memory_pages
			 WHERE scope = ? AND scope_id = ? AND user_id = ?
			 ORDER BY updated_at DESC`,
			string(scope), scopeID, userID,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT scope, scope_id, user_id, path, content, frontmatter, created_at, updated_at
			 FROM memory_pages
			 WHERE scope = ? AND scope_id = ? AND user_id = ? AND path LIKE ? || '%'
			 ORDER BY updated_at DESC`,
			string(scope), scopeID, userID, pathPrefix,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MemoryPage{}
	for rows.Next() {
		var p MemoryPage
		var scopeStr string
		if err := rows.Scan(&scopeStr, &p.ScopeID, &p.UserID, &p.Path, &p.Content, &p.Frontmatter, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Scope = MemoryScope(scopeStr)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteMemoryPage removes one page and cascades through memory_links
// (rows where the page appears as from_path or to_path are also dropped).
// Missing page is a no-op.
func (s *SQLiteStore) DeleteMemoryPage(scope MemoryScope, scopeID, userID, path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`DELETE FROM memory_pages WHERE scope = ? AND scope_id = ? AND user_id = ? AND path = ?`,
		string(scope), scopeID, userID, path,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM memory_links WHERE scope = ? AND scope_id = ? AND user_id = ? AND (from_path = ? OR to_path = ?)`,
		string(scope), scopeID, userID, path, path,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameMemoryPage moves a page from oldPath to newPath and rewrites
// every link where oldPath appears as from_path or to_path. Atomic.
//
// Note: newPath must not already exist — this fails the page insert at
// the unique constraint. Callers who want overwrite semantics should
// delete the destination first.
func (s *SQLiteStore) RenameMemoryPage(scope MemoryScope, scopeID, userID, oldPath, newPath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE memory_pages SET path = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE scope = ? AND scope_id = ? AND user_id = ? AND path = ?`,
		newPath, string(scope), scopeID, userID, oldPath,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE memory_links SET from_path = ?
		 WHERE scope = ? AND scope_id = ? AND user_id = ? AND from_path = ?`,
		newPath, string(scope), scopeID, userID, oldPath,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE memory_links SET to_path = ?
		 WHERE scope = ? AND scope_id = ? AND user_id = ? AND to_path = ?`,
		newPath, string(scope), scopeID, userID, oldPath,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// SearchMemoryPages does a case-insensitive substring search across
// path and content, ranked by updated_at DESC.
func (s *SQLiteStore) SearchMemoryPages(scope MemoryScope, scopeID, userID, query string, limit int) ([]MemoryPage, error) {
	if limit <= 0 {
		limit = 25
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT scope, scope_id, user_id, path, content, frontmatter, created_at, updated_at
		 FROM memory_pages
		 WHERE scope = ? AND scope_id = ? AND user_id = ?
		   AND (LOWER(path) LIKE LOWER(?) OR LOWER(content) LIKE LOWER(?))
		 ORDER BY updated_at DESC
		 LIMIT ?`,
		string(scope), scopeID, userID, pattern, pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryPage{}
	for rows.Next() {
		var p MemoryPage
		var scopeStr string
		if err := rows.Scan(&scopeStr, &p.ScopeID, &p.UserID, &p.Path, &p.Content, &p.Frontmatter, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Scope = MemoryScope(scopeStr)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ReplaceMemoryLinks atomically replaces the out-edges from fromPath
// with one row per (fromPath, to) in toPaths. Empty toPaths clears.
func (s *SQLiteStore) ReplaceMemoryLinks(scope MemoryScope, scopeID, userID, fromPath string, toPaths []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`DELETE FROM memory_links WHERE scope = ? AND scope_id = ? AND user_id = ? AND from_path = ?`,
		string(scope), scopeID, userID, fromPath,
	); err != nil {
		return err
	}
	for _, to := range toPaths {
		if to == "" || to == fromPath {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO memory_links (scope, scope_id, user_id, from_path, to_path)
			 VALUES (?, ?, ?, ?, ?)`,
			string(scope), scopeID, userID, fromPath, to,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListMemoryLinks returns every link under (scope, scopeID, userID).
func (s *SQLiteStore) ListMemoryLinks(scope MemoryScope, scopeID, userID string) ([]MemoryLink, error) {
	rows, err := s.db.Query(
		`SELECT scope, scope_id, user_id, from_path, to_path
		 FROM memory_links
		 WHERE scope = ? AND scope_id = ? AND user_id = ?`,
		string(scope), scopeID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryLink{}
	for rows.Next() {
		var l MemoryLink
		var scopeStr string
		if err := rows.Scan(&scopeStr, &l.ScopeID, &l.UserID, &l.FromPath, &l.ToPath); err != nil {
			return nil, err
		}
		l.Scope = MemoryScope(scopeStr)
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListMemoryScopeIDs returns every distinct scope_id under (scope, userID),
// ordered alphabetically. Used to enumerate agent wikis for a user.
func (s *SQLiteStore) ListMemoryScopeIDs(scope MemoryScope, userID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT scope_id FROM memory_pages
		 WHERE scope = ? AND user_id = ?
		 ORDER BY scope_id ASC`,
		string(scope), userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpsertScheduledJob creates or replaces a scheduled job. The Name field
// is the legacy primary key (cron runner + DSL lookup); ID defaults to
// Name for backwards compatibility when callers don't supply it.
func (s *SQLiteStore) UpsertScheduledJob(job ScheduledJob) error {
	if job.ID == "" {
		job.ID = job.Name
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO scheduled_jobs (
			id, name, title, cron, agent_name, message, schedule_json, enabled,
			last_run_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(
		    (SELECT created_at FROM scheduled_jobs WHERE name = ?),
		    CURRENT_TIMESTAMP
		 ), CURRENT_TIMESTAMP)`,
		job.ID, job.Name, job.Title, job.Cron, job.AgentName, job.Message,
		job.ScheduleJSON, job.Enabled, job.LastRunAt, job.Name,
	)
	return err
}

// DeleteScheduledJob removes a scheduled job by name (the cron-runner key).
func (s *SQLiteStore) DeleteScheduledJob(name string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_jobs WHERE name = ?`, name)
	return err
}

// GetScheduledJobByID returns one job by its server-generated id (or by
// name for legacy rows). Returns nil, nil when the row doesn't exist.
func (s *SQLiteStore) GetScheduledJobByID(id string) (*ScheduledJob, error) {
	row := s.db.QueryRow(
		`SELECT id, name, title, cron, agent_name, message, schedule_json,
		        enabled, last_run_at, created_at, updated_at
		 FROM scheduled_jobs WHERE id = ?`, id,
	)
	var j ScheduledJob
	var lastRun sql.NullTime
	var updated sql.NullTime
	if err := row.Scan(&j.ID, &j.Name, &j.Title, &j.Cron, &j.AgentName,
		&j.Message, &j.ScheduleJSON, &j.Enabled, &lastRun, &j.CreatedAt, &updated); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if lastRun.Valid {
		t := lastRun.Time
		j.LastRunAt = &t
	}
	if updated.Valid {
		j.UpdatedAt = updated.Time
	}
	return &j, nil
}

// ListScheduledJobs returns all scheduled jobs.
func (s *SQLiteStore) ListScheduledJobs() ([]ScheduledJob, error) {
	rows, err := s.db.Query(
		`SELECT id, name, title, cron, agent_name, message, schedule_json,
		        enabled, last_run_at, created_at, updated_at
		 FROM scheduled_jobs ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ScheduledJob
	for rows.Next() {
		var j ScheduledJob
		var lastRun sql.NullTime
		var updated sql.NullTime
		if err := rows.Scan(&j.ID, &j.Name, &j.Title, &j.Cron, &j.AgentName,
			&j.Message, &j.ScheduleJSON, &j.Enabled, &lastRun, &j.CreatedAt, &updated); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			t := lastRun.Time
			j.LastRunAt = &t
		}
		if updated.Valid {
			j.UpdatedAt = updated.Time
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// SearchEvents matches the activity-log query against the events table
// (refs govega#33). Search is a case-insensitive substring scan over
// type, agent_name, data, result, and error — LIKE-based rather than
// FTS5 to keep the schema migration cost down. At today's data volumes
// the index scan on (timestamp, agent_name) plus a per-row LIKE pass is
// well within the latency budget; the API shape is the same once FTS5
// lands.
func (s *SQLiteStore) SearchEvents(filter ActivityFilter) ([]StoreEvent, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if q := strings.TrimSpace(filter.Query); q != "" {
		where = append(where, `(
			LOWER(type)       LIKE ? OR
			LOWER(agent_name) LIKE ? OR
			LOWER(data)       LIKE ? OR
			LOWER(result)     LIKE ? OR
			LOWER(error)      LIKE ?
		)`)
		pat := "%" + strings.ToLower(q) + "%"
		args = append(args, pat, pat, pat, pat, pat)
	}
	if filter.Type != "" {
		where = append(where, "type = ?")
		args = append(args, filter.Type)
	}
	if filter.Agent != "" {
		where = append(where, "agent_name = ?")
		args = append(args, filter.Agent)
	}
	if !filter.From.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, filter.From)
	}
	if !filter.To.IsZero() {
		where = append(where, "timestamp < ?")
		args = append(args, filter.To)
	}
	whereClause := strings.Join(where, " AND ")

	// Total count over the same filter — single round-trip, no need to
	// materialize all matching rows.
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE `+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	pageArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := s.db.Query(`
SELECT id, type, process_id, agent_name, timestamp, data, result, error
FROM events
WHERE `+whereClause+`
ORDER BY id DESC
LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []StoreEvent
	for rows.Next() {
		var e StoreEvent
		if err := rows.Scan(&e.ID, &e.Type, &e.ProcessID, &e.AgentName, &e.Timestamp, &e.Data, &e.Result, &e.Error); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// GetAgentBudget returns the persisted budget row for agentName, or
// (nil, nil) when the agent has no row. Refs govega#47.
func (s *SQLiteStore) GetAgentBudget(agentName string) (*AgentBudget, error) {
	row := s.db.QueryRow(
		`SELECT agent_name, budget_cap, soft_alert_threshold, enabled, created_at, updated_at
		 FROM agent_budgets WHERE agent_name = ?`, agentName,
	)
	var b AgentBudget
	var cap sql.NullFloat64
	if err := row.Scan(&b.AgentName, &cap, &b.SoftAlertThreshold, &b.Enabled, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if cap.Valid {
		v := cap.Float64
		b.BudgetCap = &v
	}
	return &b, nil
}

// UpsertAgentBudget creates or replaces a budget row. created_at is
// preserved on update via COALESCE so the field stays meaningful.
func (s *SQLiteStore) UpsertAgentBudget(b AgentBudget) error {
	var cap any
	if b.BudgetCap != nil {
		cap = *b.BudgetCap
	}
	_, err := s.db.Exec(
		`INSERT INTO agent_budgets
		 (agent_name, budget_cap, soft_alert_threshold, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, COALESCE(
		   (SELECT created_at FROM agent_budgets WHERE agent_name = ?),
		   CURRENT_TIMESTAMP
		 ), CURRENT_TIMESTAMP)
		 ON CONFLICT(agent_name) DO UPDATE SET
		   budget_cap           = excluded.budget_cap,
		   soft_alert_threshold = excluded.soft_alert_threshold,
		   enabled              = excluded.enabled,
		   updated_at           = CURRENT_TIMESTAMP`,
		b.AgentName, cap, b.SoftAlertThreshold, b.Enabled, b.AgentName,
	)
	return err
}

// AgentSpendInPeriod sums cost_usd across the latest snapshot of every
// process for agentName, optionally bounded by [from, to). Zero from/to
// is treated as unbounded on that side. Used by the per-agent spend
// rollup endpoint (refs govega#47) so the FE doesn't have to fan out
// /processes per agent to render the observed_spend column.
func (s *SQLiteStore) AgentSpendInPeriod(agentName string, from, to time.Time) (float64, error) {
	q := `
SELECT COALESCE(SUM(cost_usd), 0)
FROM process_snapshots ps
JOIN (
    SELECT process_id, MAX(id) AS max_id
    FROM process_snapshots
    WHERE agent_name = ?
    GROUP BY process_id
) latest ON ps.id = latest.max_id`
	args := []any{agentName}
	if !from.IsZero() {
		q += ` WHERE COALESCE(ps.started_at, ps.snapshot_at) >= ?`
		args = append(args, from)
		if !to.IsZero() {
			q += ` AND COALESCE(ps.started_at, ps.snapshot_at) < ?`
			args = append(args, to)
		}
	} else if !to.IsZero() {
		q += ` WHERE COALESCE(ps.started_at, ps.snapshot_at) < ?`
		args = append(args, to)
	}
	var total float64
	if err := s.db.QueryRow(q, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// InsertAgentBrainFile persists an agent-scoped knowledge attachment
// (refs govega#43). Content is stored inline; callers must enforce size
// limits before invoking.
func (s *SQLiteStore) InsertAgentBrainFile(f AgentBrainFile) error {
	_, err := s.db.Exec(
		`INSERT INTO agent_brain_files (id, agent_name, name, mime_type, size_bytes, content, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		f.ID, f.AgentName, f.Name, f.MimeType, f.SizeBytes, f.Content,
	)
	return err
}

// ListAgentBrainFiles returns metadata only (no content) for every
// brain file on agentName, oldest first.
func (s *SQLiteStore) ListAgentBrainFiles(agentName string) ([]AgentBrainFile, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_name, name, mime_type, size_bytes, created_at
		 FROM agent_brain_files WHERE agent_name = ? ORDER BY created_at ASC`,
		agentName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentBrainFile
	for rows.Next() {
		var f AgentBrainFile
		if err := rows.Scan(&f.ID, &f.AgentName, &f.Name, &f.MimeType, &f.SizeBytes, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetAgentBrainFile returns one file (content included) when it belongs
// to agentName; returns nil, nil otherwise.
func (s *SQLiteStore) GetAgentBrainFile(agentName, id string) (*AgentBrainFile, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_name, name, mime_type, size_bytes, content, created_at
		 FROM agent_brain_files WHERE id = ? AND agent_name = ?`,
		id, agentName,
	)
	var f AgentBrainFile
	if err := row.Scan(&f.ID, &f.AgentName, &f.Name, &f.MimeType, &f.SizeBytes, &f.Content, &f.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// DeleteAgentBrainFile removes a brain file from agentName. Returns
// sql.ErrNoRows when the file doesn't exist on the agent.
func (s *SQLiteStore) DeleteAgentBrainFile(agentName, id string) error {
	res, err := s.db.Exec(
		`DELETE FROM agent_brain_files WHERE id = ? AND agent_name = ?`,
		id, agentName,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MarkScheduledJobRun stamps last_run_at = now for the given job name.
// Best-effort — a failure just means the next-run computation will be
// slightly stale; the cron runner is the source of truth for firing.
func (s *SQLiteStore) MarkScheduledJobRun(name string, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE scheduled_jobs SET last_run_at = ? WHERE name = ?`,
		at, name,
	)
	return err
}

// InsertMemoryItem saves a memory item and returns its ID. If a row already
// exists with the same (user_id, agent, type, content), its tags are merged
// (union of comma-separated values) and updated_at advances rather than
// inserting a duplicate. Items missing a Type are stored as
// MemoryTypeReference so legacy callers continue to work.
func (s *SQLiteStore) InsertMemoryItem(item MemoryItem) (int64, error) {
	if item.Type == "" {
		item.Type = MemoryTypeReference
	}

	// Look for an existing row with the same dedup key.
	var (
		existingID   int64
		existingTags string
	)
	err := s.db.QueryRow(
		`SELECT id, tags FROM memory_items
		 WHERE user_id = ? AND agent = ? AND type = ? AND content = ?
		 LIMIT 1`,
		item.UserID, item.Agent, string(item.Type), item.Content,
	).Scan(&existingID, &existingTags)

	switch {
	case err == nil:
		// Existing row — merge tags and bump updated_at.
		merged := mergeTags(existingTags, item.Tags)
		if _, err := s.db.Exec(
			`UPDATE memory_items
			 SET tags = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?`,
			merged, existingID,
		); err != nil {
			return 0, err
		}
		return existingID, nil
	case err == sql.ErrNoRows:
		// Fall through to insert.
	default:
		return 0, err
	}

	result, err := s.db.Exec(
		`INSERT INTO memory_items (user_id, agent, type, topic, content, tags)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		item.UserID, item.Agent, string(item.Type), item.Topic, item.Content, item.Tags,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// SearchMemoryItems searches memory items by keyword via LIKE across topic, content, and tags.
func (s *SQLiteStore) SearchMemoryItems(userID, agent, query string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items
		 WHERE user_id = ? AND agent = ?
		   AND (topic LIKE ? OR content LIKE ? OR tags LIKE ?)
		 ORDER BY updated_at DESC LIMIT ?`,
		userID, agent, pattern, pattern, pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MemoryItem
	for rows.Next() {
		var m MemoryItem
		if err := rows.Scan(&m.ID, &m.UserID, &m.Agent, &m.Type, &m.Topic, &m.Content, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// SearchMemoryItemsByType is SearchMemoryItems narrowed to a single MemoryType.
func (s *SQLiteStore) SearchMemoryItemsByType(userID, agent, query string, typ MemoryType, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items
		 WHERE user_id = ? AND agent = ? AND type = ?
		   AND (topic LIKE ? OR content LIKE ? OR tags LIKE ?)
		 ORDER BY updated_at DESC LIMIT ?`,
		userID, agent, string(typ), pattern, pattern, pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MemoryItem
	for rows.Next() {
		var m MemoryItem
		if err := rows.Scan(&m.ID, &m.UserID, &m.Agent, &m.Type, &m.Topic, &m.Content, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// mergeTags returns the comma-separated union of two tag lists, preserving
// the order of first appearance and skipping empty entries.
func mergeTags(a, b string) string {
	seen := make(map[string]struct{})
	var out []string
	add := func(raw string) {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	add(a)
	add(b)
	return strings.Join(out, ",")
}

// DeleteMemoryItem removes a memory item by ID.
func (s *SQLiteStore) DeleteMemoryItem(id int64) error {
	result, err := s.db.Exec(`DELETE FROM memory_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListMemoryItemsByTopic returns memory items for a given user+agent+topic.
func (s *SQLiteStore) ListMemoryItemsByTopic(userID, agent, topic string) ([]MemoryItem, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items
		 WHERE user_id = ? AND agent = ? AND topic = ?
		 ORDER BY created_at ASC`,
		userID, agent, topic,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MemoryItem
	for rows.Next() {
		var m MemoryItem
		if err := rows.Scan(&m.ID, &m.UserID, &m.Agent, &m.Type, &m.Topic, &m.Content, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// InsertWorkspaceFile records a file write by an agent.
func (s *SQLiteStore) InsertWorkspaceFile(f WorkspaceFile) error {
	_, err := s.db.Exec(
		`INSERT INTO workspace_files (path, agent, process_id, operation, description)
		 VALUES (?, ?, ?, ?, ?)`,
		f.Path, f.Agent, f.ProcessID, f.Operation, f.Description,
	)
	return err
}

// ListWorkspaceFiles returns workspace file records, optionally filtered by agent.
func (s *SQLiteStore) ListWorkspaceFiles(agent string) ([]WorkspaceFile, error) {
	var rows *sql.Rows
	var err error
	if agent != "" {
		rows, err = s.db.Query(
			`SELECT id, path, agent, process_id, operation, description, created_at
			 FROM workspace_files WHERE agent = ? ORDER BY created_at DESC`, agent,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, path, agent, process_id, operation, description, created_at
			 FROM workspace_files ORDER BY created_at DESC`,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []WorkspaceFile
	for rows.Next() {
		var f WorkspaceFile
		if err := rows.Scan(&f.ID, &f.Path, &f.Agent, &f.ProcessID, &f.Operation, &f.Description, &f.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// ListWorkspaceFileAgents returns distinct agent names that have written files.
func (s *SQLiteStore) ListWorkspaceFileAgents() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT agent FROM workspace_files WHERE agent != '' ORDER BY agent ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// CountTable returns the number of rows in the given table.
func (s *SQLiteStore) CountTable(table string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
	return count, err
}

// DeleteAllFromTable removes all rows from the given table.
func (s *SQLiteStore) DeleteAllFromTable(table string) error {
	_, err := s.db.Exec("DELETE FROM " + table)
	return err
}

// Vacuum reclaims unused space in the database.
func (s *SQLiteStore) Vacuum() {
	s.db.Exec("VACUUM")
}

// ResetData clears all transient data but preserves settings.
func (s *SQLiteStore) ResetData() error {
	tables := []string{
		"composed_agents",
		"chat_messages",
		"user_memory",
		"memory_items",
		"memory_pages",
		"memory_links",
		"events",
		"process_snapshots",
		"workflow_runs",
		"scheduled_jobs",
		"channel_messages",
		"channels",
		"inbox_replies",
		"agent_inbox",
		"workspace_files",
		"channel_read_cursors",
		"chat_read_cursors",
		"task_processes",
		"task_comments",
		"tasks",
	}
	for _, t := range tables {
		if err := s.DeleteAllFromTable(t); err != nil {
			return fmt.Errorf("clear %s: %w", t, err)
		}
	}
	s.Vacuum()
	return nil
}

// UpsertSetting creates or updates a setting.
func (s *SQLiteStore) UpsertSetting(st Setting) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value, sensitive, created_at, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(key)
		 DO UPDATE SET value = excluded.value, sensitive = excluded.sensitive, updated_at = CURRENT_TIMESTAMP`,
		st.Key, st.Value, st.Sensitive,
	)
	return err
}

// GetSetting returns a setting by key.
func (s *SQLiteStore) GetSetting(key string) (*Setting, error) {
	var st Setting
	err := s.db.QueryRow(
		`SELECT key, value, sensitive, created_at, updated_at FROM settings WHERE key = ?`, key,
	).Scan(&st.Key, &st.Value, &st.Sensitive, &st.CreatedAt, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// ListSettings returns all settings.
func (s *SQLiteStore) ListSettings() ([]Setting, error) {
	rows, err := s.db.Query(
		`SELECT key, value, sensitive, created_at, updated_at FROM settings ORDER BY key ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var settings []Setting
	for rows.Next() {
		var st Setting
		if err := rows.Scan(&st.Key, &st.Value, &st.Sensitive, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		settings = append(settings, st)
	}
	return settings, rows.Err()
}

// DeleteSetting removes a setting by key.
func (s *SQLiteStore) DeleteSetting(key string) error {
	result, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpsertMCPServer persists an MCP server connection config.
func (s *SQLiteStore) UpsertMCPServer(name, configJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO mcp_servers (name, config, created_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name)
		 DO UPDATE SET config = excluded.config`,
		name, configJSON,
	)
	return err
}

// DeleteMCPServer removes a persisted MCP server connection.
func (s *SQLiteStore) DeleteMCPServer(name string) error {
	_, err := s.db.Exec(`DELETE FROM mcp_servers WHERE name = ?`, name)
	return err
}

// ListMCPServers returns all persisted MCP server configs.
func (s *SQLiteStore) ListMCPServers() ([]MCPServerConfig, error) {
	rows, err := s.db.Query(
		`SELECT name, config, disabled FROM mcp_servers ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []MCPServerConfig
	for rows.Next() {
		var sc MCPServerConfig
		if err := rows.Scan(&sc.Name, &sc.ConfigJSON, &sc.Disabled); err != nil {
			return nil, err
		}
		servers = append(servers, sc)
	}
	return servers, rows.Err()
}

// SetMCPServerDisabled enables or disables a persisted MCP server.
func (s *SQLiteStore) SetMCPServerDisabled(name string, disabled bool) error {
	val := 0
	if disabled {
		val = 1
	}
	res, err := s.db.Exec(`UPDATE mcp_servers SET disabled = ? WHERE name = ?`, val, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("server %q not found", name)
	}
	return nil
}

// --- Channel Methods ---

// CreateChannel creates a new channel.
func (s *SQLiteStore) CreateChannel(id, name, description, createdBy string, team []string, mode string) error {
	teamJSON, _ := json.Marshal(team)
	_, err := s.db.Exec(
		`INSERT INTO channels (id, name, description, team, mode, created_by) VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, description, string(teamJSON), mode, createdBy,
	)
	return err
}

// GetChannel returns a channel by name.
func (s *SQLiteStore) GetChannel(name string) (*Channel, error) {
	var ch Channel
	var teamJSON string
	var updatedAt sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, name, description, team, mode, created_by, created_at, updated_at FROM channels WHERE name = ?`, name,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &teamJSON, &ch.Mode, &ch.CreatedBy, &ch.CreatedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(teamJSON), &ch.Team)
	if updatedAt.Valid {
		ch.UpdatedAt = updatedAt.Time
	} else {
		ch.UpdatedAt = ch.CreatedAt
	}
	return &ch, nil
}

// GetChannelByName returns minimal channel info for the dsl.ChannelBackend interface.
func (s *SQLiteStore) GetChannelByName(name string) (*dsl.ChannelInfo, error) {
	ch, err := s.GetChannel(name)
	if err != nil || ch == nil {
		return nil, err
	}
	return &dsl.ChannelInfo{ID: ch.ID, Name: ch.Name, Team: ch.Team}, nil
}

// ListAllChannels returns all channels as ChannelInfo (for the dsl.ChannelBackend interface).
func (s *SQLiteStore) ListAllChannels() ([]dsl.ChannelInfo, error) {
	channels, err := s.ListChannels("default")
	if err != nil {
		return nil, err
	}
	result := make([]dsl.ChannelInfo, len(channels))
	for i, ch := range channels {
		result[i] = dsl.ChannelInfo{ID: ch.ID, Name: ch.Name, Team: ch.Team}
	}
	return result, nil
}

// ListChannelsForAgent returns channels where the agent is a team member.
func (s *SQLiteStore) ListChannelsForAgent(agent string) ([]dsl.ChannelInfo, error) {
	channels, err := s.ListChannels("default")
	if err != nil {
		return nil, err
	}
	var result []dsl.ChannelInfo
	for _, ch := range channels {
		for _, member := range ch.Team {
			if member == agent {
				result = append(result, dsl.ChannelInfo{ID: ch.ID, Name: ch.Name, Team: ch.Team})
				break
			}
		}
	}
	return result, nil
}

// ListChannels returns all channels with unread counts for the given user.
func (s *SQLiteStore) ListChannels(userID string) ([]Channel, error) {
	if userID == "" {
		userID = "default"
	}
	rows, err := s.db.Query(`
		SELECT c.id, c.name, c.description, c.team, c.mode, c.created_by, c.created_at, c.updated_at,
		       COALESCE((SELECT COUNT(*) FROM channel_messages WHERE channel_id = c.id AND thread_id IS NULL), 0),
		       COALESCE((SELECT COUNT(*) FROM channel_messages WHERE channel_id = c.id AND thread_id IS NULL
		                 AND id > COALESCE((SELECT last_read_id FROM channel_read_cursors WHERE channel_id = c.id AND user_id = ?), 0)), 0)
		FROM channels c
		ORDER BY c.created_at ASC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		var teamJSON string
		var updatedAt sql.NullTime
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &teamJSON, &ch.Mode, &ch.CreatedBy, &ch.CreatedAt, &updatedAt, &ch.MessageCount, &ch.UnreadCount); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(teamJSON), &ch.Team)
		if updatedAt.Valid {
			ch.UpdatedAt = updatedAt.Time
		} else {
			ch.UpdatedAt = ch.CreatedAt
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// DeleteChannel removes a channel by name.
func (s *SQLiteStore) DeleteChannel(name string) error {
	// Get channel ID first for cascading message cleanup.
	var id string
	err := s.db.QueryRow(`SELECT id FROM channels WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	// Delete messages first (SQLite foreign key cascade may not be enabled).
	s.db.Exec(`DELETE FROM channel_messages WHERE channel_id = ?`, id)
	result, err := s.db.Exec(`DELETE FROM channels WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateChannelTeam updates the team members of a channel and bumps
// updated_at.
func (s *SQLiteStore) UpdateChannelTeam(name string, team []string) error {
	teamJSON, _ := json.Marshal(team)
	result, err := s.db.Exec(
		`UPDATE channels SET team = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?`,
		string(teamJSON), name,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateChannelMeta partially updates a channel's display fields (name,
// description). Pass nil/empty to leave a field unchanged. Bumps
// updated_at. Returns sql.ErrNoRows if the channel doesn't exist.
func (s *SQLiteStore) UpdateChannelMeta(currentName string, newName, newDescription *string) error {
	if newName == nil && newDescription == nil {
		// Nothing to update — still verify the channel exists so the
		// caller can distinguish "no-op" from "not found."
		var exists int
		err := s.db.QueryRow(`SELECT 1 FROM channels WHERE name = ?`, currentName).Scan(&exists)
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return err
	}
	// Build the UPDATE dynamically.
	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if newName != nil {
		sets = append(sets, "name = ?")
		args = append(args, *newName)
	}
	if newDescription != nil {
		sets = append(sets, "description = ?")
		args = append(args, *newDescription)
	}
	args = append(args, currentName)
	result, err := s.db.Exec(`UPDATE channels SET `+strings.Join(sets, ", ")+` WHERE name = ?`, args...)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// FindChannelForAgents returns the first channel where both agents are team members.
func (s *SQLiteStore) FindChannelForAgents(agent1, agent2 string) (string, string, error) {
	rows, err := s.db.Query(`SELECT id, name, team FROM channels`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, teamJSON string
		if err := rows.Scan(&id, &name, &teamJSON); err != nil {
			return "", "", err
		}
		var team []string
		json.Unmarshal([]byte(teamJSON), &team)
		has1, has2 := false, false
		for _, m := range team {
			if m == agent1 {
				has1 = true
			}
			if m == agent2 {
				has2 = true
			}
		}
		if has1 && has2 {
			return id, name, nil
		}
	}
	return "", "", rows.Err()
}

// InsertChannelMessage inserts a message into a channel and returns its ID.
// Pass nil for `activities` when there are no tool calls to record.
func (s *SQLiteStore) InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string, activities []vega.ToolActivity) (int64, error) {
	if metadata == "" {
		metadata = "{}"
	}
	activitiesJSON := []byte("[]")
	if len(activities) > 0 {
		if b, err := json.Marshal(activities); err == nil {
			activitiesJSON = b
		}
	}
	result, err := s.db.Exec(
		`INSERT INTO channel_messages (channel_id, thread_id, agent, role, content, metadata, sender, tool_activities) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		channelID, threadID, agent, role, content, metadata, sender, string(activitiesJSON),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ListChannelMessages returns top-level messages for a channel with
// reply count + latest reply timestamp + distinct reply senders, so the
// channel UI can render thread indicators without an N+1 fetch.
func (s *SQLiteStore) ListChannelMessages(channelID string, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT m.id, m.channel_id, m.thread_id, m.agent, m.sender, m.role, m.content, m.metadata, m.tool_activities, m.created_at,
		        COALESCE((SELECT COUNT(*) FROM channel_messages r WHERE r.thread_id = m.id), 0) as reply_count,
		        (SELECT MAX(created_at) FROM channel_messages r WHERE r.thread_id = m.id) as latest_reply_at,
		        (SELECT GROUP_CONCAT(DISTINCT COALESCE(NULLIF(sender, ''), agent))
		           FROM channel_messages r WHERE r.thread_id = m.id) as reply_senders
		 FROM channel_messages m
		 WHERE m.channel_id = ? AND m.thread_id IS NULL
		 ORDER BY m.created_at ASC LIMIT ?`,
		channelID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ChannelMessage
	for rows.Next() {
		var m ChannelMessage
		var threadID sql.NullInt64
		// The (SELECT MAX(created_at) ...) subquery loses column type info
		// in modernc.org/sqlite — DATETIME comes back as a string rather
		// than a time.Time. Scan into NullString and parse so NullTime
		// doesn't choke on the unsupported conversion.
		var latestReplyRaw sql.NullString
		var replySenders sql.NullString
		var activitiesJSON string
		if err := rows.Scan(&m.ID, &m.ChannelID, &threadID, &m.Agent, &m.Sender, &m.Role, &m.Content, &m.Metadata, &activitiesJSON, &m.CreatedAt, &m.ReplyCount, &latestReplyRaw, &replySenders); err != nil {
			return nil, err
		}
		if activitiesJSON != "" && activitiesJSON != "[]" {
			_ = json.Unmarshal([]byte(activitiesJSON), &m.ToolActivities)
		}
		if threadID.Valid {
			m.ThreadID = &threadID.Int64
		}
		if latestReplyRaw.Valid && latestReplyRaw.String != "" {
			if t, err := parseSQLiteTime(latestReplyRaw.String); err == nil {
				m.LatestReplyAt = &t
			}
		}
		if replySenders.Valid && replySenders.String != "" {
			// GROUP_CONCAT returns a comma-separated list. Split + dedup
			// (SQLite already DISTINCTs but trim spaces just in case).
			for _, s := range strings.Split(replySenders.String, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					m.ReplySenders = append(m.ReplySenders, s)
				}
			}
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// RecentChannelMessages returns the last N messages in a channel (lightweight, for status checks).
func (s *SQLiteStore) RecentChannelMessages(channelID string, limit int) ([]dsl.ChannelMessage, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.Query(
		`SELECT agent, sender, content FROM channel_messages
		 WHERE channel_id = ? AND thread_id IS NULL
		 ORDER BY created_at DESC LIMIT ?`,
		channelID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []dsl.ChannelMessage
	for rows.Next() {
		var m dsl.ChannelMessage
		if err := rows.Scan(&m.Agent, &m.Sender, &m.Content); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

// ListThreadMessages returns the original message and all replies in a thread.
func (s *SQLiteStore) ListThreadMessages(channelID string, threadID int64) ([]ChannelMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, channel_id, thread_id, agent, sender, role, content, metadata, tool_activities, created_at
		 FROM channel_messages
		 WHERE channel_id = ? AND (id = ? OR thread_id = ?)
		 ORDER BY created_at ASC`,
		channelID, threadID, threadID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ChannelMessage
	for rows.Next() {
		var m ChannelMessage
		var tid sql.NullInt64
		var activitiesJSON string
		if err := rows.Scan(&m.ID, &m.ChannelID, &tid, &m.Agent, &m.Sender, &m.Role, &m.Content, &m.Metadata, &activitiesJSON, &m.CreatedAt); err != nil {
			return nil, err
		}
		if tid.Valid {
			m.ThreadID = &tid.Int64
		}
		if activitiesJSON != "" && activitiesJSON != "[]" {
			_ = json.Unmarshal([]byte(activitiesJSON), &m.ToolActivities)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// --- Inbox Methods ---

// InsertInboxItem creates a new inbox item and returns its ID.
func (s *SQLiteStore) InsertInboxItem(fromAgent, subject, body, priority string) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO agent_inbox (from_agent, subject, body, priority) VALUES (?, ?, ?, ?)`,
		fromAgent, subject, body, priority,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// InsertResolvedInboxItem inserts an item already marked resolved. Used
// for auto-success dispatch outcomes that don't need orchestrator
// triage.
func (s *SQLiteStore) InsertResolvedInboxItem(fromAgent, subject, body, resolution string) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO agent_inbox (from_agent, subject, body, priority, status, resolution, resolved_at)
		 VALUES (?, ?, ?, 'normal', 'resolved', ?, CURRENT_TIMESTAMP)`,
		fromAgent, subject, body, resolution,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ListInboxItems returns inbox items filtered by status.
// PendingInboxCount returns the number of pending inbox items (cheap query, no LLM needed).
func (s *SQLiteStore) PendingInboxCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE status = 'pending'`).Scan(&count)
	return count, err
}

func (s *SQLiteStore) ListInboxItems(status string, limit int) ([]InboxItem, error) {
	if limit <= 0 {
		limit = 50
	}

	var query string
	var args []any
	if status == "all" || status == "" {
		query = `SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at, triage_count, last_triaged_at
			FROM agent_inbox ORDER BY created_at DESC LIMIT ?`
		args = []any{limit}
	} else {
		query = `SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at, triage_count, last_triaged_at
			FROM agent_inbox WHERE status = ? ORDER BY created_at DESC LIMIT ?`
		args = []any{status, limit}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []InboxItem
	for rows.Next() {
		var item InboxItem
		var resolution sql.NullString
		var resolvedAt, lastTriagedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.FromAgent, &item.Subject, &item.Body,
			&item.Priority, &item.Status, &resolution, &item.CreatedAt, &resolvedAt,
			&item.TriageCount, &lastTriagedAt); err != nil {
			return nil, err
		}
		if resolution.Valid {
			item.Resolution = resolution.String
		}
		if resolvedAt.Valid {
			item.ResolvedAt = &resolvedAt.Time
		}
		if lastTriagedAt.Valid {
			item.LastTriagedAt = &lastTriagedAt.Time
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetInboxItem returns a single inbox item by ID.
func (s *SQLiteStore) GetInboxItem(id int64) (*InboxItem, error) {
	row := s.db.QueryRow(
		`SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at, triage_count, last_triaged_at
		FROM agent_inbox WHERE id = ?`, id)

	var item InboxItem
	var resolution sql.NullString
	var resolvedAt, lastTriagedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.FromAgent, &item.Subject, &item.Body,
		&item.Priority, &item.Status, &resolution, &item.CreatedAt, &resolvedAt,
		&item.TriageCount, &lastTriagedAt); err != nil {
		return nil, err
	}
	if resolution.Valid {
		item.Resolution = resolution.String
	}
	if resolvedAt.Valid {
		item.ResolvedAt = &resolvedAt.Time
	}
	if lastTriagedAt.Valid {
		item.LastTriagedAt = &lastTriagedAt.Time
	}
	return &item, nil
}

// ResolveInboxItem marks an inbox item as resolved.
func (s *SQLiteStore) ResolveInboxItem(id int64, resolution string) error {
	result, err := s.db.Exec(
		`UPDATE agent_inbox SET status = 'resolved', resolution = ?, resolved_at = CURRENT_TIMESTAMP WHERE id = ?`,
		resolution, id,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteResolvedInboxItems removes all resolved inbox items and their replies.
func (s *SQLiteStore) DeleteResolvedInboxItems() (int64, error) {
	// Delete replies for resolved items first.
	s.db.Exec(`DELETE FROM inbox_replies WHERE inbox_id IN (SELECT id FROM agent_inbox WHERE status = 'resolved')`)
	result, err := s.db.Exec(`DELETE FROM agent_inbox WHERE status = 'resolved'`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// TriageInboxItems is the cost-control backstop for the orchestrator's
// inbox loop. Each id that is currently pending gets its triage_count
// incremented and last_triaged_at stamped; anything whose post-increment
// count reaches `threshold` is auto-resolved with a synthetic
// resolution. Returns the ids that flipped to resolved on this call.
//
// Implementation is intentionally a few small queries rather than one
// CTE — sqlite + postgres both run this, and we want it to behave
// identically. Volume per call is bounded by the orchestrator's
// list_inbox page (≤ 50), so the cost is negligible.
func (s *SQLiteStore) TriageInboxItems(ids []int64, threshold int) ([]int64, error) {
	if len(ids) == 0 || threshold <= 0 {
		return nil, nil
	}
	aged := make([]int64, 0)
	for _, id := range ids {
		// Only touch pending rows. UPDATE returns rows-affected so we
		// know whether to read back.
		res, err := s.db.Exec(
			`UPDATE agent_inbox
			   SET triage_count = triage_count + 1, last_triaged_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND status = 'pending'`, id)
		if err != nil {
			return aged, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		var count int
		if err := s.db.QueryRow(`SELECT triage_count FROM agent_inbox WHERE id = ?`, id).Scan(&count); err != nil {
			return aged, err
		}
		if count >= threshold {
			resolution := fmt.Sprintf(
				"auto-aged: triaged %d times without an orchestrator decision — see channel posts for context",
				count,
			)
			if _, err := s.db.Exec(
				`UPDATE agent_inbox
				   SET status = 'resolved', resolution = ?, resolved_at = CURRENT_TIMESTAMP
				 WHERE id = ? AND status = 'pending'`,
				resolution, id,
			); err != nil {
				return aged, err
			}
			aged = append(aged, id)
		}
	}
	return aged, nil
}

// DeleteInboxItem removes a single inbox item (and any replies) by id.
// Returns sql.ErrNoRows if nothing was deleted so HTTP handlers can map
// to 404.
func (s *SQLiteStore) DeleteInboxItem(id int64) error {
	s.db.Exec(`DELETE FROM inbox_replies WHERE inbox_id = ?`, id)
	result, err := s.db.Exec(`DELETE FROM agent_inbox WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Prompt History Methods ---

// InsertPromptHistory records an original user prompt to iris.
func (s *SQLiteStore) InsertPromptHistory(prompt string) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO prompt_history (prompt) VALUES (?)`, prompt,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ListPromptHistory returns prompt history entries, newest first.
func (s *SQLiteStore) ListPromptHistory(limit int) ([]PromptHistoryItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, prompt, created_at FROM prompt_history ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PromptHistoryItem
	for rows.Next() {
		var item PromptHistoryItem
		if err := rows.Scan(&item.ID, &item.Prompt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SearchPromptHistory searches prompt history by keyword via LIKE.
func (s *SQLiteStore) SearchPromptHistory(query string, limit int) ([]PromptHistoryItem, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, prompt, created_at FROM prompt_history
		 WHERE prompt LIKE ?
		 ORDER BY id DESC LIMIT ?`,
		pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PromptHistoryItem
	for rows.Next() {
		var item PromptHistoryItem
		if err := rows.Scan(&item.ID, &item.Prompt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DeletePromptHistory removes a prompt history entry by ID.
func (s *SQLiteStore) DeletePromptHistory(id int64) error {
	result, err := s.db.Exec(`DELETE FROM prompt_history WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MarkChannelRead updates the read cursor for a channel so unread count resets.
func (s *SQLiteStore) MarkChannelRead(channelID, userID string) error {
	if userID == "" {
		userID = "default"
	}
	_, err := s.db.Exec(`
		INSERT INTO channel_read_cursors (channel_id, user_id, last_read_id, updated_at)
		VALUES (?, ?, COALESCE((SELECT MAX(id) FROM channel_messages WHERE channel_id = ? AND thread_id IS NULL), 0), CURRENT_TIMESTAMP)
		ON CONFLICT(channel_id, user_id) DO UPDATE SET
			last_read_id = COALESCE((SELECT MAX(id) FROM channel_messages WHERE channel_id = excluded.channel_id AND thread_id IS NULL), 0),
			updated_at = CURRENT_TIMESTAMP`,
		channelID, userID, channelID,
	)
	return err
}

// MarkChatRead updates the read cursor for a DM conversation so unread count resets.
func (s *SQLiteStore) MarkChatRead(agent, userID string) error {
	if userID == "" {
		userID = "default"
	}
	_, err := s.db.Exec(`
		INSERT INTO chat_read_cursors (agent, user_id, last_read_id, updated_at)
		VALUES (?, ?, COALESCE((SELECT MAX(id) FROM chat_messages WHERE agent = ?), 0), CURRENT_TIMESTAMP)
		ON CONFLICT(agent, user_id) DO UPDATE SET
			last_read_id = COALESCE((SELECT MAX(id) FROM chat_messages WHERE agent = excluded.agent), 0),
			updated_at = CURRENT_TIMESTAMP`,
		agent, userID, agent,
	)
	return err
}

// ChatUnreadCounts returns a map of agent name → unread message count for DMs.
func (s *SQLiteStore) ChatUnreadCounts(userID string) (map[string]int, error) {
	if userID == "" {
		userID = "default"
	}
	rows, err := s.db.Query(`
		SELECT cm.agent, COUNT(*) as unread
		FROM chat_messages cm
		LEFT JOIN chat_read_cursors crc ON cm.agent = crc.agent AND crc.user_id = ?
		WHERE cm.role = 'assistant'
		  AND cm.id > COALESCE(crc.last_read_id, 0)
		GROUP BY cm.agent`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var agent string
		var count int
		if err := rows.Scan(&agent, &count); err != nil {
			return nil, err
		}
		counts[agent] = count
	}
	return counts, rows.Err()
}

// snapshotFromResponse builds a ProcessSnapshot from a live process response.
func snapshotFromResponse(proc ProcessResponse) ProcessSnapshot {
	return ProcessSnapshot{
		ProcessID:    proc.ID,
		AgentName:    proc.Agent,
		Status:       proc.Status,
		ParentID:     proc.ParentID,
		InputTokens:  proc.Metrics.InputTokens,
		OutputTokens: proc.Metrics.OutputTokens,
		CostUSD:      proc.Metrics.CostUSD,
		StartedAt:    proc.StartedAt,
		CompletedAt:  proc.CompletedAt,
		SnapshotAt:   time.Now(),
	}
}
