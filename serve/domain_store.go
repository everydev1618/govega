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

// ────────────────────────────────────────────────────────────────
// Domain types V2
// ────────────────────────────────────────────────────────────────

// Customer represents a customer in the CRM.
type Customer struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	ContactName   string    `json:"contact_name"`
	Email         string    `json:"email"`
	Phone         string    `json:"phone"`
	Address       string    `json:"address"`
	City          string    `json:"city"`
	State         string    `json:"state"`
	Zip           string    `json:"zip"`
	Source        string    `json:"source"`
	Status        string    `json:"status"`
	Tags          string    `json:"tags"`
	Notes         string    `json:"notes"`
	PaymentMethod string    `json:"payment_method"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Property represents a customer's property.
type Property struct {
	ID           int64     `json:"id"`
	CustomerID   int64     `json:"customer_id"`
	Address      string    `json:"address"`
	City         string    `json:"city"`
	State        string    `json:"state"`
	Zip          string    `json:"zip"`
	LotSizeSqft  float64   `json:"lot_size_sqft"`
	LawnSqft     float64   `json:"lawn_sqft"`
	BedSqft      float64   `json:"bed_sqft"`
	HardscapeSqft float64  `json:"hardscape_sqft"`
	Tags         string    `json:"tags"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CrewMember represents an individual crew member.
type CrewMember struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	Phone      string    `json:"phone"`
	Email      string    `json:"email"`
	HourlyRate float64   `json:"hourly_rate"`
	Skills     string    `json:"skills"`
	Active     bool      `json:"active"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
}

// Crew represents a work crew.
type Crew struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ForemanID   int64  `json:"foreman_id"`
	MemberIDs   string `json:"member_ids"`
	Truck       string `json:"truck"`
	Specialties string `json:"specialties"`
	Active      bool   `json:"active"`
}

// Item represents a catalog item (material, service, etc.).
type Item struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Unit      string    `json:"unit"`
	Cost      float64   `json:"cost"`
	Price     float64   `json:"price"`
	Supplier  string    `json:"supplier"`
	SKU       string    `json:"sku"`
	Taxable   bool      `json:"taxable"`
	Active    bool      `json:"active"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Estimate represents a project estimate/quote.
