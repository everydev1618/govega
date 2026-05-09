package serve

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task is a unit of work that lives independently of any single agent run.
// A Process executes; a Task is something a human (or, later, the orchestrator)
// chooses to work on. One Task may be fulfilled by zero or many Processes.
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	Assignee    string     `json:"assignee"`
	Tags        string     `json:"tags"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// TaskComment is an entry on a task's activity feed.
type TaskComment struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskUpdate is a partial-update payload — nil pointer = unchanged.
type TaskUpdate struct {
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	Status      *string    `json:"status,omitempty"`
	Priority    *string    `json:"priority,omitempty"`
	Assignee    *string    `json:"assignee,omitempty"`
	Tags        *string    `json:"tags,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// TaskFilter narrows ListTasks; an empty filter returns all tasks.
type TaskFilter struct {
	Status   []string
	Assignee []string
	Tag      []string // matches if task tags overlap any of these
	Limit    int      // 0 = no limit
	Offset   int
}

// Task status / priority constants. Free-form strings are accepted by the
// store on insert (so apps can extend), but UpdateTask validates the enum to
// catch typos in the active path agents use most.
const (
	TaskStatusTodo     = "todo"
	TaskStatusDoing    = "doing"
	TaskStatusBlocked  = "blocked"
	TaskStatusDone     = "done"
	TaskStatusCanceled = "canceled"
)

const (
	TaskPriorityLow    = "low"
	TaskPriorityNormal = "normal"
	TaskPriorityHigh   = "high"
	TaskPriorityUrgent = "urgent"
)

func validTaskStatus(s string) bool {
	switch s {
	case TaskStatusTodo, TaskStatusDoing, TaskStatusBlocked, TaskStatusDone, TaskStatusCanceled:
		return true
	}
	return false
}

