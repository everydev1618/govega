package serve

import (
	"database/sql"
	"encoding/json"
	"time"

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
	// Enable WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
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
		name        TEXT PRIMARY KEY,
		model       TEXT NOT NULL DEFAULT '',
		persona     TEXT NOT NULL DEFAULT '',
		skills      TEXT NOT NULL DEFAULT '[]',
		tools       TEXT NOT NULL DEFAULT '[]',
		team        TEXT NOT NULL DEFAULT '[]',
		system      TEXT NOT NULL DEFAULT '',
		temperature REAL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
		`INSERT OR REPLACE INTO composed_agents (name, model, persona, skills, tools, team, system, temperature, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Model, a.Persona, string(skillsJSON), string(toolsJSON), string(teamJSON), a.System, a.Temperature, a.CreatedAt,
	)
	return err
}

// ListComposedAgents returns all composed agents.
func (s *SQLiteStore) ListComposedAgents() ([]ComposedAgent, error) {
	rows, err := s.db.Query(
		`SELECT name, model, persona, skills, tools, team, system, temperature, created_at
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
		if err := rows.Scan(&a.Name, &a.Model, &a.Persona, &skillsJSON, &toolsJSON, &teamJSON, &a.System, &temp, &a.CreatedAt); err != nil {
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
		`SELECT name, config FROM mcp_servers ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []MCPServerConfig
	for rows.Next() {
		var sc MCPServerConfig
		if err := rows.Scan(&sc.Name, &sc.ConfigJSON); err != nil {
			return nil, err
		}
		servers = append(servers, sc)
	}
	return servers, rows.Err()
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
