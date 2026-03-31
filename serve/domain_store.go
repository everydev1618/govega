package serve

import (
	"database/sql"
	"time"
)

// ────────────────────────────────────────────────────────────────
// Domain types — agent-owned state that doesn't live in SynkedUp
// ────────────────────────────────────────────────────────────────

// Job tracks a job through the landscaping lifecycle stages.
type Job struct {
	ID              int64     `json:"id"`
	ExternalID      string    `json:"external_id,omitempty"` // SynkedUp project ID
	CustomerName    string    `json:"customer_name"`
	PropertyAddress string    `json:"property_address,omitempty"`
	JobType         string    `json:"job_type"` // patio, retaining_wall, planting, irrigation, maintenance, etc.
	Stage           string    `json:"stage"`    // lead_captured → pnl_updated (16 stages)
	OwnerAgent      string    `json:"owner_agent"`
	Notes           string    `json:"notes,omitempty"`
	EstimateTotal   float64   `json:"estimate_total,omitempty"`
	ActualTotal     float64   `json:"actual_total,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// FollowUp is a scheduled action for an agent (sales follow-up, payment reminder, etc.).
type FollowUp struct {
	ID          int64      `json:"id"`
	Agent       string     `json:"agent"`
	TargetType  string     `json:"target_type"` // lead, invoice, customer, job
	TargetName  string     `json:"target_name"` // human-readable name
	Action      string     `json:"action"`      // call, text, email, visit, reminder
	DueDate     string     `json:"due_date"`    // YYYY-MM-DD
	Status      string     `json:"status"`      // pending, done, skipped
	Notes       string     `json:"notes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ProductionRate tracks estimate accuracy for a job type.
type ProductionRate struct {
	ID                   int64     `json:"id"`
	JobType              string    `json:"job_type"`
	Unit                 string    `json:"unit"` // sq_ft, lin_ft, cu_yd, each
	EstimatedHoursPerUnit float64  `json:"estimated_hours_per_unit"`
	ActualHoursPerUnit    float64  `json:"actual_hours_per_unit"`
	JobName              string    `json:"job_name,omitempty"`
	Notes                string    `json:"notes,omitempty"`
	RecordedAt           time.Time `json:"recorded_at"`
}

// ────────────────────────────────────────────────────────────────
// SQLite implementations
// ────────────────────────────────────────────────────────────────

// InitDomainTables creates the domain-specific tables.
func (s *SQLiteStore) InitDomainTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		external_id      TEXT NOT NULL DEFAULT '',
		customer_name    TEXT NOT NULL,
		property_address TEXT NOT NULL DEFAULT '',
		job_type         TEXT NOT NULL DEFAULT '',
		stage            TEXT NOT NULL DEFAULT 'lead_captured',
		owner_agent      TEXT NOT NULL DEFAULT '',
		notes            TEXT NOT NULL DEFAULT '',
		estimate_total   REAL NOT NULL DEFAULT 0,
		actual_total     REAL NOT NULL DEFAULT 0,
		created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_stage ON jobs(stage);
	CREATE INDEX IF NOT EXISTS idx_jobs_customer ON jobs(customer_name);

	CREATE TABLE IF NOT EXISTS follow_ups (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		agent        TEXT NOT NULL,
		target_type  TEXT NOT NULL DEFAULT '',
		target_name  TEXT NOT NULL DEFAULT '',
		action       TEXT NOT NULL DEFAULT '',
		due_date     TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL DEFAULT 'pending',
		notes        TEXT NOT NULL DEFAULT '',
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		completed_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_follow_ups_agent ON follow_ups(agent, status);
	CREATE INDEX IF NOT EXISTS idx_follow_ups_due ON follow_ups(due_date, status);

	CREATE TABLE IF NOT EXISTS production_rates (
		id                      INTEGER PRIMARY KEY AUTOINCREMENT,
		job_type                TEXT NOT NULL,
		unit                    TEXT NOT NULL DEFAULT '',
		estimated_hours_per_unit REAL NOT NULL DEFAULT 0,
		actual_hours_per_unit    REAL NOT NULL DEFAULT 0,
		job_name                TEXT NOT NULL DEFAULT '',
		notes                   TEXT NOT NULL DEFAULT '',
		recorded_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_production_rates_type ON production_rates(job_type);
	`
	_, err := s.db.Exec(schema)
	return err
}

// ── Jobs ────────────────────────────────────────────────────────

// InsertJob creates a new job record.
func (s *SQLiteStore) InsertJob(j Job) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO jobs (external_id, customer_name, property_address, job_type, stage, owner_agent, notes, estimate_total, actual_total)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ExternalID, j.CustomerName, j.PropertyAddress, j.JobType, j.Stage, j.OwnerAgent, j.Notes, j.EstimateTotal, j.ActualTotal,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateJobStage advances a job to a new lifecycle stage.
func (s *SQLiteStore) UpdateJobStage(id int64, stage, ownerAgent, notes string) error {
	_, err := s.db.Exec(`
		UPDATE jobs SET stage = ?, owner_agent = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, stage, ownerAgent, notes, id)
	return err
}

// UpdateJobTotals sets estimate and actual totals for job costing.
func (s *SQLiteStore) UpdateJobTotals(id int64, estimateTotal, actualTotal float64) error {
	_, err := s.db.Exec(`
		UPDATE jobs SET estimate_total = ?, actual_total = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, estimateTotal, actualTotal, id)
	return err
}

// GetJob returns a single job by ID.
func (s *SQLiteStore) GetJob(id int64) (*Job, error) {
	row := s.db.QueryRow(`SELECT id, external_id, customer_name, property_address, job_type, stage, owner_agent, notes, estimate_total, actual_total, created_at, updated_at FROM jobs WHERE id = ?`, id)
	var j Job
	err := row.Scan(&j.ID, &j.ExternalID, &j.CustomerName, &j.PropertyAddress, &j.JobType, &j.Stage, &j.OwnerAgent, &j.Notes, &j.EstimateTotal, &j.ActualTotal, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobsByStage returns jobs in a given lifecycle stage.
func (s *SQLiteStore) ListJobsByStage(stage string, limit int) ([]Job, error) {
	rows, err := s.db.Query(`
		SELECT id, external_id, customer_name, property_address, job_type, stage, owner_agent, notes, estimate_total, actual_total, created_at, updated_at
		FROM jobs WHERE stage = ? ORDER BY updated_at DESC LIMIT ?`, stage, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ListJobs returns recent jobs across all stages.
func (s *SQLiteStore) ListJobs(limit int) ([]Job, error) {
	rows, err := s.db.Query(`
		SELECT id, external_id, customer_name, property_address, job_type, stage, owner_agent, notes, estimate_total, actual_total, created_at, updated_at
		FROM jobs ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// SearchJobs searches jobs by customer name or job type.
func (s *SQLiteStore) SearchJobs(query string, limit int) ([]Job, error) {
	q := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT id, external_id, customer_name, property_address, job_type, stage, owner_agent, notes, estimate_total, actual_total, created_at, updated_at
		FROM jobs WHERE customer_name LIKE ? OR job_type LIKE ? OR notes LIKE ?
		ORDER BY updated_at DESC LIMIT ?`, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func scanJobs(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Job, error) {
	type scanner interface {
		Scan(dest ...any) error
		Next() bool
	}
	var jobs []Job
	r := rows.(scanner)
	for r.Next() {
		var j Job
		if err := r.Scan(&j.ID, &j.ExternalID, &j.CustomerName, &j.PropertyAddress, &j.JobType, &j.Stage, &j.OwnerAgent, &j.Notes, &j.EstimateTotal, &j.ActualTotal, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// ── Follow-ups ──────────────────────────────────────────────────

// InsertFollowUp creates a new follow-up action.
func (s *SQLiteStore) InsertFollowUp(f FollowUp) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO follow_ups (agent, target_type, target_name, action, due_date, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.Agent, f.TargetType, f.TargetName, f.Action, f.DueDate, f.Status, f.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CompleteFollowUp marks a follow-up as done or skipped.
func (s *SQLiteStore) CompleteFollowUp(id int64, status string) error {
	_, err := s.db.Exec(`
		UPDATE follow_ups SET status = ?, completed_at = CURRENT_TIMESTAMP
		WHERE id = ?`, status, id)
	return err
}

// ListFollowUpsDue returns pending follow-ups due on or before the given date.
func (s *SQLiteStore) ListFollowUpsDue(agent, asOfDate string, limit int) ([]FollowUp, error) {
	rows, err := s.db.Query(`
		SELECT id, agent, target_type, target_name, action, due_date, status, notes, created_at, completed_at
		FROM follow_ups WHERE status = 'pending' AND due_date <= ? AND (? = '' OR agent = ?)
		ORDER BY due_date ASC LIMIT ?`, asOfDate, agent, agent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFollowUps(rows)
}

// ListFollowUpsByAgent returns all follow-ups for an agent.
func (s *SQLiteStore) ListFollowUpsByAgent(agent, status string, limit int) ([]FollowUp, error) {
	rows, err := s.db.Query(`
		SELECT id, agent, target_type, target_name, action, due_date, status, notes, created_at, completed_at
		FROM follow_ups WHERE agent = ? AND (? = '' OR status = ?)
		ORDER BY due_date ASC LIMIT ?`, agent, status, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFollowUps(rows)
}

func scanFollowUps(rows interface{ Scan(dest ...any) error; Next() bool }) ([]FollowUp, error) {
	type scanner interface {
		Scan(dest ...any) error
		Next() bool
	}
	var items []FollowUp
	r := rows.(scanner)
	for r.Next() {
		var f FollowUp
		var completedAt sql.NullTime
		if err := r.Scan(&f.ID, &f.Agent, &f.TargetType, &f.TargetName, &f.Action, &f.DueDate, &f.Status, &f.Notes, &f.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			f.CompletedAt = &completedAt.Time
		}
		items = append(items, f)
	}
	return items, nil
}

// ── Production Rates ────────────────────────────────────────────

// InsertProductionRate records an estimate-vs-actual data point.
func (s *SQLiteStore) InsertProductionRate(p ProductionRate) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO production_rates (job_type, unit, estimated_hours_per_unit, actual_hours_per_unit, job_name, notes)
		VALUES (?, ?, ?, ?, ?, ?)`,
		p.JobType, p.Unit, p.EstimatedHoursPerUnit, p.ActualHoursPerUnit, p.JobName, p.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetProductionRates returns rate history for a job type, newest first.
func (s *SQLiteStore) GetProductionRates(jobType string, limit int) ([]ProductionRate, error) {
	rows, err := s.db.Query(`
		SELECT id, job_type, unit, estimated_hours_per_unit, actual_hours_per_unit, job_name, notes, recorded_at
		FROM production_rates WHERE job_type = ? ORDER BY recorded_at DESC LIMIT ?`, jobType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rates []ProductionRate
	for rows.Next() {
		var p ProductionRate
		if err := rows.Scan(&p.ID, &p.JobType, &p.Unit, &p.EstimatedHoursPerUnit, &p.ActualHoursPerUnit, &p.JobName, &p.Notes, &p.RecordedAt); err != nil {
			return nil, err
		}
		rates = append(rates, p)
	}
	return rates, nil
}

// GetProductionRateAverage returns the average actual hours/unit for a job type.
func (s *SQLiteStore) GetProductionRateAverage(jobType string) (avgEstimated, avgActual float64, count int, err error) {
	row := s.db.QueryRow(`
		SELECT COALESCE(AVG(estimated_hours_per_unit), 0), COALESCE(AVG(actual_hours_per_unit), 0), COUNT(*)
		FROM production_rates WHERE job_type = ?`, jobType)
	err = row.Scan(&avgEstimated, &avgActual, &count)
	return
}
