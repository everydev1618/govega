package serve

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using modernc.org/sqlite (pure Go).
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens or creates a SQLite database at the given path.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Enable WAL mode for concurrent reads and set busy timeout
	// so concurrent writers wait instead of returning SQLITE_BUSY.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=30000"); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
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
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		from_agent  TEXT NOT NULL,
		subject     TEXT NOT NULL,
		body        TEXT NOT NULL DEFAULT '',
		priority    TEXT NOT NULL DEFAULT 'normal',
		status      TEXT NOT NULL DEFAULT 'pending',
		resolution  TEXT DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		resolved_at DATETIME
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

	CREATE INDEX IF NOT EXISTS idx_events_process ON events(process_id);
	CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_snapshots_process ON process_snapshots(process_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_id ON workflow_runs(run_id);
	CREATE INDEX IF NOT EXISTS idx_chat_agent ON chat_messages(agent);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
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

	// Migrate: add tool_activities column to chat_messages + channel_messages.
	// Stores a JSON array of completed tool calls captured during the
	// streaming turn, so loaded history reproduces the live timeline.
	s.db.Exec(`ALTER TABLE chat_messages ADD COLUMN tool_activities TEXT NOT NULL DEFAULT '[]'`)
	s.db.Exec(`ALTER TABLE channel_messages ADD COLUMN tool_activities TEXT NOT NULL DEFAULT '[]'`)

	// Migrate: add mode column to channels if missing.
	s.db.Exec(`ALTER TABLE channels ADD COLUMN mode TEXT NOT NULL DEFAULT ''`)

	// Migrate: add updated_at column. Backfill from created_at so existing
	// rows surface a sensible value rather than null/epoch.
	if _, err := s.db.Exec(`ALTER TABLE channels ADD COLUMN updated_at DATETIME`); err == nil {
		s.db.Exec(`UPDATE channels SET updated_at = created_at WHERE updated_at IS NULL`)
	}

	// Migrate: add sender column to channel_messages for multi-user identity.
	s.db.Exec(`ALTER TABLE channel_messages ADD COLUMN sender TEXT DEFAULT ''`)

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
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO composed_agents (name, display_name, title, description, avatar, icon, avatar_gradient, model, persona, skills, tools, team, system, temperature, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		   updated_at      = excluded.updated_at`,
		a.Name, a.DisplayName, a.Title, a.Description, a.Avatar, a.Icon, string(gradJSON), a.Model, a.Persona, string(skillsJSON), string(toolsJSON), string(teamJSON), a.System, a.Temperature, a.CreatedAt, a.UpdatedAt,
	)
	return err
}

// ListComposedAgents returns all composed agents.
func (s *SQLiteStore) ListComposedAgents() ([]ComposedAgent, error) {
	rows, err := s.db.Query(
		`SELECT name, display_name, title, description, avatar, icon, avatar_gradient, model, persona, skills, tools, team, system, temperature, created_at, updated_at
		 FROM composed_agents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []ComposedAgent
	for rows.Next() {
		var a ComposedAgent
		var skillsJSON, toolsJSON, teamJSON, gradJSON string
		var temp sql.NullFloat64
		var updatedAt sql.NullTime
		if err := rows.Scan(&a.Name, &a.DisplayName, &a.Title, &a.Description, &a.Avatar, &a.Icon, &gradJSON, &a.Model, &a.Persona, &skillsJSON, &toolsJSON, &teamJSON, &a.System, &temp, &a.CreatedAt, &updatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(skillsJSON), &a.Skills)
		json.Unmarshal([]byte(toolsJSON), &a.Tools)
		json.Unmarshal([]byte(teamJSON), &a.Team)
		json.Unmarshal([]byte(gradJSON), &a.AvatarGradient)
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
		var latestReplyAt sql.NullTime
		var replySenders sql.NullString
		var activitiesJSON string
		if err := rows.Scan(&m.ID, &m.ChannelID, &threadID, &m.Agent, &m.Sender, &m.Role, &m.Content, &m.Metadata, &activitiesJSON, &m.CreatedAt, &m.ReplyCount, &latestReplyAt, &replySenders); err != nil {
			return nil, err
		}
		if activitiesJSON != "" && activitiesJSON != "[]" {
			_ = json.Unmarshal([]byte(activitiesJSON), &m.ToolActivities)
		}
		if threadID.Valid {
			m.ThreadID = &threadID.Int64
		}
		if latestReplyAt.Valid {
			m.LatestReplyAt = &latestReplyAt.Time
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
		query = `SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at
			FROM agent_inbox ORDER BY created_at DESC LIMIT ?`
		args = []any{limit}
	} else {
		query = `SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at
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
		var resolvedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.FromAgent, &item.Subject, &item.Body,
			&item.Priority, &item.Status, &resolution, &item.CreatedAt, &resolvedAt); err != nil {
			return nil, err
		}
		if resolution.Valid {
			item.Resolution = resolution.String
		}
		if resolvedAt.Valid {
			item.ResolvedAt = &resolvedAt.Time
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetInboxItem returns a single inbox item by ID.
func (s *SQLiteStore) GetInboxItem(id int64) (*InboxItem, error) {
	row := s.db.QueryRow(
		`SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at
		FROM agent_inbox WHERE id = ?`, id)

	var item InboxItem
	var resolution sql.NullString
	var resolvedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.FromAgent, &item.Subject, &item.Body,
		&item.Priority, &item.Status, &resolution, &item.CreatedAt, &resolvedAt); err != nil {
		return nil, err
	}
	if resolution.Valid {
		item.Resolution = resolution.String
	}
	if resolvedAt.Valid {
		item.ResolvedAt = &resolvedAt.Time
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

// snapshotProcess creates a snapshot from a live process and persists it.
func (s *SQLiteStore) snapshotProcess(proc ProcessResponse) error {
	snap := ProcessSnapshot{
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
	return s.InsertProcessSnapshot(snap)
}
