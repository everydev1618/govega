package serve

// PostgresStore is the Postgres-backed implementation of Store (refs
// govega#61). Lives alongside SQLiteStore — pick via Config.DBKind.
//
// SQLite stays the zero-config default for single-user / dev / hobby
// hosts; Postgres is for multi-writer hosted deployments where SQLite's
// single-writer model and lack of row-level isolation start to bite.
//
// Implementation uses database/sql via the pgx stdlib driver so the
// query-running shape mirrors SQLiteStore as closely as possible. The
// dialect differences (placeholder syntax, upsert, autoincrement,
// blob, timestamp) are spelled out per-method rather than wrapped in
// a translation layer — easier to read, easier to debug.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements Store on top of Postgres.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens a connection pool against the supplied URL
// (postgres:// scheme). Pings once to fail fast on a bad URL.
func NewPostgresStore(url string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

// Close shuts down the connection pool.
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// Init creates the schema. Idempotent — every CREATE uses IF NOT EXISTS
// so re-running against an upgraded database is safe.
func (s *PostgresStore) Init() error {
	if _, err := s.db.Exec(postgresSchema); err != nil {
		return fmt.Errorf("postgres init: %w", err)
	}
	return nil
}

// --- Events ---

func (s *PostgresStore) InsertEvent(e StoreEvent) error {
	_, err := s.db.Exec(
		`INSERT INTO events (type, process_id, agent_name, timestamp, data, result, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.Type, e.ProcessID, e.AgentName, e.Timestamp, e.Data, e.Result, e.Error,
	)
	return err
}

func (s *PostgresStore) ListEvents(limit int) ([]StoreEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, type, process_id, agent_name, timestamp, data, result, error
		 FROM events ORDER BY id DESC LIMIT $1`, limit,
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

func (s *PostgresStore) SearchEvents(filter ActivityFilter) ([]StoreEvent, int, error) {
	where := []string{"1=1"}
	args := []any{}
	next := func() string { return fmt.Sprintf("$%d", len(args)) }
	if q := strings.TrimSpace(filter.Query); q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%")
		p := next()
		where = append(where, fmt.Sprintf(`(
			LOWER(type)       LIKE %s OR
			LOWER(agent_name) LIKE %s OR
			LOWER(data)       LIKE %s OR
			LOWER(result)     LIKE %s OR
			LOWER(error)      LIKE %s
		)`, p, p, p, p, p))
	}
	if filter.Type != "" {
		args = append(args, filter.Type)
		where = append(where, "type = "+next())
	}
	if filter.Agent != "" {
		args = append(args, filter.Agent)
		where = append(where, "agent_name = "+next())
	}
	if !filter.From.IsZero() {
		args = append(args, filter.From)
		where = append(where, "timestamp >= "+next())
	}
	if !filter.To.IsZero() {
		args = append(args, filter.To)
		where = append(where, "timestamp < "+next())
	}
	whereClause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM events WHERE "+whereClause, args...).Scan(&total); err != nil {
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
	args = append(args, limit)
	limitP := next()
	args = append(args, offset)
	offsetP := next()
	rows, err := s.db.Query(`
SELECT id, type, process_id, agent_name, timestamp, data, result, error
FROM events
WHERE `+whereClause+`
ORDER BY id DESC
LIMIT `+limitP+` OFFSET `+offsetP, args...)
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

// --- Process snapshots ---

func (s *PostgresStore) InsertProcessSnapshot(snap ProcessSnapshot) error {
	_, err := s.db.Exec(
		`INSERT INTO process_snapshots
		 (process_id, agent_name, status, parent_id, input_tokens, output_tokens, cost_usd, started_at, completed_at, snapshot_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		snap.ProcessID, snap.AgentName, snap.Status, snap.ParentID,
		snap.InputTokens, snap.OutputTokens, snap.CostUSD,
		nullableTime(snap.StartedAt), snap.CompletedAt, snap.SnapshotAt,
	)
	return err
}

func (s *PostgresStore) ListProcessSnapshots() ([]ProcessSnapshot, error) {
	rows, err := s.db.Query(
		`SELECT ps.id, ps.process_id, ps.agent_name, ps.status, ps.parent_id,
		        ps.input_tokens, ps.output_tokens, ps.cost_usd,
		        ps.started_at, ps.completed_at, ps.snapshot_at
		 FROM process_snapshots ps
		 INNER JOIN (
		   SELECT process_id, MAX(id) AS max_id FROM process_snapshots GROUP BY process_id
		 ) latest ON ps.id = latest.max_id
		 ORDER BY ps.started_at DESC NULLS LAST`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []ProcessSnapshot
	for rows.Next() {
		var snap ProcessSnapshot
		var startedAt, completedAt sql.NullTime
		if err := rows.Scan(
			&snap.ID, &snap.ProcessID, &snap.AgentName, &snap.Status, &snap.ParentID,
			&snap.InputTokens, &snap.OutputTokens, &snap.CostUSD,
			&startedAt, &completedAt, &snap.SnapshotAt,
		); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			snap.StartedAt = startedAt.Time
		}
		if completedAt.Valid {
			snap.CompletedAt = &completedAt.Time
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}

// --- Workflow runs ---

func (s *PostgresStore) InsertWorkflowRun(r WorkflowRun) error {
	_, err := s.db.Exec(
		`INSERT INTO workflow_runs (run_id, workflow, inputs, status, started_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		r.RunID, r.Workflow, r.Inputs, r.Status, r.StartedAt,
	)
	return err
}

func (s *PostgresStore) UpdateWorkflowRun(runID, status, result string) error {
	_, err := s.db.Exec(
		`UPDATE workflow_runs SET status = $1, result = $2 WHERE run_id = $3`,
		status, result, runID,
	)
	return err
}

func (s *PostgresStore) ListWorkflowRuns(limit int) ([]WorkflowRun, error) {
	rows, err := s.db.Query(
		`SELECT id, run_id, workflow, inputs, status, result, started_at
		 FROM workflow_runs ORDER BY id DESC LIMIT $1`, limit,
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

// --- Composed agents ---

func (s *PostgresStore) InsertComposedAgent(a ComposedAgent) error {
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
		`INSERT INTO composed_agents
		 (name, display_name, title, description, avatar, icon, avatar_gradient,
		  model, persona, skills, tools, team, system, temperature, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		 ON CONFLICT (name) DO UPDATE SET
		   display_name    = EXCLUDED.display_name,
		   title           = EXCLUDED.title,
		   description     = EXCLUDED.description,
		   avatar          = EXCLUDED.avatar,
		   icon            = EXCLUDED.icon,
		   avatar_gradient = EXCLUDED.avatar_gradient,
		   model           = EXCLUDED.model,
		   persona         = EXCLUDED.persona,
		   skills          = EXCLUDED.skills,
		   tools           = EXCLUDED.tools,
		   team            = EXCLUDED.team,
		   system          = EXCLUDED.system,
		   temperature     = EXCLUDED.temperature,
		   updated_at      = EXCLUDED.updated_at`,
		a.Name, a.DisplayName, a.Title, a.Description, a.Avatar, a.Icon, string(gradJSON),
		a.Model, a.Persona, string(skillsJSON), string(toolsJSON), string(teamJSON),
		a.System, a.Temperature, a.CreatedAt, a.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) ListComposedAgents() ([]ComposedAgent, error) {
	rows, err := s.db.Query(
		`SELECT name, display_name, title, description, avatar, icon, avatar_gradient,
		        model, persona, skills, tools, team, system, temperature, created_at, updated_at
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
		if err := rows.Scan(&a.Name, &a.DisplayName, &a.Title, &a.Description, &a.Avatar, &a.Icon,
			&gradJSON, &a.Model, &a.Persona, &skillsJSON, &toolsJSON, &teamJSON,
			&a.System, &temp, &a.CreatedAt, &updatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(skillsJSON), &a.Skills)
		_ = json.Unmarshal([]byte(toolsJSON), &a.Tools)
		_ = json.Unmarshal([]byte(teamJSON), &a.Team)
		_ = json.Unmarshal([]byte(gradJSON), &a.AvatarGradient)
		if temp.Valid {
			a.Temperature = &temp.Float64
		}
		if updatedAt.Valid {
			a.UpdatedAt = updatedAt.Time
		} else {
			a.UpdatedAt = a.CreatedAt
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *PostgresStore) DeleteComposedAgent(name string) error {
	res, err := s.db.Exec(`DELETE FROM composed_agents WHERE name = $1`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Chat messages ---

func (s *PostgresStore) InsertChatMessage(agent, role, content string, activities []vega.ToolActivity) error {
	activitiesJSON := []byte("[]")
	if len(activities) > 0 {
		if b, err := json.Marshal(activities); err == nil {
			activitiesJSON = b
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO chat_messages (agent, role, content, tool_activities)
		 VALUES ($1, $2, $3, $4)`,
		agent, role, content, string(activitiesJSON),
	)
	return err
}

func (s *PostgresStore) ListChatMessages(agent string) ([]ChatMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, tool_activities, created_at
		 FROM chat_messages WHERE agent = $1 ORDER BY id ASC`,
		agent,
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

func (s *PostgresStore) DeleteChatMessages(agent string) error {
	_, err := s.db.Exec(`DELETE FROM chat_messages WHERE agent = $1`, agent)
	return err
}

// --- User memory ---

func (s *PostgresStore) UpsertUserMemory(userID, agent, layer, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_memory (user_id, agent, layer, content, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT (user_id, agent, layer) DO UPDATE SET
		   content    = EXCLUDED.content,
		   updated_at = CURRENT_TIMESTAMP`,
		userID, agent, layer, content,
	)
	return err
}

func (s *PostgresStore) GetUserMemory(userID, agent string) ([]UserMemory, error) {
	rows, err := s.db.Query(
		`SELECT layer, content, created_at, updated_at
		 FROM user_memory WHERE user_id = $1 AND agent = $2`,
		userID, agent,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserMemory
	for rows.Next() {
		var m UserMemory
		m.UserID = userID
		m.Agent = agent
		if err := rows.Scan(&m.Layer, &m.Content, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteUserMemory(userID, agent string) error {
	_, err := s.db.Exec(`DELETE FROM user_memory WHERE user_id = $1 AND agent = $2`, userID, agent)
	return err
}

// --- Scheduled jobs ---

func (s *PostgresStore) UpsertScheduledJob(job ScheduledJob) error {
	if job.ID == "" {
		job.ID = job.Name
	}
	_, err := s.db.Exec(
		`INSERT INTO scheduled_jobs
		 (id, name, title, cron, agent_name, message, schedule_json, enabled, last_run_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT (id) DO UPDATE SET
		   name          = EXCLUDED.name,
		   title         = EXCLUDED.title,
		   cron          = EXCLUDED.cron,
		   agent_name    = EXCLUDED.agent_name,
		   message       = EXCLUDED.message,
		   schedule_json = EXCLUDED.schedule_json,
		   enabled       = EXCLUDED.enabled,
		   last_run_at   = EXCLUDED.last_run_at,
		   updated_at    = CURRENT_TIMESTAMP`,
		job.ID, job.Name, job.Title, job.Cron, job.AgentName, job.Message,
		job.ScheduleJSON, job.Enabled, job.LastRunAt,
	)
	return err
}

func (s *PostgresStore) DeleteScheduledJob(name string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_jobs WHERE name = $1`, name)
	return err
}

func (s *PostgresStore) GetScheduledJobByID(id string) (*ScheduledJob, error) {
	row := s.db.QueryRow(
		`SELECT id, name, title, cron, agent_name, message, schedule_json,
		        enabled, last_run_at, created_at, updated_at
		 FROM scheduled_jobs WHERE id = $1`, id,
	)
	var j ScheduledJob
	var lastRun, updated sql.NullTime
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

func (s *PostgresStore) ListScheduledJobs() ([]ScheduledJob, error) {
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
		var lastRun, updated sql.NullTime
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

func (s *PostgresStore) MarkScheduledJobRun(name string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE scheduled_jobs SET last_run_at = $1 WHERE name = $2`, at, name)
	return err
}

// --- Spend rollup ---

func (s *PostgresStore) AgentSpendInPeriod(agentName string, from, to time.Time) (float64, error) {
	q := `
SELECT COALESCE(SUM(cost_usd), 0)
FROM process_snapshots ps
JOIN (
    SELECT process_id, MAX(id) AS max_id
    FROM process_snapshots
    WHERE agent_name = $1
    GROUP BY process_id
) latest ON ps.id = latest.max_id`
	args := []any{agentName}
	pIdx := 2
	if !from.IsZero() {
		q += fmt.Sprintf(` WHERE COALESCE(ps.started_at, ps.snapshot_at) >= $%d`, pIdx)
		args = append(args, from)
		pIdx++
		if !to.IsZero() {
			q += fmt.Sprintf(` AND COALESCE(ps.started_at, ps.snapshot_at) < $%d`, pIdx)
			args = append(args, to)
		}
	} else if !to.IsZero() {
		q += fmt.Sprintf(` WHERE COALESCE(ps.started_at, ps.snapshot_at) < $%d`, pIdx)
		args = append(args, to)
	}
	var total float64
	if err := s.db.QueryRow(q, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// --- Brain files ---

func (s *PostgresStore) InsertAgentBrainFile(f AgentBrainFile) error {
	_, err := s.db.Exec(
		`INSERT INTO agent_brain_files (id, agent_name, name, mime_type, size_bytes, content, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)`,
		f.ID, f.AgentName, f.Name, f.MimeType, f.SizeBytes, f.Content,
	)
	return err
}

func (s *PostgresStore) ListAgentBrainFiles(agentName string) ([]AgentBrainFile, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_name, name, mime_type, size_bytes, created_at
		 FROM agent_brain_files WHERE agent_name = $1 ORDER BY created_at ASC`,
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

func (s *PostgresStore) GetAgentBrainFile(agentName, id string) (*AgentBrainFile, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_name, name, mime_type, size_bytes, content, created_at
		 FROM agent_brain_files WHERE id = $1 AND agent_name = $2`,
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

func (s *PostgresStore) DeleteAgentBrainFile(agentName, id string) error {
	res, err := s.db.Exec(`DELETE FROM agent_brain_files WHERE id = $1 AND agent_name = $2`, id, agentName)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Settings ---

func (s *PostgresStore) UpsertSetting(st Setting) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value, sensitive, created_at, updated_at)
		 VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT (key) DO UPDATE SET
		   value      = EXCLUDED.value,
		   sensitive  = EXCLUDED.sensitive,
		   updated_at = CURRENT_TIMESTAMP`,
		st.Key, st.Value, st.Sensitive,
	)
	return err
}

func (s *PostgresStore) GetSetting(key string) (*Setting, error) {
	var st Setting
	err := s.db.QueryRow(
		`SELECT key, value, sensitive, created_at, updated_at FROM settings WHERE key = $1`, key,
	).Scan(&st.Key, &st.Value, &st.Sensitive, &st.CreatedAt, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *PostgresStore) ListSettings() ([]Setting, error) {
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

func (s *PostgresStore) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = $1`, key)
	return err
}

// --- Workspace files ---

func (s *PostgresStore) InsertWorkspaceFile(f WorkspaceFile) error {
	_, err := s.db.Exec(
		`INSERT INTO workspace_files (path, agent, process_id, operation, description)
		 VALUES ($1, $2, $3, $4, $5)`,
		f.Path, f.Agent, f.ProcessID, f.Operation, f.Description,
	)
	return err
}

func (s *PostgresStore) ListWorkspaceFiles(agent string) ([]WorkspaceFile, error) {
	q := `SELECT id, path, agent, process_id, operation, description, created_at FROM workspace_files`
	var rows *sql.Rows
	var err error
	if agent != "" {
		rows, err = s.db.Query(q+` WHERE agent = $1 ORDER BY id DESC`, agent)
	} else {
		rows, err = s.db.Query(q + ` ORDER BY id DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceFile
	for rows.Next() {
		var f WorkspaceFile
		if err := rows.Scan(&f.ID, &f.Path, &f.Agent, &f.ProcessID, &f.Operation, &f.Description, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListWorkspaceFileAgents() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT agent FROM workspace_files WHERE agent <> '' ORDER BY agent ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// nullableTime returns the time wrapped in sql.NullTime so a zero time
// stays NULL rather than becoming the year-1 epoch — Postgres rejects
// the latter when started_at is constrained.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return t
}

// --- Memory items ---

func (s *PostgresStore) InsertMemoryItem(item MemoryItem) (int64, error) {
	if item.Type == "" {
		item.Type = MemoryTypeReference
	}
	// Dedup on (user_id, agent, type, content) — merge tags if a match exists.
	var existingID int64
	var existingTags string
	err := s.db.QueryRow(
		`SELECT id, tags FROM memory_items
		 WHERE user_id = $1 AND agent = $2 AND type = $3 AND content = $4
		 LIMIT 1`,
		item.UserID, item.Agent, string(item.Type), item.Content,
	).Scan(&existingID, &existingTags)
	switch {
	case err == nil:
		merged := mergeTags(existingTags, item.Tags)
		if _, err := s.db.Exec(
			`UPDATE memory_items SET tags = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`,
			merged, existingID,
		); err != nil {
			return 0, err
		}
		return existingID, nil
	case err == sql.ErrNoRows:
		// fall through to insert
	default:
		return 0, err
	}
	var newID int64
	err = s.db.QueryRow(
		`INSERT INTO memory_items (user_id, agent, type, topic, content, tags)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		item.UserID, item.Agent, string(item.Type), item.Topic, item.Content, item.Tags,
	).Scan(&newID)
	return newID, err
}

func (s *PostgresStore) SearchMemoryItems(userID, agent, query string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items
		 WHERE user_id = $1 AND agent = $2
		   AND (topic LIKE $3 OR content LIKE $3 OR tags LIKE $3)
		 ORDER BY updated_at DESC LIMIT $4`,
		userID, agent, pattern, limit,
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

func (s *PostgresStore) SearchMemoryItemsByType(userID, agent, query string, typ MemoryType, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items
		 WHERE user_id = $1 AND agent = $2 AND type = $3
		   AND (topic LIKE $4 OR content LIKE $4 OR tags LIKE $4)
		 ORDER BY updated_at DESC LIMIT $5`,
		userID, agent, string(typ), pattern, limit,
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

func (s *PostgresStore) DeleteMemoryItem(id int64) error {
	res, err := s.db.Exec(`DELETE FROM memory_items WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) ListMemoryItemsByTopic(userID, agent, topic string) ([]MemoryItem, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, agent, type, topic, content, tags, created_at, updated_at
		 FROM memory_items
		 WHERE user_id = $1 AND agent = $2 AND topic = $3
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

// --- Channels ---

func (s *PostgresStore) CreateChannel(id, name, description, createdBy string, team []string, mode string) error {
	teamJSON, _ := json.Marshal(team)
	_, err := s.db.Exec(
		`INSERT INTO channels (id, name, description, team, mode, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, name, description, string(teamJSON), mode, createdBy,
	)
	return err
}

func (s *PostgresStore) GetChannel(name string) (*Channel, error) {
	var ch Channel
	var teamJSON string
	var updatedAt sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, name, description, team, mode, created_by, created_at, updated_at
		 FROM channels WHERE name = $1`, name,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &teamJSON, &ch.Mode, &ch.CreatedBy, &ch.CreatedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(teamJSON), &ch.Team)
	if updatedAt.Valid {
		ch.UpdatedAt = updatedAt.Time
	} else {
		ch.UpdatedAt = ch.CreatedAt
	}
	return &ch, nil
}

func (s *PostgresStore) GetChannelByName(name string) (*dsl.ChannelInfo, error) {
	ch, err := s.GetChannel(name)
	if err != nil || ch == nil {
		return nil, err
	}
	return &dsl.ChannelInfo{ID: ch.ID, Name: ch.Name, Team: ch.Team}, nil
}

func (s *PostgresStore) ListAllChannels() ([]dsl.ChannelInfo, error) {
	channels, err := s.ListChannels("default")
	if err != nil {
		return nil, err
	}
	out := make([]dsl.ChannelInfo, len(channels))
	for i, ch := range channels {
		out[i] = dsl.ChannelInfo{ID: ch.ID, Name: ch.Name, Team: ch.Team}
	}
	return out, nil
}

func (s *PostgresStore) ListChannelsForAgent(agent string) ([]dsl.ChannelInfo, error) {
	channels, err := s.ListChannels("default")
	if err != nil {
		return nil, err
	}
	var out []dsl.ChannelInfo
	for _, ch := range channels {
		for _, m := range ch.Team {
			if m == agent {
				out = append(out, dsl.ChannelInfo{ID: ch.ID, Name: ch.Name, Team: ch.Team})
				break
			}
		}
	}
	return out, nil
}

func (s *PostgresStore) ListChannels(userID string) ([]Channel, error) {
	if userID == "" {
		userID = "default"
	}
	rows, err := s.db.Query(`
		SELECT c.id, c.name, c.description, c.team, c.mode, c.created_by, c.created_at, c.updated_at,
		       COALESCE((SELECT COUNT(*) FROM channel_messages WHERE channel_id = c.id AND thread_id IS NULL), 0),
		       COALESCE((SELECT COUNT(*) FROM channel_messages WHERE channel_id = c.id AND thread_id IS NULL
		                 AND id > COALESCE((SELECT last_read_id FROM channel_read_cursors WHERE channel_id = c.id AND user_id = $1), 0)), 0)
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
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Description, &teamJSON, &ch.Mode, &ch.CreatedBy,
			&ch.CreatedAt, &updatedAt, &ch.MessageCount, &ch.UnreadCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(teamJSON), &ch.Team)
		if updatedAt.Valid {
			ch.UpdatedAt = updatedAt.Time
		} else {
			ch.UpdatedAt = ch.CreatedAt
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *PostgresStore) DeleteChannel(name string) error {
	var id string
	err := s.db.QueryRow(`SELECT id FROM channels WHERE name = $1`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`DELETE FROM channel_messages WHERE channel_id = $1`, id)
	res, err := s.db.Exec(`DELETE FROM channels WHERE name = $1`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) UpdateChannelTeam(name string, team []string) error {
	teamJSON, _ := json.Marshal(team)
	res, err := s.db.Exec(
		`UPDATE channels SET team = $1, updated_at = CURRENT_TIMESTAMP WHERE name = $2`,
		string(teamJSON), name,
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

func (s *PostgresStore) UpdateChannelMeta(currentName string, newName, newDescription *string) error {
	if newName == nil && newDescription == nil {
		var exists int
		err := s.db.QueryRow(`SELECT 1 FROM channels WHERE name = $1`, currentName).Scan(&exists)
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return err
	}
	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	next := func() string { return fmt.Sprintf("$%d", len(args)) }
	if newName != nil {
		args = append(args, *newName)
		sets = append(sets, "name = "+next())
	}
	if newDescription != nil {
		args = append(args, *newDescription)
		sets = append(sets, "description = "+next())
	}
	args = append(args, currentName)
	res, err := s.db.Exec(`UPDATE channels SET `+strings.Join(sets, ", ")+` WHERE name = `+next(), args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) FindChannelForAgents(agent1, agent2 string) (string, string, error) {
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
		_ = json.Unmarshal([]byte(teamJSON), &team)
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

// --- Channel messages ---

func (s *PostgresStore) InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string, activities []vega.ToolActivity) (int64, error) {
	if metadata == "" {
		metadata = "{}"
	}
	activitiesJSON := []byte("[]")
	if len(activities) > 0 {
		if b, err := json.Marshal(activities); err == nil {
			activitiesJSON = b
		}
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO channel_messages (channel_id, thread_id, agent, role, content, metadata, sender, tool_activities)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		channelID, threadID, agent, role, content, metadata, sender, string(activitiesJSON),
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) ListChannelMessages(channelID string, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	// Postgres uses string_agg instead of SQLite's GROUP_CONCAT.
	rows, err := s.db.Query(
		`SELECT m.id, m.channel_id, m.thread_id, m.agent, m.sender, m.role, m.content, m.metadata, m.tool_activities, m.created_at,
		        COALESCE((SELECT COUNT(*) FROM channel_messages r WHERE r.thread_id = m.id), 0) AS reply_count,
		        (SELECT MAX(created_at) FROM channel_messages r WHERE r.thread_id = m.id) AS latest_reply_at,
		        (SELECT string_agg(DISTINCT COALESCE(NULLIF(sender, ''), agent), ',')
		           FROM channel_messages r WHERE r.thread_id = m.id) AS reply_senders
		 FROM channel_messages m
		 WHERE m.channel_id = $1 AND m.thread_id IS NULL
		 ORDER BY m.created_at ASC LIMIT $2`,
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
		if err := rows.Scan(&m.ID, &m.ChannelID, &threadID, &m.Agent, &m.Sender, &m.Role, &m.Content, &m.Metadata,
			&activitiesJSON, &m.CreatedAt, &m.ReplyCount, &latestReplyAt, &replySenders); err != nil {
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

func (s *PostgresStore) RecentChannelMessages(channelID string, limit int) ([]dsl.ChannelMessage, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.Query(
		`SELECT agent, sender, content FROM channel_messages
		 WHERE channel_id = $1 AND thread_id IS NULL
		 ORDER BY created_at DESC LIMIT $2`,
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
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

func (s *PostgresStore) ListThreadMessages(channelID string, threadID int64) ([]ChannelMessage, error) {
	rows, err := s.db.Query(
		`SELECT id, channel_id, thread_id, agent, sender, role, content, metadata, tool_activities, created_at
		 FROM channel_messages
		 WHERE channel_id = $1 AND (id = $2 OR thread_id = $2)
		 ORDER BY created_at ASC`,
		channelID, threadID,
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

func (s *PostgresStore) MarkChannelRead(channelID, userID string) error {
	if userID == "" {
		userID = "default"
	}
	_, err := s.db.Exec(`
		INSERT INTO channel_read_cursors (channel_id, user_id, last_read_id, updated_at)
		VALUES ($1, $2,
		    COALESCE((SELECT MAX(id) FROM channel_messages WHERE channel_id = $1 AND thread_id IS NULL), 0),
		    CURRENT_TIMESTAMP)
		ON CONFLICT (channel_id, user_id) DO UPDATE SET
		    last_read_id = COALESCE((SELECT MAX(id) FROM channel_messages WHERE channel_id = EXCLUDED.channel_id AND thread_id IS NULL), 0),
		    updated_at = CURRENT_TIMESTAMP`,
		channelID, userID,
	)
	return err
}

func (s *PostgresStore) MarkChatRead(agent, userID string) error {
	if userID == "" {
		userID = "default"
	}
	_, err := s.db.Exec(`
		INSERT INTO chat_read_cursors (agent, user_id, last_read_id, updated_at)
		VALUES ($1, $2,
		    COALESCE((SELECT MAX(id) FROM chat_messages WHERE agent = $1), 0),
		    CURRENT_TIMESTAMP)
		ON CONFLICT (agent, user_id) DO UPDATE SET
		    last_read_id = COALESCE((SELECT MAX(id) FROM chat_messages WHERE agent = EXCLUDED.agent), 0),
		    updated_at = CURRENT_TIMESTAMP`,
		agent, userID,
	)
	return err
}

func (s *PostgresStore) ChatUnreadCounts(userID string) (map[string]int, error) {
	if userID == "" {
		userID = "default"
	}
	rows, err := s.db.Query(`
		SELECT cm.agent, COUNT(*) AS unread
		FROM chat_messages cm
		LEFT JOIN chat_read_cursors crc ON cm.agent = crc.agent AND crc.user_id = $1
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

// --- Inbox (agent_inbox + inbox_replies) ---

func (s *PostgresStore) InsertInboxItem(fromAgent, subject, body, priority string) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO agent_inbox (from_agent, subject, body, priority)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		fromAgent, subject, body, priority,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) ListInboxItems(status string, limit int) ([]InboxItem, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if status == "all" || status == "" {
		rows, err = s.db.Query(
			`SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at
			 FROM agent_inbox ORDER BY created_at DESC LIMIT $1`, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at
			 FROM agent_inbox WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	}
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

func (s *PostgresStore) GetInboxItem(id int64) (*InboxItem, error) {
	row := s.db.QueryRow(
		`SELECT id, from_agent, subject, body, priority, status, resolution, created_at, resolved_at
		 FROM agent_inbox WHERE id = $1`, id)
	var item InboxItem
	var resolution sql.NullString
	var resolvedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.FromAgent, &item.Subject, &item.Body,
		&item.Priority, &item.Status, &resolution, &item.CreatedAt, &resolvedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
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

func (s *PostgresStore) ResolveInboxItem(id int64, resolution string) error {
	res, err := s.db.Exec(
		`UPDATE agent_inbox SET status = 'resolved', resolution = $1, resolved_at = CURRENT_TIMESTAMP WHERE id = $2`,
		resolution, id,
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

func (s *PostgresStore) DeleteResolvedInboxItems() (int64, error) {
	// Cascading FK on inbox_replies handles replies cleanup.
	res, err := s.db.Exec(`DELETE FROM agent_inbox WHERE status = 'resolved'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Prompt history ---

func (s *PostgresStore) InsertPromptHistory(prompt string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`INSERT INTO prompt_history (prompt) VALUES ($1) RETURNING id`, prompt).Scan(&id)
	return id, err
}

func (s *PostgresStore) ListPromptHistory(limit int) ([]PromptHistoryItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, prompt, created_at FROM prompt_history ORDER BY id DESC LIMIT $1`, limit,
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

func (s *PostgresStore) SearchPromptHistory(query string, limit int) ([]PromptHistoryItem, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT id, prompt, created_at FROM prompt_history
		 WHERE prompt LIKE $1 ORDER BY id DESC LIMIT $2`,
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

func (s *PostgresStore) DeletePromptHistory(id int64) error {
	res, err := s.db.Exec(`DELETE FROM prompt_history WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Tasks ---

func (s *PostgresStore) InsertTask(t Task) error {
	if t.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if t.Title == "" {
		return fmt.Errorf("task title is required")
	}
	if t.Status == "" {
		t.Status = TaskStatusTodo
	}
	if t.Priority == "" {
		t.Priority = TaskPriorityNormal
	}
	_, err := s.db.Exec(
		`INSERT INTO tasks (id, title, description, status, priority, assignee, tags, created_by, due_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, t.Tags, t.CreatedBy, t.DueAt,
	)
	return err
}

func (s *PostgresStore) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(
		`SELECT id, title, description, status, priority, assignee, tags, created_by, created_at, updated_at, due_at
		 FROM tasks WHERE id = $1`, id,
	)
	t := &Task{}
	var due sql.NullTime
	if err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.Tags, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &due); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if due.Valid {
		t.DueAt = &due.Time
	}
	return t, nil
}

func (s *PostgresStore) ListTasks(f TaskFilter) ([]Task, error) {
	q := `SELECT id, title, description, status, priority, assignee, tags, created_by, created_at, updated_at, due_at FROM tasks`
	var clauses []string
	var args []any
	next := func() string { return fmt.Sprintf("$%d", len(args)) }
	if len(f.Status) > 0 {
		placeholders := make([]string, len(f.Status))
		for i, v := range f.Status {
			args = append(args, v)
			placeholders[i] = next()
		}
		clauses = append(clauses, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(f.Assignee) > 0 {
		placeholders := make([]string, len(f.Assignee))
		for i, v := range f.Assignee {
			args = append(args, v)
			placeholders[i] = next()
		}
		clauses = append(clauses, "assignee IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(f.Tag) > 0 {
		var tagClauses []string
		for _, tg := range f.Tag {
			args = append(args, "%,"+tg+",%")
			tagClauses = append(tagClauses, fmt.Sprintf("(',' || tags || ',') LIKE %s", next()))
		}
		clauses = append(clauses, "("+strings.Join(tagClauses, " OR ")+")")
	}
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY updated_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t := Task{}
		var due sql.NullTime
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.Tags, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &due); err != nil {
			return nil, err
		}
		if due.Valid {
			t.DueAt = &due.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateTask(id string, u TaskUpdate) error {
	if u.Status != nil && !validTaskStatus(*u.Status) {
		return fmt.Errorf("invalid status %q", *u.Status)
	}
	var sets []string
	var args []any
	next := func() string { return fmt.Sprintf("$%d", len(args)) }
	if u.Title != nil {
		args = append(args, *u.Title)
		sets = append(sets, "title = "+next())
	}
	if u.Description != nil {
		args = append(args, *u.Description)
		sets = append(sets, "description = "+next())
	}
	if u.Status != nil {
		args = append(args, *u.Status)
		sets = append(sets, "status = "+next())
	}
	if u.Priority != nil {
		args = append(args, *u.Priority)
		sets = append(sets, "priority = "+next())
	}
	if u.Assignee != nil {
		args = append(args, *u.Assignee)
		sets = append(sets, "assignee = "+next())
	}
	if u.Tags != nil {
		args = append(args, *u.Tags)
		sets = append(sets, "tags = "+next())
	}
	if u.DueAt != nil {
		args = append(args, *u.DueAt)
		sets = append(sets, "due_at = "+next())
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	res, err := s.db.Exec("UPDATE tasks SET "+strings.Join(sets, ", ")+" WHERE id = "+next(), args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) DeleteTask(id string) error {
	// ON DELETE CASCADE on the FK handles comments + processes.
	res, err := s.db.Exec(`DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) AddTaskComment(taskID, author, content string) (int64, error) {
	if content == "" {
		return 0, fmt.Errorf("comment content is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO task_comments (task_id, author, content) VALUES ($1, $2, $3) RETURNING id`,
		taskID, author, content,
	).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = $1`, taskID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *PostgresStore) ListTaskComments(taskID string) ([]TaskComment, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, author, content, created_at
		 FROM task_comments WHERE task_id = $1 ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskComment{}
	for rows.Next() {
		c := TaskComment{}
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Author, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PostgresStore) LinkTaskProcess(taskID, processID string) error {
	_, err := s.db.Exec(
		`INSERT INTO task_processes (task_id, process_id) VALUES ($1, $2)
		 ON CONFLICT (task_id, process_id) DO NOTHING`,
		taskID, processID,
	)
	return err
}

func (s *PostgresStore) ListTaskProcesses(taskID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT process_id FROM task_processes WHERE task_id = $1 ORDER BY created_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListMyTasks(assignee string, status []string, limit int) ([]Task, error) {
	return s.ListTasks(TaskFilter{Assignee: []string{assignee}, Status: status, Limit: limit})
}

func (s *PostgresStore) ListUnassignedTasks(limit int) ([]Task, error) {
	return s.ListTasks(TaskFilter{Assignee: []string{""}, Limit: limit})
}

func (s *PostgresStore) UpdateTaskStatus(id, status string) error {
	return s.UpdateTask(id, TaskUpdate{Status: &status})
}

func (s *PostgresStore) AssignTask(id, assignee string) error {
	return s.UpdateTask(id, TaskUpdate{Assignee: &assignee})
}

func (s *PostgresStore) ClaimTask(id, assignee string) error {
	doing := TaskStatusDoing
	return s.UpdateTask(id, TaskUpdate{Assignee: &assignee, Status: &doing})
}

func (s *PostgresStore) TaskStatsByAssignee() (map[string]AgentStatsResponse, error) {
	rows, err := s.db.Query(
		`SELECT assignee, status, COUNT(*) FROM tasks WHERE assignee <> '' GROUP BY assignee, status`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type counts struct{ assigned, done, canceled int }
	raw := make(map[string]*counts)
	for rows.Next() {
		var assignee, status string
		var n int
		if err := rows.Scan(&assignee, &status, &n); err != nil {
			return nil, err
		}
		c, ok := raw[assignee]
		if !ok {
			c = &counts{}
			raw[assignee] = c
		}
		switch status {
		case TaskStatusTodo, TaskStatusDoing, TaskStatusBlocked:
			c.assigned += n
		case TaskStatusDone:
			c.done += n
		case TaskStatusCanceled:
			c.canceled += n
		}
	}
	out := make(map[string]AgentStatsResponse, len(raw))
	for assignee, c := range raw {
		stats := AgentStatsResponse{AssignedTasks: c.assigned, CompletedTasks: c.done}
		if c.done+c.canceled > 0 {
			rate := float64(c.done) / float64(c.done+c.canceled)
			stats.SuccessRate = &rate
		}
		out[assignee] = stats
	}
	return out, rows.Err()
}

// --- Reset ---

// ResetData clears every transient table but preserves settings + mcp_servers.
// Mirrors SQLiteStore.ResetData behavior.
func (s *PostgresStore) ResetData() error {
	// TRUNCATE ... CASCADE handles FK chains in one go.
	_, err := s.db.Exec(`
TRUNCATE TABLE
    composed_agents, chat_messages, user_memory, memory_items,
    events, process_snapshots, workflow_runs, scheduled_jobs,
    channel_messages, channels, inbox_replies, agent_inbox,
    workspace_files, channel_read_cursors, chat_read_cursors,
    task_processes, task_comments, tasks
RESTART IDENTITY CASCADE`)
	return err
}

// Compile-time assertion that PostgresStore satisfies Store.
var _ Store = (*PostgresStore)(nil)