// initTaskSchema creates the tasks/comments/links tables. Called from Init().
func (s *SQLiteStore) initTaskSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id          TEXT PRIMARY KEY,
		title       TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'todo',
		priority    TEXT NOT NULL DEFAULT 'normal',
		assignee    TEXT NOT NULL DEFAULT '',
		tags        TEXT NOT NULL DEFAULT '',
		created_by  TEXT NOT NULL DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		due_at      DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_tasks_status   ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);

	CREATE TABLE IF NOT EXISTS task_comments (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id    TEXT NOT NULL,
		author     TEXT NOT NULL DEFAULT '',
		content    TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_task_comments_task ON task_comments(task_id, created_at);

	CREATE TABLE IF NOT EXISTS task_processes (
		task_id    TEXT NOT NULL,
		process_id TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (task_id, process_id),
		FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

// InsertTask creates a new task. If Status/Priority are empty, defaults apply
// (todo / normal). CreatedAt and UpdatedAt are set by SQLite if not provided.
func (s *SQLiteStore) InsertTask(t Task) error {
	if t.ID == "" {
		return errors.New("task id is required")
	}
	if t.Title == "" {
		return errors.New("task title is required")
	}
	if t.Status == "" {
		t.Status = TaskStatusTodo
	}
	if t.Priority == "" {
		t.Priority = TaskPriorityNormal
	}

	_, err := s.db.Exec(
		`INSERT INTO tasks (id, title, description, status, priority, assignee, tags, created_by, due_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.Status, t.Priority, t.Assignee, t.Tags, t.CreatedBy, t.DueAt,
	)
	return err
}

// GetTask returns a task by id, or (nil, nil) if not found.
func (s *SQLiteStore) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(
		`SELECT id, title, description, status, priority, assignee, tags, created_by, created_at, updated_at, due_at
		 FROM tasks WHERE id = ?`, id,
	)
	t := &Task{}
	var due sql.NullTime
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee, &t.Tags, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if due.Valid {
		t.DueAt = &due.Time
	}
	return t, nil
}

// ListTasks returns tasks matching the filter, newest-updated first.
func (s *SQLiteStore) ListTasks(f TaskFilter) ([]Task, error) {
	q := `SELECT id, title, description, status, priority, assignee, tags, created_by, created_at, updated_at, due_at
	      FROM tasks`
	var clauses []string
	var args []any

	if len(f.Status) > 0 {
		clauses = append(clauses, "status IN ("+placeholders(len(f.Status))+")")
		for _, v := range f.Status {
			args = append(args, v)
		}
	}
	if len(f.Assignee) > 0 {
		clauses = append(clauses, "assignee IN ("+placeholders(len(f.Assignee))+")")
		for _, v := range f.Assignee {
			args = append(args, v)
		}
	}
	if len(f.Tag) > 0 {
		// tags is a CSV; substring match per tag is good enough for v1
		// (escape commas to anchor). Apps with many tags should switch to
		// a join table later.
		var tagClauses []string
		for _, tg := range f.Tag {
			tagClauses = append(tagClauses, "(','||tags||',') LIKE ?")
			args = append(args, "%,"+tg+",%")
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

// UpdateTask applies a partial update. Returns sql.ErrNoRows-equivalent if
// the id doesn't exist (caller maps to 404). Validates the status enum.
func (s *SQLiteStore) UpdateTask(id string, u TaskUpdate) error {
	if u.Status != nil && !validTaskStatus(*u.Status) {
		return fmt.Errorf("invalid status %q", *u.Status)
	}

	var sets []string
	var args []any
	if u.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *u.Title)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *u.Status)
	}
	if u.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *u.Priority)
	}
	if u.Assignee != nil {
		sets = append(sets, "assignee = ?")
		args = append(args, *u.Assignee)
	}
	if u.Tags != nil {
		sets = append(sets, "tags = ?")
		args = append(args, *u.Tags)
	}
	if u.DueAt != nil {
		sets = append(sets, "due_at = ?")
		args = append(args, *u.DueAt)
	}
	if len(sets) == 0 {
		return nil // no-op
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)

	q := "UPDATE tasks SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteTask removes a task and its comments / process links. The schema
// declares ON DELETE CASCADE, but SQLite enforces FKs only when the
// per-connection `foreign_keys` pragma is on — this codebase doesn't enable
// it globally, so we cascade by hand inside a transaction.
func (s *SQLiteStore) DeleteTask(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM task_comments WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM task_processes WHERE task_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// AddTaskComment appends a comment and bumps the task's updated_at so list
// ordering reflects activity.
func (s *SQLiteStore) AddTaskComment(taskID, author, content string) (int64, error) {
	if content == "" {
		return 0, errors.New("comment content is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO task_comments (task_id, author, content) VALUES (?, ?, ?)`,
		taskID, author, content,
	)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, taskID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListTaskComments returns comments oldest-first (chronological).
func (s *SQLiteStore) ListTaskComments(taskID string) ([]TaskComment, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, author, content, created_at
		 FROM task_comments WHERE task_id = ? ORDER BY created_at ASC, id ASC`,
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

// ListMyTasks returns tasks assigned to a specific agent, optionally
// filtered by status. Convenience wrapper over ListTasks for the agent
// tool layer; ordering matches ListTasks (newest-updated first).
func (s *SQLiteStore) ListMyTasks(assignee string, status []string, limit int) ([]Task, error) {
	return s.ListTasks(TaskFilter{
		Assignee: []string{assignee},
		Status:   status,
		Limit:    limit,
	})
}

// ListUnassignedTasks returns tasks with empty assignee — the routing
// queue the orchestrator triages.
func (s *SQLiteStore) ListUnassignedTasks(limit int) ([]Task, error) {
	return s.ListTasks(TaskFilter{
		Assignee: []string{""},
		Limit:    limit,
	})
}

// UpdateTaskStatus is a focused setter used by the agent tool layer. Wraps
// UpdateTask so the validation lives in one place.
func (s *SQLiteStore) UpdateTaskStatus(id, status string) error {
	return s.UpdateTask(id, TaskUpdate{Status: &status})
}

// AssignTask sets the assignee without changing status. Used by the
// orchestrator to route an unassigned task to a worker; the worker
// decides when to start (claim_task → status='doing').
func (s *SQLiteStore) AssignTask(id, assignee string) error {
	return s.UpdateTask(id, TaskUpdate{Assignee: &assignee})
}

// ClaimTask atomically assigns a task to the caller and moves it to
// 'doing'. The two updates happen in one transaction so a heartbeat
// crash mid-claim can't leave the board in a half-state.
func (s *SQLiteStore) ClaimTask(id, assignee string) error {
	doing := TaskStatusDoing
	return s.UpdateTask(id, TaskUpdate{Assignee: &assignee, Status: &doing})
}

// LinkTaskProcess associates a process with a task. Idempotent — re-linking
// the same process is a no-op so callers (and retries) don't have to check.
func (s *SQLiteStore) LinkTaskProcess(taskID, processID string) error {
	_, err := s.db.Exec(
		`INSERT INTO task_processes (task_id, process_id) VALUES (?, ?)
		 ON CONFLICT(task_id, process_id) DO NOTHING`,
		taskID, processID,
	)
	return err
}

// ListTaskProcesses returns process IDs linked to a task, oldest-first.
func (s *SQLiteStore) ListTaskProcesses(taskID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT process_id FROM task_processes WHERE task_id = ? ORDER BY created_at ASC`,
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

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
