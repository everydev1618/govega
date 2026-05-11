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

// errPostgresNotImplemented is returned by the few remaining stubs that
// the rest of the Phase 3 port hasn't reached yet. Tracked in govega#61.
var errPostgresNotImplemented = fmt.Errorf("postgres: method not yet implemented (refs govega#61)")

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

// --- Stubs for the remainder; ported in follow-up commits ---

func (s *PostgresStore) InsertMemoryItem(MemoryItem) (int64, error) { return 0, errPostgresNotImplemented }
func (s *PostgresStore) SearchMemoryItems(string, string, string, int) ([]MemoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) SearchMemoryItemsByType(string, string, string, MemoryType, int) ([]MemoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteMemoryItem(int64) error { return errPostgresNotImplemented }
func (s *PostgresStore) ListMemoryItemsByTopic(string, string, string) ([]MemoryItem, error) {
	return nil, errPostgresNotImplemented
}

func (s *PostgresStore) CreateChannel(string, string, string, string, []string, string) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) GetChannel(string) (*Channel, error)        { return nil, errPostgresNotImplemented }
func (s *PostgresStore) GetChannelByName(string) (*dsl.ChannelInfo, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListAllChannels() ([]dsl.ChannelInfo, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListChannelsForAgent(string) ([]dsl.ChannelInfo, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListChannels(string) ([]Channel, error)     { return nil, errPostgresNotImplemented }
func (s *PostgresStore) DeleteChannel(string) error                 { return errPostgresNotImplemented }
func (s *PostgresStore) UpdateChannelTeam(string, []string) error   { return errPostgresNotImplemented }
func (s *PostgresStore) UpdateChannelMeta(string, *string, *string) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) FindChannelForAgents(string, string) (string, string, error) {
	return "", "", errPostgresNotImplemented
}
func (s *PostgresStore) InsertInboxItem(string, string, string, string) (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListInboxItems(string, int) ([]InboxItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) GetInboxItem(int64) (*InboxItem, error)    { return nil, errPostgresNotImplemented }
func (s *PostgresStore) ResolveInboxItem(int64, string) error      { return errPostgresNotImplemented }
func (s *PostgresStore) DeleteResolvedInboxItems() (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) InsertChannelMessage(string, string, string, string, *int64, string, string, []vega.ToolActivity) (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListChannelMessages(string, int) ([]ChannelMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) RecentChannelMessages(string, int) ([]dsl.ChannelMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListThreadMessages(string, int64) ([]ChannelMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) MarkChannelRead(string, string) error      { return errPostgresNotImplemented }
func (s *PostgresStore) MarkChatRead(string, string) error         { return errPostgresNotImplemented }
func (s *PostgresStore) ChatUnreadCounts(string) (map[string]int, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ResetData() error                          { return errPostgresNotImplemented }
func (s *PostgresStore) InsertPromptHistory(string) (int64, error) { return 0, errPostgresNotImplemented }
func (s *PostgresStore) ListPromptHistory(int) ([]PromptHistoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) SearchPromptHistory(string, int) ([]PromptHistoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeletePromptHistory(int64) error { return errPostgresNotImplemented }
func (s *PostgresStore) InsertTask(Task) error           { return errPostgresNotImplemented }
func (s *PostgresStore) GetTask(string) (*Task, error)  { return nil, errPostgresNotImplemented }
func (s *PostgresStore) ListTasks(TaskFilter) ([]Task, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) UpdateTask(string, TaskUpdate) error { return errPostgresNotImplemented }
func (s *PostgresStore) DeleteTask(string) error            { return errPostgresNotImplemented }
func (s *PostgresStore) AddTaskComment(string, string, string) (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListTaskComments(string) ([]TaskComment, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) LinkTaskProcess(string, string) error { return errPostgresNotImplemented }
func (s *PostgresStore) ListTaskProcesses(string) ([]string, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListMyTasks(string, []string, int) ([]Task, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListUnassignedTasks(int) ([]Task, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) UpdateTaskStatus(string, string) error { return errPostgresNotImplemented }
func (s *PostgresStore) AssignTask(string, string) error       { return errPostgresNotImplemented }
func (s *PostgresStore) ClaimTask(string, string) error        { return errPostgresNotImplemented }
func (s *PostgresStore) TaskStatsByAssignee() (map[string]AgentStatsResponse, error) {
	return nil, errPostgresNotImplemented
}

// Compile-time assertion that PostgresStore satisfies Store.
var _ Store = (*PostgresStore)(nil)