type Estimate struct {
	ID          int64     `json:"id"`
	CustomerID  int64     `json:"customer_id"`
	PropertyID  int64     `json:"property_id"`
	JobID       int64     `json:"job_id"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	LineItems   string    `json:"line_items"`
	Subtotal    float64   `json:"subtotal"`
	Tax         float64   `json:"tax"`
	Total       float64   `json:"total"`
	MarginPct   float64   `json:"margin_pct"`
	DepositPct  float64   `json:"deposit_pct"`
	ValidUntil  string    `json:"valid_until"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CalendarEvent represents a scheduled event on the calendar.
type CalendarEvent struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	EventType  string    `json:"event_type"`
	Date       string    `json:"date"`
	StartTime  string    `json:"start_time"`
	EndTime    string    `json:"end_time"`
	CrewID     int64     `json:"crew_id"`
	JobID      int64     `json:"job_id"`
	CustomerID int64     `json:"customer_id"`
	PropertyID int64     `json:"property_id"`
	Status     string    `json:"status"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Invoice represents a customer invoice.
type Invoice struct {
	ID             int64     `json:"id"`
	InvoiceNumber  string    `json:"invoice_number"`
	CustomerID     int64     `json:"customer_id"`
	JobID          int64     `json:"job_id"`
	EstimateID     int64     `json:"estimate_id"`
	Status         string    `json:"status"`
	LineItems      string    `json:"line_items"`
	Subtotal       float64   `json:"subtotal"`
	Tax            float64   `json:"tax"`
	Total          float64   `json:"total"`
	DepositApplied float64   `json:"deposit_applied"`
	AmountDue      float64   `json:"amount_due"`
	IssuedDate     string    `json:"issued_date"`
	DueDate        string    `json:"due_date"`
	PaidDate       string    `json:"paid_date"`
	PaymentMethod  string    `json:"payment_method"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Vendor represents a supplier or subcontractor.
type Vendor struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	ContactName   string    `json:"contact_name"`
	Phone         string    `json:"phone"`
	Email         string    `json:"email"`
	Address       string    `json:"address"`
	Specialty     string    `json:"specialty"`
	PaymentTerms  string    `json:"payment_terms"`
	AccountNumber string    `json:"account_number"`
	Active        bool      `json:"active"`
	Notes         string    `json:"notes"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SalesLead represents a sales pipeline lead.
type SalesLead struct {
	ID              int64     `json:"id"`
	CustomerID      int64     `json:"customer_id"`
	Name            string    `json:"name"`
	Phone           string    `json:"phone"`
	Email           string    `json:"email"`
	Source          string    `json:"source"`
	Status          string    `json:"status"`
	EstimatedValue  float64   `json:"estimated_value"`
	JobType         string    `json:"job_type"`
	PropertyAddress string    `json:"property_address"`
	AssignedTo      string    `json:"assigned_to"`
	LostReason      string    `json:"lost_reason"`
	Notes           string    `json:"notes"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CostCode represents a cost code for budgeting.
type CostCode struct {
	ID            int64  `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Division      string `json:"division"`
	DivisionGroup string `json:"division_group"`
	Active        bool   `json:"active"`
}

// Budget represents an annual budget.
type Budget struct {
	ID              int64     `json:"id"`
	Year            int       `json:"year"`
	Name            string    `json:"name"`
	RevenueTarget   float64   `json:"revenue_target"`
	TotalOverhead   float64   `json:"total_overhead"`
	BillableHours   float64   `json:"billable_hours"`
	HourlyRate      float64   `json:"hourly_rate"`
	OwnerSalary     float64   `json:"owner_salary"`
	TargetMarginPct float64   `json:"target_margin_pct"`
	Notes           string    `json:"notes"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// BudgetLine represents a line item in a budget.
type BudgetLine struct {
	ID            int64   `json:"id"`
	BudgetID      int64   `json:"budget_id"`
	CostCode      string  `json:"cost_code"`
	Description   string  `json:"description"`
	Category      string  `json:"category"`
	AnnualAmount  float64 `json:"annual_amount"`
	MonthlyAmount float64 `json:"monthly_amount"`
	Notes         string  `json:"notes"`
}

// ────────────────────────────────────────────────────────────────
// InitDomainTablesV2 — creates the 13 new domain tables
// ────────────────────────────────────────────────────────────────

// InitDomainTablesV2 creates additional domain tables (customers, properties, crews, etc.).
func (s *SQLiteStore) InitDomainTablesV2() error {
	schema := `
	CREATE TABLE IF NOT EXISTS customers (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT NOT NULL,
		contact_name    TEXT NOT NULL DEFAULT '',
		email           TEXT NOT NULL DEFAULT '',
		phone           TEXT NOT NULL DEFAULT '',
		address         TEXT NOT NULL DEFAULT '',
		city            TEXT NOT NULL DEFAULT '',
		state           TEXT NOT NULL DEFAULT '',
		zip             TEXT NOT NULL DEFAULT '',
		source          TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'prospect',
		tags            TEXT NOT NULL DEFAULT '',
		notes           TEXT NOT NULL DEFAULT '',
		payment_method  TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_customers_status ON customers(status);
	CREATE INDEX IF NOT EXISTS idx_customers_name ON customers(name);

	CREATE TABLE IF NOT EXISTS properties (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		customer_id     INTEGER NOT NULL,
		address         TEXT NOT NULL,
		city            TEXT NOT NULL DEFAULT '',
		state           TEXT NOT NULL DEFAULT '',
		zip             TEXT NOT NULL DEFAULT '',
		lot_size_sqft   REAL NOT NULL DEFAULT 0,
		lawn_sqft       REAL NOT NULL DEFAULT 0,
		bed_sqft        REAL NOT NULL DEFAULT 0,
		hardscape_sqft  REAL NOT NULL DEFAULT 0,
		tags            TEXT NOT NULL DEFAULT '',
		notes           TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_properties_customer ON properties(customer_id);

	CREATE TABLE IF NOT EXISTS crew_members (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL,
		role        TEXT NOT NULL DEFAULT 'laborer',
		phone       TEXT NOT NULL DEFAULT '',
		email       TEXT NOT NULL DEFAULT '',
		hourly_rate REAL NOT NULL DEFAULT 0,
		skills      TEXT NOT NULL DEFAULT '',
		active      INTEGER NOT NULL DEFAULT 1,
		notes       TEXT NOT NULL DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_crew_members_role ON crew_members(role, active);

	CREATE TABLE IF NOT EXISTS crews (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL,
		foreman_id  INTEGER NOT NULL DEFAULT 0,
		member_ids  TEXT NOT NULL DEFAULT '[]',
		truck       TEXT NOT NULL DEFAULT '',
		specialties TEXT NOT NULL DEFAULT '',
		active      INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS items (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL,
		category    TEXT NOT NULL DEFAULT '',
		unit        TEXT NOT NULL DEFAULT 'each',
		cost        REAL NOT NULL DEFAULT 0,
		price       REAL NOT NULL DEFAULT 0,
		supplier    TEXT NOT NULL DEFAULT '',
		sku         TEXT NOT NULL DEFAULT '',
		taxable     INTEGER NOT NULL DEFAULT 1,
		active      INTEGER NOT NULL DEFAULT 1,
		notes       TEXT NOT NULL DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_items_category ON items(category, active);
	CREATE INDEX IF NOT EXISTS idx_items_name ON items(name);

	CREATE TABLE IF NOT EXISTS estimates (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		customer_id     INTEGER NOT NULL,
		property_id     INTEGER NOT NULL DEFAULT 0,
		job_id          INTEGER NOT NULL DEFAULT 0,
		title           TEXT NOT NULL,
		status          TEXT NOT NULL DEFAULT 'draft',
		line_items      TEXT NOT NULL DEFAULT '[]',
		subtotal        REAL NOT NULL DEFAULT 0,
		tax             REAL NOT NULL DEFAULT 0,
		total           REAL NOT NULL DEFAULT 0,
		margin_pct      REAL NOT NULL DEFAULT 0,
		deposit_pct     REAL NOT NULL DEFAULT 50,
		valid_until     TEXT NOT NULL DEFAULT '',
		notes           TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_estimates_customer ON estimates(customer_id);
	CREATE INDEX IF NOT EXISTS idx_estimates_status ON estimates(status);

	CREATE TABLE IF NOT EXISTS calendar_events (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		title       TEXT NOT NULL,
		event_type  TEXT NOT NULL DEFAULT 'job',
		date        TEXT NOT NULL,
		start_time  TEXT NOT NULL DEFAULT '',
		end_time    TEXT NOT NULL DEFAULT '',
		crew_id     INTEGER NOT NULL DEFAULT 0,
		job_id      INTEGER NOT NULL DEFAULT 0,
		customer_id INTEGER NOT NULL DEFAULT 0,
		property_id INTEGER NOT NULL DEFAULT 0,
		status      TEXT NOT NULL DEFAULT 'scheduled',
		notes       TEXT NOT NULL DEFAULT '',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_calendar_events_date ON calendar_events(date);
	CREATE INDEX IF NOT EXISTS idx_calendar_events_crew ON calendar_events(crew_id, date);

	CREATE TABLE IF NOT EXISTS invoices (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		invoice_number  TEXT NOT NULL,
		customer_id     INTEGER NOT NULL,
		job_id          INTEGER NOT NULL DEFAULT 0,
		estimate_id     INTEGER NOT NULL DEFAULT 0,
		status          TEXT NOT NULL DEFAULT 'draft',
		line_items      TEXT NOT NULL DEFAULT '[]',
		subtotal        REAL NOT NULL DEFAULT 0,
		tax             REAL NOT NULL DEFAULT 0,
		total           REAL NOT NULL DEFAULT 0,
		deposit_applied REAL NOT NULL DEFAULT 0,
		amount_due      REAL NOT NULL DEFAULT 0,
		issued_date     TEXT NOT NULL DEFAULT '',
		due_date        TEXT NOT NULL DEFAULT '',
		paid_date       TEXT NOT NULL DEFAULT '',
		payment_method  TEXT NOT NULL DEFAULT '',
		notes           TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_invoices_customer ON invoices(customer_id);
	CREATE INDEX IF NOT EXISTS idx_invoices_status ON invoices(status);

	CREATE TABLE IF NOT EXISTS vendors (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT NOT NULL,
		contact_name    TEXT NOT NULL DEFAULT '',
		phone           TEXT NOT NULL DEFAULT '',
		email           TEXT NOT NULL DEFAULT '',
		address         TEXT NOT NULL DEFAULT '',
		specialty       TEXT NOT NULL DEFAULT '',
		payment_terms   TEXT NOT NULL DEFAULT '',
		account_number  TEXT NOT NULL DEFAULT '',
		active          INTEGER NOT NULL DEFAULT 1,
		notes           TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_vendors_active ON vendors(active);

	CREATE TABLE IF NOT EXISTS sales_leads (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		customer_id      INTEGER NOT NULL DEFAULT 0,
		name             TEXT NOT NULL,
		phone            TEXT NOT NULL DEFAULT '',
		email            TEXT NOT NULL DEFAULT '',
		source           TEXT NOT NULL DEFAULT '',
		status           TEXT NOT NULL DEFAULT 'new',
		estimated_value  REAL NOT NULL DEFAULT 0,
		job_type         TEXT NOT NULL DEFAULT '',
		property_address TEXT NOT NULL DEFAULT '',
		assigned_to      TEXT NOT NULL DEFAULT '',
		lost_reason      TEXT NOT NULL DEFAULT '',
		notes            TEXT NOT NULL DEFAULT '',
		created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_sales_leads_status ON sales_leads(status);
	CREATE INDEX IF NOT EXISTS idx_sales_leads_assigned ON sales_leads(assigned_to);

	CREATE TABLE IF NOT EXISTS cost_codes (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		code            TEXT NOT NULL,
		name            TEXT NOT NULL,
		division        TEXT NOT NULL DEFAULT '',
		division_group  TEXT NOT NULL DEFAULT '',
		active          INTEGER NOT NULL DEFAULT 1
	);
	CREATE INDEX IF NOT EXISTS idx_cost_codes_division ON cost_codes(division);

	CREATE TABLE IF NOT EXISTS budgets (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		year              INTEGER NOT NULL,
		name              TEXT NOT NULL DEFAULT '',
		revenue_target    REAL NOT NULL DEFAULT 0,
		total_overhead    REAL NOT NULL DEFAULT 0,
		billable_hours    REAL NOT NULL DEFAULT 0,
		hourly_rate       REAL NOT NULL DEFAULT 0,
		owner_salary      REAL NOT NULL DEFAULT 0,
		target_margin_pct REAL NOT NULL DEFAULT 0,
		notes             TEXT NOT NULL DEFAULT '',
		created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_budgets_year ON budgets(year);

	CREATE TABLE IF NOT EXISTS budget_lines (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		budget_id       INTEGER NOT NULL,
		cost_code       TEXT NOT NULL DEFAULT '',
		description     TEXT NOT NULL,
		category        TEXT NOT NULL DEFAULT '',
		annual_amount   REAL NOT NULL DEFAULT 0,
		monthly_amount  REAL NOT NULL DEFAULT 0,
		notes           TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_budget_lines_budget ON budget_lines(budget_id);
	`
	_, err := s.db.Exec(schema)
	return err
}

// ── Customers ────────────────────────────────────────────────────

// InsertCustomer creates a new customer record.
func (s *SQLiteStore) InsertCustomer(c Customer) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO customers (name, contact_name, email, phone, address, city, state, zip, source, status, tags, notes, payment_method)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.ContactName, c.Email, c.Phone, c.Address, c.City, c.State, c.Zip, c.Source, c.Status, c.Tags, c.Notes, c.PaymentMethod,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetCustomer returns a single customer by ID.
func (s *SQLiteStore) GetCustomer(id int64) (*Customer, error) {
	row := s.db.QueryRow(`SELECT id, name, contact_name, email, phone, address, city, state, zip, source, status, tags, notes, payment_method, created_at, updated_at FROM customers WHERE id = ?`, id)
	var c Customer
	err := row.Scan(&c.ID, &c.Name, &c.ContactName, &c.Email, &c.Phone, &c.Address, &c.City, &c.State, &c.Zip, &c.Source, &c.Status, &c.Tags, &c.Notes, &c.PaymentMethod, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateCustomer updates all fields of a customer by ID.
func (s *SQLiteStore) UpdateCustomer(c Customer) error {
	_, err := s.db.Exec(`
		UPDATE customers SET name = ?, contact_name = ?, email = ?, phone = ?, address = ?, city = ?, state = ?, zip = ?, source = ?, status = ?, tags = ?, notes = ?, payment_method = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		c.Name, c.ContactName, c.Email, c.Phone, c.Address, c.City, c.State, c.Zip, c.Source, c.Status, c.Tags, c.Notes, c.PaymentMethod, c.ID,
	)
	return err
}

// DeleteCustomer removes a customer by ID.
func (s *SQLiteStore) DeleteCustomer(id int64) error {
	_, err := s.db.Exec(`DELETE FROM customers WHERE id = ?`, id)
	return err
}

// ListCustomers returns recent customers ordered by updated_at.
func (s *SQLiteStore) ListCustomers(limit int) ([]Customer, error) {
	rows, err := s.db.Query(`
		SELECT id, name, contact_name, email, phone, address, city, state, zip, source, status, tags, notes, payment_method, created_at, updated_at
		FROM customers ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCustomers(rows)
}

// SearchCustomers searches customers by name, contact_name, email, phone, tags, and notes.
func (s *SQLiteStore) SearchCustomers(query string, limit int) ([]Customer, error) {
	q := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT id, name, contact_name, email, phone, address, city, state, zip, source, status, tags, notes, payment_method, created_at, updated_at
		FROM customers WHERE name LIKE ? OR contact_name LIKE ? OR email LIKE ? OR phone LIKE ? OR tags LIKE ? OR notes LIKE ?
		ORDER BY updated_at DESC LIMIT ?`, q, q, q, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCustomers(rows)
}

func scanCustomers(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Customer, error) {
	var items []Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.Name, &c.ContactName, &c.Email, &c.Phone, &c.Address, &c.City, &c.State, &c.Zip, &c.Source, &c.Status, &c.Tags, &c.Notes, &c.PaymentMethod, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, nil
}

// ── Properties ───────────────────────────────────────────────────

// InsertProperty creates a new property record.
func (s *SQLiteStore) InsertProperty(p Property) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO properties (customer_id, address, city, state, zip, lot_size_sqft, lawn_sqft, bed_sqft, hardscape_sqft, tags, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.CustomerID, p.Address, p.City, p.State, p.Zip, p.LotSizeSqft, p.LawnSqft, p.BedSqft, p.HardscapeSqft, p.Tags, p.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetProperty returns a single property by ID.
func (s *SQLiteStore) GetProperty(id int64) (*Property, error) {
	row := s.db.QueryRow(`SELECT id, customer_id, address, city, state, zip, lot_size_sqft, lawn_sqft, bed_sqft, hardscape_sqft, tags, notes, created_at, updated_at FROM properties WHERE id = ?`, id)
	var p Property
	err := row.Scan(&p.ID, &p.CustomerID, &p.Address, &p.City, &p.State, &p.Zip, &p.LotSizeSqft, &p.LawnSqft, &p.BedSqft, &p.HardscapeSqft, &p.Tags, &p.Notes, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateProperty updates all fields of a property by ID.
func (s *SQLiteStore) UpdateProperty(p Property) error {
	_, err := s.db.Exec(`
		UPDATE properties SET customer_id = ?, address = ?, city = ?, state = ?, zip = ?, lot_size_sqft = ?, lawn_sqft = ?, bed_sqft = ?, hardscape_sqft = ?, tags = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		p.CustomerID, p.Address, p.City, p.State, p.Zip, p.LotSizeSqft, p.LawnSqft, p.BedSqft, p.HardscapeSqft, p.Tags, p.Notes, p.ID,
	)
	return err
}

// DeleteProperty removes a property by ID.
func (s *SQLiteStore) DeleteProperty(id int64) error {
	_, err := s.db.Exec(`DELETE FROM properties WHERE id = ?`, id)
	return err
}

// ListPropertiesByCustomer returns properties for a given customer.
func (s *SQLiteStore) ListPropertiesByCustomer(customerID int64, limit int) ([]Property, error) {
	rows, err := s.db.Query(`
		SELECT id, customer_id, address, city, state, zip, lot_size_sqft, lawn_sqft, bed_sqft, hardscape_sqft, tags, notes, created_at, updated_at
		FROM properties WHERE customer_id = ? ORDER BY updated_at DESC LIMIT ?`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProperties(rows)
}

func scanProperties(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Property, error) {
	var items []Property
	for rows.Next() {
		var p Property
		if err := rows.Scan(&p.ID, &p.CustomerID, &p.Address, &p.City, &p.State, &p.Zip, &p.LotSizeSqft, &p.LawnSqft, &p.BedSqft, &p.HardscapeSqft, &p.Tags, &p.Notes, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

// ── Crew Members ─────────────────────────────────────────────────

// InsertCrewMember creates a new crew member record.
func (s *SQLiteStore) InsertCrewMember(m CrewMember) (int64, error) {
	active := 0
	if m.Active {
		active = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO crew_members (name, role, phone, email, hourly_rate, skills, active, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Role, m.Phone, m.Email, m.HourlyRate, m.Skills, active, m.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetCrewMember returns a single crew member by ID.
func (s *SQLiteStore) GetCrewMember(id int64) (*CrewMember, error) {
	row := s.db.QueryRow(`SELECT id, name, role, phone, email, hourly_rate, skills, active, notes, created_at FROM crew_members WHERE id = ?`, id)
	var m CrewMember
	var active int
	err := row.Scan(&m.ID, &m.Name, &m.Role, &m.Phone, &m.Email, &m.HourlyRate, &m.Skills, &active, &m.Notes, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	m.Active = active == 1
	return &m, nil
}

// UpdateCrewMember updates all fields of a crew member by ID.
func (s *SQLiteStore) UpdateCrewMember(m CrewMember) error {
	active := 0
	if m.Active {
		active = 1
	}
	_, err := s.db.Exec(`
		UPDATE crew_members SET name = ?, role = ?, phone = ?, email = ?, hourly_rate = ?, skills = ?, active = ?, notes = ?
		WHERE id = ?`,
		m.Name, m.Role, m.Phone, m.Email, m.HourlyRate, m.Skills, active, m.Notes, m.ID,
	)
	return err
}

// ListCrewMembers returns crew members filtered by role and active status.
func (s *SQLiteStore) ListCrewMembers(role string, activeOnly bool, limit int) ([]CrewMember, error) {
	query := `SELECT id, name, role, phone, email, hourly_rate, skills, active, notes, created_at FROM crew_members WHERE 1=1`
	args := []any{}
	if role != "" {
		query += ` AND role = ?`
		args = append(args, role)
	}
	if activeOnly {
		query += ` AND active = 1`
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCrewMembers(rows)
}

func scanCrewMembers(rows interface{ Scan(dest ...any) error; Next() bool }) ([]CrewMember, error) {
	var items []CrewMember
	for rows.Next() {
		var m CrewMember
		var active int
		if err := rows.Scan(&m.ID, &m.Name, &m.Role, &m.Phone, &m.Email, &m.HourlyRate, &m.Skills, &active, &m.Notes, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Active = active == 1
		items = append(items, m)
	}
	return items, nil
}

// ── Crews ────────────────────────────────────────────────────────

// InsertCrew creates a new crew record.
func (s *SQLiteStore) InsertCrew(c Crew) (int64, error) {
	active := 0
	if c.Active {
		active = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO crews (name, foreman_id, member_ids, truck, specialties, active)
		VALUES (?, ?, ?, ?, ?, ?)`,
		c.Name, c.ForemanID, c.MemberIDs, c.Truck, c.Specialties, active,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetCrew returns a single crew by ID.
func (s *SQLiteStore) GetCrew(id int64) (*Crew, error) {
	row := s.db.QueryRow(`SELECT id, name, foreman_id, member_ids, truck, specialties, active FROM crews WHERE id = ?`, id)
	var c Crew
	var active int
	err := row.Scan(&c.ID, &c.Name, &c.ForemanID, &c.MemberIDs, &c.Truck, &c.Specialties, &active)
	if err != nil {
		return nil, err
	}
	c.Active = active == 1
	return &c, nil
}

// UpdateCrew updates all fields of a crew by ID.
func (s *SQLiteStore) UpdateCrew(c Crew) error {
	active := 0
	if c.Active {
		active = 1
	}
	_, err := s.db.Exec(`
		UPDATE crews SET name = ?, foreman_id = ?, member_ids = ?, truck = ?, specialties = ?, active = ?
		WHERE id = ?`,
		c.Name, c.ForemanID, c.MemberIDs, c.Truck, c.Specialties, active, c.ID,
	)
	return err
}

// ListCrews returns crews optionally filtered by active status.
func (s *SQLiteStore) ListCrews(activeOnly bool, limit int) ([]Crew, error) {
	query := `SELECT id, name, foreman_id, member_ids, truck, specialties, active FROM crews`
	args := []any{}
	if activeOnly {
		query += ` WHERE active = 1`
	}
	query += ` ORDER BY name ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCrews(rows)
}

func scanCrews(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Crew, error) {
	var items []Crew
	for rows.Next() {
		var c Crew
		var active int
		if err := rows.Scan(&c.ID, &c.Name, &c.ForemanID, &c.MemberIDs, &c.Truck, &c.Specialties, &active); err != nil {
			return nil, err
		}
		c.Active = active == 1
		items = append(items, c)
	}
	return items, nil
}

// ── Items (Catalog) ──────────────────────────────────────────────

// InsertItem creates a new catalog item.
func (s *SQLiteStore) InsertItem(i Item) (int64, error) {
	taxable := 0
	if i.Taxable {
		taxable = 1
	}
	active := 0
	if i.Active {
		active = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO items (name, category, unit, cost, price, supplier, sku, taxable, active, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.Name, i.Category, i.Unit, i.Cost, i.Price, i.Supplier, i.SKU, taxable, active, i.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetItem returns a single catalog item by ID.
func (s *SQLiteStore) GetItem(id int64) (*Item, error) {
	row := s.db.QueryRow(`SELECT id, name, category, unit, cost, price, supplier, sku, taxable, active, notes, created_at, updated_at FROM items WHERE id = ?`, id)
	var i Item
	var taxable, active int
	err := row.Scan(&i.ID, &i.Name, &i.Category, &i.Unit, &i.Cost, &i.Price, &i.Supplier, &i.SKU, &taxable, &active, &i.Notes, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return nil, err
	}
	i.Taxable = taxable == 1
	i.Active = active == 1
	return &i, nil
}

// UpdateItem updates all fields of a catalog item by ID.
func (s *SQLiteStore) UpdateItem(i Item) error {
	taxable := 0
	if i.Taxable {
		taxable = 1
	}
	active := 0
	if i.Active {
		active = 1
	}
	_, err := s.db.Exec(`
		UPDATE items SET name = ?, category = ?, unit = ?, cost = ?, price = ?, supplier = ?, sku = ?, taxable = ?, active = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		i.Name, i.Category, i.Unit, i.Cost, i.Price, i.Supplier, i.SKU, taxable, active, i.Notes, i.ID,
	)
	return err
}

// ListItems returns catalog items filtered by category and active status.
func (s *SQLiteStore) ListItems(category string, activeOnly bool, limit int) ([]Item, error) {
	query := `SELECT id, name, category, unit, cost, price, supplier, sku, taxable, active, notes, created_at, updated_at FROM items WHERE 1=1`
	args := []any{}
	if category != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	if activeOnly {
		query += ` AND active = 1`
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

// SearchItems searches catalog items by name, category, supplier, sku, and notes.
func (s *SQLiteStore) SearchItems(query string, limit int) ([]Item, error) {
	q := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT id, name, category, unit, cost, price, supplier, sku, taxable, active, notes, created_at, updated_at
		FROM items WHERE name LIKE ? OR category LIKE ? OR supplier LIKE ? OR sku LIKE ? OR notes LIKE ?
		ORDER BY updated_at DESC LIMIT ?`, q, q, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func scanItems(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Item, error) {
	var items []Item
	for rows.Next() {
		var i Item
		var taxable, active int
		if err := rows.Scan(&i.ID, &i.Name, &i.Category, &i.Unit, &i.Cost, &i.Price, &i.Supplier, &i.SKU, &taxable, &active, &i.Notes, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		i.Taxable = taxable == 1
		i.Active = active == 1
		items = append(items, i)
	}
	return items, nil
}

// ── Estimates ────────────────────────────────────────────────────

// InsertEstimate creates a new estimate record.
func (s *SQLiteStore) InsertEstimate(e Estimate) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO estimates (customer_id, property_id, job_id, title, status, line_items, subtotal, tax, total, margin_pct, deposit_pct, valid_until, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.CustomerID, e.PropertyID, e.JobID, e.Title, e.Status, e.LineItems, e.Subtotal, e.Tax, e.Total, e.MarginPct, e.DepositPct, e.ValidUntil, e.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetEstimate returns a single estimate by ID.
func (s *SQLiteStore) GetEstimate(id int64) (*Estimate, error) {
	row := s.db.QueryRow(`SELECT id, customer_id, property_id, job_id, title, status, line_items, subtotal, tax, total, margin_pct, deposit_pct, valid_until, notes, created_at, updated_at FROM estimates WHERE id = ?`, id)
	var e Estimate
	err := row.Scan(&e.ID, &e.CustomerID, &e.PropertyID, &e.JobID, &e.Title, &e.Status, &e.LineItems, &e.Subtotal, &e.Tax, &e.Total, &e.MarginPct, &e.DepositPct, &e.ValidUntil, &e.Notes, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// UpdateEstimate updates all fields of an estimate by ID.
func (s *SQLiteStore) UpdateEstimate(e Estimate) error {
	_, err := s.db.Exec(`
		UPDATE estimates SET customer_id = ?, property_id = ?, job_id = ?, title = ?, status = ?, line_items = ?, subtotal = ?, tax = ?, total = ?, margin_pct = ?, deposit_pct = ?, valid_until = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		e.CustomerID, e.PropertyID, e.JobID, e.Title, e.Status, e.LineItems, e.Subtotal, e.Tax, e.Total, e.MarginPct, e.DepositPct, e.ValidUntil, e.Notes, e.ID,
	)
	return err
}

// UpdateEstimateStatus changes just the status of an estimate.
func (s *SQLiteStore) UpdateEstimateStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE estimates SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

// ListEstimates returns estimates optionally filtered by status.
func (s *SQLiteStore) ListEstimates(status string, limit int) ([]Estimate, error) {
	query := `SELECT id, customer_id, property_id, job_id, title, status, line_items, subtotal, tax, total, margin_pct, deposit_pct, valid_until, notes, created_at, updated_at FROM estimates`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEstimates(rows)
}

// ListEstimatesByCustomer returns estimates for a given customer.
func (s *SQLiteStore) ListEstimatesByCustomer(customerID int64, limit int) ([]Estimate, error) {
	rows, err := s.db.Query(`
		SELECT id, customer_id, property_id, job_id, title, status, line_items, subtotal, tax, total, margin_pct, deposit_pct, valid_until, notes, created_at, updated_at
		FROM estimates WHERE customer_id = ? ORDER BY updated_at DESC LIMIT ?`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEstimates(rows)
}

func scanEstimates(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Estimate, error) {
	var items []Estimate
	for rows.Next() {
		var e Estimate
		if err := rows.Scan(&e.ID, &e.CustomerID, &e.PropertyID, &e.JobID, &e.Title, &e.Status, &e.LineItems, &e.Subtotal, &e.Tax, &e.Total, &e.MarginPct, &e.DepositPct, &e.ValidUntil, &e.Notes, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, nil
}

// ── Calendar Events ──────────────────────────────────────────────

// InsertCalendarEvent creates a new calendar event.
func (s *SQLiteStore) InsertCalendarEvent(e CalendarEvent) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO calendar_events (title, event_type, date, start_time, end_time, crew_id, job_id, customer_id, property_id, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Title, e.EventType, e.Date, e.StartTime, e.EndTime, e.CrewID, e.JobID, e.CustomerID, e.PropertyID, e.Status, e.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetCalendarEvent returns a single calendar event by ID.
func (s *SQLiteStore) GetCalendarEvent(id int64) (*CalendarEvent, error) {
	row := s.db.QueryRow(`SELECT id, title, event_type, date, start_time, end_time, crew_id, job_id, customer_id, property_id, status, notes, created_at, updated_at FROM calendar_events WHERE id = ?`, id)
	var e CalendarEvent
	err := row.Scan(&e.ID, &e.Title, &e.EventType, &e.Date, &e.StartTime, &e.EndTime, &e.CrewID, &e.JobID, &e.CustomerID, &e.PropertyID, &e.Status, &e.Notes, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// UpdateCalendarEvent updates all fields of a calendar event by ID.
func (s *SQLiteStore) UpdateCalendarEvent(e CalendarEvent) error {
	_, err := s.db.Exec(`
		UPDATE calendar_events SET title = ?, event_type = ?, date = ?, start_time = ?, end_time = ?, crew_id = ?, job_id = ?, customer_id = ?, property_id = ?, status = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		e.Title, e.EventType, e.Date, e.StartTime, e.EndTime, e.CrewID, e.JobID, e.CustomerID, e.PropertyID, e.Status, e.Notes, e.ID,
	)
	return err
}

// DeleteCalendarEvent removes a calendar event by ID.
func (s *SQLiteStore) DeleteCalendarEvent(id int64) error {
	_, err := s.db.Exec(`DELETE FROM calendar_events WHERE id = ?`, id)
	return err
}

// ListCalendarEvents returns events for a specific date.
func (s *SQLiteStore) ListCalendarEvents(date string, limit int) ([]CalendarEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, title, event_type, date, start_time, end_time, crew_id, job_id, customer_id, property_id, status, notes, created_at, updated_at
		FROM calendar_events WHERE date = ? ORDER BY start_time ASC LIMIT ?`, date, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCalendarEvents(rows)
}

// ListCalendarEventsByRange returns events within a date range (inclusive).
func (s *SQLiteStore) ListCalendarEventsByRange(startDate, endDate string, limit int) ([]CalendarEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, title, event_type, date, start_time, end_time, crew_id, job_id, customer_id, property_id, status, notes, created_at, updated_at
		FROM calendar_events WHERE date >= ? AND date <= ? ORDER BY date ASC, start_time ASC LIMIT ?`, startDate, endDate, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCalendarEvents(rows)
}

// ListCalendarEventsByCrew returns events for a specific crew on a given date.
func (s *SQLiteStore) ListCalendarEventsByCrew(crewID int64, date string, limit int) ([]CalendarEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, title, event_type, date, start_time, end_time, crew_id, job_id, customer_id, property_id, status, notes, created_at, updated_at
		FROM calendar_events WHERE crew_id = ? AND date = ? ORDER BY start_time ASC LIMIT ?`, crewID, date, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCalendarEvents(rows)
}

func scanCalendarEvents(rows interface{ Scan(dest ...any) error; Next() bool }) ([]CalendarEvent, error) {
	var items []CalendarEvent
	for rows.Next() {
		var e CalendarEvent
		if err := rows.Scan(&e.ID, &e.Title, &e.EventType, &e.Date, &e.StartTime, &e.EndTime, &e.CrewID, &e.JobID, &e.CustomerID, &e.PropertyID, &e.Status, &e.Notes, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, nil
}

// ── Invoices ─────────────────────────────────────────────────────

// InsertInvoice creates a new invoice record.
func (s *SQLiteStore) InsertInvoice(i Invoice) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO invoices (invoice_number, customer_id, job_id, estimate_id, status, line_items, subtotal, tax, total, deposit_applied, amount_due, issued_date, due_date, paid_date, payment_method, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.InvoiceNumber, i.CustomerID, i.JobID, i.EstimateID, i.Status, i.LineItems, i.Subtotal, i.Tax, i.Total, i.DepositApplied, i.AmountDue, i.IssuedDate, i.DueDate, i.PaidDate, i.PaymentMethod, i.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetInvoice returns a single invoice by ID.
func (s *SQLiteStore) GetInvoice(id int64) (*Invoice, error) {
	row := s.db.QueryRow(`SELECT id, invoice_number, customer_id, job_id, estimate_id, status, line_items, subtotal, tax, total, deposit_applied, amount_due, issued_date, due_date, paid_date, payment_method, notes, created_at, updated_at FROM invoices WHERE id = ?`, id)
	var i Invoice
	err := row.Scan(&i.ID, &i.InvoiceNumber, &i.CustomerID, &i.JobID, &i.EstimateID, &i.Status, &i.LineItems, &i.Subtotal, &i.Tax, &i.Total, &i.DepositApplied, &i.AmountDue, &i.IssuedDate, &i.DueDate, &i.PaidDate, &i.PaymentMethod, &i.Notes, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// UpdateInvoice updates all fields of an invoice by ID.
func (s *SQLiteStore) UpdateInvoice(i Invoice) error {
	_, err := s.db.Exec(`
		UPDATE invoices SET invoice_number = ?, customer_id = ?, job_id = ?, estimate_id = ?, status = ?, line_items = ?, subtotal = ?, tax = ?, total = ?, deposit_applied = ?, amount_due = ?, issued_date = ?, due_date = ?, paid_date = ?, payment_method = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		i.InvoiceNumber, i.CustomerID, i.JobID, i.EstimateID, i.Status, i.LineItems, i.Subtotal, i.Tax, i.Total, i.DepositApplied, i.AmountDue, i.IssuedDate, i.DueDate, i.PaidDate, i.PaymentMethod, i.Notes, i.ID,
	)
	return err
}

// UpdateInvoiceStatus updates the status and payment details of an invoice.
func (s *SQLiteStore) UpdateInvoiceStatus(id int64, status, paidDate, paymentMethod string) error {
	_, err := s.db.Exec(`
		UPDATE invoices SET status = ?, paid_date = ?, payment_method = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, status, paidDate, paymentMethod, id)
	return err
}

// ListInvoices returns invoices optionally filtered by status.
func (s *SQLiteStore) ListInvoices(status string, limit int) ([]Invoice, error) {
	query := `SELECT id, invoice_number, customer_id, job_id, estimate_id, status, line_items, subtotal, tax, total, deposit_applied, amount_due, issued_date, due_date, paid_date, payment_method, notes, created_at, updated_at FROM invoices`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInvoices(rows)
}

// ListInvoicesByCustomer returns invoices for a given customer.
func (s *SQLiteStore) ListInvoicesByCustomer(customerID int64, limit int) ([]Invoice, error) {
	rows, err := s.db.Query(`
		SELECT id, invoice_number, customer_id, job_id, estimate_id, status, line_items, subtotal, tax, total, deposit_applied, amount_due, issued_date, due_date, paid_date, payment_method, notes, created_at, updated_at
		FROM invoices WHERE customer_id = ? ORDER BY updated_at DESC LIMIT ?`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInvoices(rows)
}

func scanInvoices(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Invoice, error) {
	var items []Invoice
	for rows.Next() {
		var i Invoice
		if err := rows.Scan(&i.ID, &i.InvoiceNumber, &i.CustomerID, &i.JobID, &i.EstimateID, &i.Status, &i.LineItems, &i.Subtotal, &i.Tax, &i.Total, &i.DepositApplied, &i.AmountDue, &i.IssuedDate, &i.DueDate, &i.PaidDate, &i.PaymentMethod, &i.Notes, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}

// ── Vendors ──────────────────────────────────────────────────────

// InsertVendor creates a new vendor record.
func (s *SQLiteStore) InsertVendor(v Vendor) (int64, error) {
	active := 0
	if v.Active {
		active = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO vendors (name, contact_name, phone, email, address, specialty, payment_terms, account_number, active, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.Name, v.ContactName, v.Phone, v.Email, v.Address, v.Specialty, v.PaymentTerms, v.AccountNumber, active, v.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetVendor returns a single vendor by ID.
func (s *SQLiteStore) GetVendor(id int64) (*Vendor, error) {
	row := s.db.QueryRow(`SELECT id, name, contact_name, phone, email, address, specialty, payment_terms, account_number, active, notes, created_at, updated_at FROM vendors WHERE id = ?`, id)
	var v Vendor
	var active int
	err := row.Scan(&v.ID, &v.Name, &v.ContactName, &v.Phone, &v.Email, &v.Address, &v.Specialty, &v.PaymentTerms, &v.AccountNumber, &active, &v.Notes, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	v.Active = active == 1
	return &v, nil
}

// UpdateVendor updates all fields of a vendor by ID.
func (s *SQLiteStore) UpdateVendor(v Vendor) error {
	active := 0
	if v.Active {
		active = 1
	}
	_, err := s.db.Exec(`
		UPDATE vendors SET name = ?, contact_name = ?, phone = ?, email = ?, address = ?, specialty = ?, payment_terms = ?, account_number = ?, active = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		v.Name, v.ContactName, v.Phone, v.Email, v.Address, v.Specialty, v.PaymentTerms, v.AccountNumber, active, v.Notes, v.ID,
	)
	return err
}

// ListVendors returns vendors optionally filtered by active status.
func (s *SQLiteStore) ListVendors(activeOnly bool, limit int) ([]Vendor, error) {
	query := `SELECT id, name, contact_name, phone, email, address, specialty, payment_terms, account_number, active, notes, created_at, updated_at FROM vendors`
	args := []any{}
	if activeOnly {
		query += ` WHERE active = 1`
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVendors(rows)
}

// SearchVendors searches vendors by name, contact_name, specialty, and notes.
func (s *SQLiteStore) SearchVendors(query string, limit int) ([]Vendor, error) {
	q := "%" + query + "%"
	rows, err := s.db.Query(`
		SELECT id, name, contact_name, phone, email, address, specialty, payment_terms, account_number, active, notes, created_at, updated_at
		FROM vendors WHERE name LIKE ? OR contact_name LIKE ? OR specialty LIKE ? OR notes LIKE ?
		ORDER BY updated_at DESC LIMIT ?`, q, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVendors(rows)
}

func scanVendors(rows interface{ Scan(dest ...any) error; Next() bool }) ([]Vendor, error) {
	var items []Vendor
	for rows.Next() {
		var v Vendor
		var active int
		if err := rows.Scan(&v.ID, &v.Name, &v.ContactName, &v.Phone, &v.Email, &v.Address, &v.Specialty, &v.PaymentTerms, &v.AccountNumber, &active, &v.Notes, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.Active = active == 1
		items = append(items, v)
	}
	return items, nil
}

// ── Sales Leads ──────────────────────────────────────────────────

// InsertSalesLead creates a new sales lead record.
func (s *SQLiteStore) InsertSalesLead(l SalesLead) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO sales_leads (customer_id, name, phone, email, source, status, estimated_value, job_type, property_address, assigned_to, lost_reason, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.CustomerID, l.Name, l.Phone, l.Email, l.Source, l.Status, l.EstimatedValue, l.JobType, l.PropertyAddress, l.AssignedTo, l.LostReason, l.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetSalesLead returns a single sales lead by ID.
func (s *SQLiteStore) GetSalesLead(id int64) (*SalesLead, error) {
	row := s.db.QueryRow(`SELECT id, customer_id, name, phone, email, source, status, estimated_value, job_type, property_address, assigned_to, lost_reason, notes, created_at, updated_at FROM sales_leads WHERE id = ?`, id)
	var l SalesLead
	err := row.Scan(&l.ID, &l.CustomerID, &l.Name, &l.Phone, &l.Email, &l.Source, &l.Status, &l.EstimatedValue, &l.JobType, &l.PropertyAddress, &l.AssignedTo, &l.LostReason, &l.Notes, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// UpdateSalesLead updates all fields of a sales lead by ID.
func (s *SQLiteStore) UpdateSalesLead(l SalesLead) error {
	_, err := s.db.Exec(`
		UPDATE sales_leads SET customer_id = ?, name = ?, phone = ?, email = ?, source = ?, status = ?, estimated_value = ?, job_type = ?, property_address = ?, assigned_to = ?, lost_reason = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		l.CustomerID, l.Name, l.Phone, l.Email, l.Source, l.Status, l.EstimatedValue, l.JobType, l.PropertyAddress, l.AssignedTo, l.LostReason, l.Notes, l.ID,
	)
	return err
}

// ListSalesLeads returns sales leads optionally filtered by status.
func (s *SQLiteStore) ListSalesLeads(status string, limit int) ([]SalesLead, error) {
	query := `SELECT id, customer_id, name, phone, email, source, status, estimated_value, job_type, property_address, assigned_to, lost_reason, notes, created_at, updated_at FROM sales_leads`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSalesLeads(rows)
}

// ListSalesLeadsByAssignee returns sales leads assigned to a specific person.
func (s *SQLiteStore) ListSalesLeadsByAssignee(assignee string, limit int) ([]SalesLead, error) {
	rows, err := s.db.Query(`
		SELECT id, customer_id, name, phone, email, source, status, estimated_value, job_type, property_address, assigned_to, lost_reason, notes, created_at, updated_at
		FROM sales_leads WHERE assigned_to = ? ORDER BY updated_at DESC LIMIT ?`, assignee, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSalesLeads(rows)
}

func scanSalesLeads(rows interface{ Scan(dest ...any) error; Next() bool }) ([]SalesLead, error) {
	var items []SalesLead
	for rows.Next() {
		var l SalesLead
		if err := rows.Scan(&l.ID, &l.CustomerID, &l.Name, &l.Phone, &l.Email, &l.Source, &l.Status, &l.EstimatedValue, &l.JobType, &l.PropertyAddress, &l.AssignedTo, &l.LostReason, &l.Notes, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, nil
}

// ── Cost Codes ───────────────────────────────────────────────────

// InsertCostCode creates a new cost code record.
func (s *SQLiteStore) InsertCostCode(c CostCode) (int64, error) {
	active := 0
	if c.Active {
		active = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO cost_codes (code, name, division, division_group, active)
		VALUES (?, ?, ?, ?, ?)`,
		c.Code, c.Name, c.Division, c.DivisionGroup, active,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListCostCodes returns cost codes optionally filtered by division.
func (s *SQLiteStore) ListCostCodes(division string, limit int) ([]CostCode, error) {
	query := `SELECT id, code, name, division, division_group, active FROM cost_codes`
	args := []any{}
	if division != "" {
		query += ` WHERE division = ?`
		args = append(args, division)
	}
	query += ` ORDER BY code ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CostCode
	for rows.Next() {
		var c CostCode
		var active int
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.Division, &c.DivisionGroup, &active); err != nil {
			return nil, err
		}
		c.Active = active == 1
		items = append(items, c)
	}
	return items, nil
}

// ListDivisions returns distinct division names from cost codes.
func (s *SQLiteStore) ListDivisions() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT division FROM cost_codes WHERE division != '' ORDER BY division ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var divisions []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		divisions = append(divisions, d)
	}
	return divisions, nil
}

// ── Budgets ──────────────────────────────────────────────────────

// InsertBudget creates a new budget record.
func (s *SQLiteStore) InsertBudget(b Budget) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO budgets (year, name, revenue_target, total_overhead, billable_hours, hourly_rate, owner_salary, target_margin_pct, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.Year, b.Name, b.RevenueTarget, b.TotalOverhead, b.BillableHours, b.HourlyRate, b.OwnerSalary, b.TargetMarginPct, b.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetBudget returns a single budget by ID.
func (s *SQLiteStore) GetBudget(id int64) (*Budget, error) {
	row := s.db.QueryRow(`SELECT id, year, name, revenue_target, total_overhead, billable_hours, hourly_rate, owner_salary, target_margin_pct, notes, created_at, updated_at FROM budgets WHERE id = ?`, id)
	var b Budget
	err := row.Scan(&b.ID, &b.Year, &b.Name, &b.RevenueTarget, &b.TotalOverhead, &b.BillableHours, &b.HourlyRate, &b.OwnerSalary, &b.TargetMarginPct, &b.Notes, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// UpdateBudget updates all fields of a budget by ID.
func (s *SQLiteStore) UpdateBudget(b Budget) error {
	_, err := s.db.Exec(`
		UPDATE budgets SET year = ?, name = ?, revenue_target = ?, total_overhead = ?, billable_hours = ?, hourly_rate = ?, owner_salary = ?, target_margin_pct = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		b.Year, b.Name, b.RevenueTarget, b.TotalOverhead, b.BillableHours, b.HourlyRate, b.OwnerSalary, b.TargetMarginPct, b.Notes, b.ID,
	)
	return err
}

// GetBudgetByYear returns the budget for a given year.
func (s *SQLiteStore) GetBudgetByYear(year int) (*Budget, error) {
	row := s.db.QueryRow(`SELECT id, year, name, revenue_target, total_overhead, billable_hours, hourly_rate, owner_salary, target_margin_pct, notes, created_at, updated_at FROM budgets WHERE year = ?`, year)
	var b Budget
	err := row.Scan(&b.ID, &b.Year, &b.Name, &b.RevenueTarget, &b.TotalOverhead, &b.BillableHours, &b.HourlyRate, &b.OwnerSalary, &b.TargetMarginPct, &b.Notes, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBudgetYears returns distinct budget years in descending order.
func (s *SQLiteStore) ListBudgetYears() ([]int, error) {
	rows, err := s.db.Query(`SELECT DISTINCT year FROM budgets ORDER BY year DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var years []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		years = append(years, y)
	}
	return years, nil
}

// ── Budget Lines ─────────────────────────────────────────────────

// InsertBudgetLine creates a new budget line item.
func (s *SQLiteStore) InsertBudgetLine(l BudgetLine) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO budget_lines (budget_id, cost_code, description, category, annual_amount, monthly_amount, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.BudgetID, l.CostCode, l.Description, l.Category, l.AnnualAmount, l.MonthlyAmount, l.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListBudgetLines returns all line items for a given budget.
func (s *SQLiteStore) ListBudgetLines(budgetID int64) ([]BudgetLine, error) {
	rows, err := s.db.Query(`
		SELECT id, budget_id, cost_code, description, category, annual_amount, monthly_amount, notes
		FROM budget_lines WHERE budget_id = ? ORDER BY id ASC`, budgetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []BudgetLine
	for rows.Next() {
		var l BudgetLine
		if err := rows.Scan(&l.ID, &l.BudgetID, &l.CostCode, &l.Description, &l.Category, &l.AnnualAmount, &l.MonthlyAmount, &l.Notes); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, nil
}

// DeleteBudgetLine removes a budget line item by ID.
func (s *SQLiteStore) DeleteBudgetLine(id int64) error {
	_, err := s.db.Exec(`DELETE FROM budget_lines WHERE id = ?`, id)
	return err
}
