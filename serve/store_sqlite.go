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

	CREATE INDEX IF NOT EXISTS idx_events_process ON events(process_id);
	CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_snapshots_process ON process_snapshots(process_id);
	CREATE INDEX IF NOT EXISTS idx_workflow_runs_id ON workflow_runs(run_id);
	CREATE INDEX IF NOT EXISTS idx_chat_agent ON chat_messages(agent);
	`
	_, err := s.db.Exec(schema)
	return err
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
	teamJSON, _ := json.Marshal(a.Team)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO composed_agents (name, model, persona, skills, team, system, temperature, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Model, a.Persona, string(skillsJSON), string(teamJSON), a.System, a.Temperature, a.CreatedAt,
	)
	return err
}

// ListComposedAgents returns all composed agents.
func (s *SQLiteStore) ListComposedAgents() ([]ComposedAgent, error) {
	rows, err := s.db.Query(
		`SELECT name, model, persona, skills, team, system, temperature, created_at
		 FROM composed_agents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []ComposedAgent
	for rows.Next() {
		var a ComposedAgent
		var skillsJSON, teamJSON string
		var temp sql.NullFloat64
		if err := rows.Scan(&a.Name, &a.Model, &a.Persona, &skillsJSON, &teamJSON, &a.System, &temp, &a.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(skillsJSON), &a.Skills)
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
