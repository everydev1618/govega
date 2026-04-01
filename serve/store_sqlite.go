package serve

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
		name         TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		title        TEXT NOT NULL DEFAULT '',
		model        TEXT NOT NULL DEFAULT '',
		persona      TEXT NOT NULL DEFAULT '',
		skills       TEXT NOT NULL DEFAULT '[]',
		tools        TEXT NOT NULL DEFAULT '[]',
		team         TEXT NOT NULL DEFAULT '[]',
		system       TEXT NOT NULL DEFAULT '',
		temperature  REAL,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS chat_messages (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		agent      TEXT NOT NULL,
		role       TEXT NOT NULL,
		content    TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
		topic      TEXT NOT NULL DEFAULT '',
		content    TEXT NOT NULL,
		tags       TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_memory_items_user_agent ON memory_items(user_id, agent);
	CREATE INDEX IF NOT EXISTS idx_memory_items_topic ON memory_items(user_id, agent, topic);

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
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS channel_messages (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id  TEXT NOT NULL,
		thread_id   INTEGER,
		agent       TEXT DEFAULT '',
		role        TEXT NOT NULL,
		content     TEXT NOT NULL,
		metadata    TEXT DEFAULT '{}',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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

	// Migrate: add mode column to channels if missing.
	s.db.Exec(`ALTER TABLE channels ADD COLUMN mode TEXT NOT NULL DEFAULT ''`)

	// Migrate: add sender column to channel_messages for multi-user identity.
	s.db.Exec(`ALTER TABLE channel_messages ADD COLUMN sender TEXT DEFAULT ''`)

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

// InsertComposedAgent persists a composed agent definition.
func (s *SQLiteStore) InsertComposedAgent(a ComposedAgent) error {
	skillsJSON, _ := json.Marshal(a.Skills)
	toolsJSON, _ := json.Marshal(a.Tools)
	teamJSON, _ := json.Marshal(a.Team)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO composed_agents (name, display_name, title, avatar, model, persona, skills, tools, team, system, temperature, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.DisplayName, a.Title, a.Avatar, a.Model, a.Persona, string(skillsJSON), string(toolsJSON), string(teamJSON), a.System, a.Temperature, a.CreatedAt,
	)
	return err
}

// ListComposedAgents returns all composed agents.
func (s *SQLiteStore) ListComposedAgents() ([]ComposedAgent, error) {
	rows, err := s.db.Query(
		`SELECT name, display_name, title, avatar, model, persona, skills, tools, team, system, temperature, created_at
		 FROM composed_agents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []ComposedAgent
	for rows.Next() {
		var a ComposedAgent
		var skillsJSON, toolsJSON, teamJSON string
		var temp sql.NullFloat64
		if err := rows.Scan(&a.Name, &a.DisplayName, &a.Title, &a.Avatar, &a.Model, &a.Persona, &skillsJSON, &toolsJSON, &teamJSON, &a.System, &temp, &a.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(skillsJSON), &a.Skills)
		json.Unmarshal([]byte(toolsJSON), &a.Tools)
		json.Unmarshal([]byte(teamJSON), &a.Team)
		if temp.Valid {
			a.Temperature = &temp.Float64
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

// InsertChatMessage persists a chat message for an agent.
func (s *SQLiteStore) InsertChatMessage(agent, role, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO chat_messages (agent, role, content) VALUES (?, ?, ?)`,
		agent, role, content,
	)
	return err
}

// ListChatMessages returns all chat messages for an agent, oldest first.
func (s *SQLiteStore) ListChatMessages(agent string) ([]ChatMessage, error) {
	rows, err := s.db.Query(
		`SELECT role, content FROM chat_messages WHERE agent = ? ORDER BY id ASC`, agent,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
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

// UpsertScheduledJob creates or replaces a scheduled job.
func (s *SQLiteStore) UpsertScheduledJob(job ScheduledJob) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO scheduled_jobs (name, cron, agent_name, message, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, COALESCE(
		   (SELECT created_at FROM scheduled_jobs WHERE name = ?),
		   CURRENT_TIMESTAMP
		 ))`,
		job.Name, job.Cron, job.AgentName, job.Message, job.Enabled, job.Name,
	)
	return err
}

// DeleteScheduledJob removes a scheduled job by name.
func (s *SQLiteStore) DeleteScheduledJob(name string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_jobs WHERE name = ?`, name)
	return err
}

// ListScheduledJobs returns all scheduled jobs.
func (s *SQLiteStore) ListScheduledJobs() ([]ScheduledJob, error) {
	rows, err := s.db.Query(
		`SELECT name, cron, agent_name, message, enabled, created_at
		 FROM scheduled_jobs ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ScheduledJob
	for rows.Next() {
		var j ScheduledJob
		if err := rows.Scan(&j.Name, &j.Cron, &j.AgentName, &j.Message, &j.Enabled, &j.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// InsertMemoryItem saves a memory item and returns its ID.
func (s *SQLiteStore) InsertMemoryItem(item MemoryItem) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO memory_items (user_id, agent, topic, content, tags)
		 VALUES (?, ?, ?, ?, ?)`,
		item.UserID, item.Agent, item.Topic, item.Content, item.Tags,
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
		`SELECT id, user_id, agent, topic, content, tags, created_at, updated_at
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
		if err := rows.Scan(&m.ID, &m.UserID, &m.Agent, &m.Topic, &m.Content, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
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
		`SELECT id, user_id, agent, topic, content, tags, created_at, updated_at
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
		if err := rows.Scan(&m.ID, &m.UserID, &m.Agent, &m.Topic, &m.Content, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
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
	err := s.db.QueryRow(
		`SELECT id, name, description, team, mode, created_by, created_at FROM channels WHERE name = ?`, name,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &teamJSON, &ch.Mode, &ch.CreatedBy, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(teamJSON), &ch.Team)
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
		SELECT c.id, c.name, c.description, c.team, c.mode, c.created_by, c.created_at,
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
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &teamJSON, &ch.Mode, &ch.CreatedBy, &ch.CreatedAt, &ch.MessageCount, &ch.UnreadCount); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(teamJSON), &ch.Team)
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

// UpdateChannelTeam updates the team members of a channel.
func (s *SQLiteStore) UpdateChannelTeam(name string, team []string) error {
	teamJSON, _ := json.Marshal(team)
	result, err := s.db.Exec(`UPDATE channels SET team = ? WHERE name = ?`, string(teamJSON), name)
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
func (s *SQLiteStore) InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string) (int64, error) {
	if metadata == "" {
		metadata = "{}"
	}
	result, err := s.db.Exec(
		`INSERT INTO channel_messages (channel_id, thread_id, agent, role, content, metadata, sender) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		channelID, threadID, agent, role, content, metadata, sender,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// ListChannelMessages returns top-level messages for a channel with reply counts.
func (s *SQLiteStore) ListChannelMessages(channelID string, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT m.id, m.channel_id, m.thread_id, m.agent, m.sender, m.role, m.content, m.metadata, m.created_at,
		        COALESCE((SELECT COUNT(*) FROM channel_messages r WHERE r.thread_id = m.id), 0) as reply_count
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
		if err := rows.Scan(&m.ID, &m.ChannelID, &threadID, &m.Agent, &m.Sender, &m.Role, &m.Content, &m.Metadata, &m.CreatedAt, &m.ReplyCount); err != nil {
			return nil, err
		}
		if threadID.Valid {
			m.ThreadID = &threadID.Int64
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
		`SELECT id, channel_id, thread_id, agent, sender, role, content, metadata, created_at
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
		if err := rows.Scan(&m.ID, &m.ChannelID, &tid, &m.Agent, &m.Sender, &m.Role, &m.Content, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		if tid.Valid {
			m.ThreadID = &tid.Int64
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

// InsertPromptHistory records an original user prompt to hermes.
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
