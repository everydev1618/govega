package peering

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SQLiteSchema is the SQLite DDL for the three peering tables. Exported so
// serve/store_sqlite.go can apply it inside the existing monolithic Init().
const SQLiteSchema = `
CREATE TABLE IF NOT EXISTS peer_orchestrators (
    node_id        TEXT PRIMARY KEY,
    handle         TEXT NOT NULL DEFAULT '',
    endpoint       TEXT NOT NULL,
    shared_secret  TEXT NOT NULL,
    trust_level    TEXT NOT NULL DEFAULT 'scoped',
    added_by       TEXT NOT NULL DEFAULT '',
    added_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at   DATETIME,
    notes          TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_peer_orchestrators_handle
    ON peer_orchestrators(handle) WHERE handle != '';

CREATE TABLE IF NOT EXISTS peer_agent_grants (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    peer_node_id      TEXT NOT NULL,
    local_agent       TEXT NOT NULL,
    max_tokens_per_op INTEGER NOT NULL DEFAULT 8000,
    max_ops_per_hour  INTEGER NOT NULL DEFAULT 30,
    active            INTEGER NOT NULL DEFAULT 1,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(peer_node_id, local_agent)
);
CREATE INDEX IF NOT EXISTS idx_peer_agent_grants_peer
    ON peer_agent_grants(peer_node_id);

CREATE TABLE IF NOT EXISTS peer_audit_log (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ts             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    direction      TEXT NOT NULL,
    peer_node_id   TEXT NOT NULL,
    peer_handle    TEXT NOT NULL DEFAULT '',
    agent          TEXT NOT NULL,
    op_id          INTEGER NOT NULL,
    tokens_in      INTEGER NOT NULL DEFAULT 0,
    tokens_out     INTEGER NOT NULL DEFAULT 0,
    cost_usd       REAL NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'started',
    denial_reason  TEXT NOT NULL DEFAULT '',
    duration_ms    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_peer_audit_peer_agent_ts
    ON peer_audit_log(peer_node_id, agent, ts DESC);
CREATE INDEX IF NOT EXISTS idx_peer_audit_ts
    ON peer_audit_log(ts DESC);

-- Peering-local key/value settings. Used today for the locally-generated
-- NodeID; new keys added as the federation feature grows.
CREATE TABLE IF NOT EXISTS peering_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
`

// ApplySQLiteSchema executes the peering DDL against db. Idempotent —
// every statement uses IF NOT EXISTS.
func ApplySQLiteSchema(db *sql.DB) error {
	if db == nil {
		return ErrNilDB
	}
	_, err := db.Exec(SQLiteSchema)
	return err
}

// SQLiteStorage implements Store on top of a *sql.DB opened against a
// modernc.org/sqlite database. The same struct can also drive Postgres
// after a placeholder rewrite — see NewPostgresStorage for that path.
type SQLiteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage returns a Store backed by db. The caller is responsible
// for applying SQLiteSchema first (or for using serve/store_sqlite.go's Init,
// which does so as part of its monolithic schema setup).
func NewSQLiteStorage(db *sql.DB) *SQLiteStorage {
	return &SQLiteStorage{db: db}
}

// --- Peers ---

func (s *SQLiteStorage) UpsertPeer(p Peer) error {
	if p.TrustLevel == "" {
		p.TrustLevel = TrustScoped
	}
	_, err := s.db.Exec(
		`INSERT INTO peer_orchestrators
			(node_id, handle, endpoint, shared_secret, trust_level, added_by, notes)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
			handle = excluded.handle,
			endpoint = excluded.endpoint,
			shared_secret = excluded.shared_secret,
			trust_level = excluded.trust_level,
			added_by = excluded.added_by,
			notes = excluded.notes`,
		p.NodeID, p.Handle, p.Endpoint, p.SharedSecret, string(p.TrustLevel), p.AddedBy, p.Notes,
	)
	return err
}

func (s *SQLiteStorage) GetPeer(nodeID string) (*Peer, error) {
	row := s.db.QueryRow(
		`SELECT node_id, handle, endpoint, shared_secret, trust_level,
			added_by, added_at, last_seen_at, notes
		 FROM peer_orchestrators WHERE node_id = ?`, nodeID,
	)
	return scanPeer(row)
}

func (s *SQLiteStorage) ListPeers() ([]Peer, error) {
	rows, err := s.db.Query(
		`SELECT node_id, handle, endpoint, shared_secret, trust_level,
			added_by, added_at, last_seen_at, notes
		 FROM peer_orchestrators ORDER BY added_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Peer
	for rows.Next() {
		p, err := scanPeerRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *SQLiteStorage) DeletePeer(nodeID string) error {
	_, err := s.db.Exec(`DELETE FROM peer_orchestrators WHERE node_id = ?`, nodeID)
	return err
}

func (s *SQLiteStorage) TouchPeerLastSeen(nodeID string, t time.Time) error {
	_, err := s.db.Exec(`UPDATE peer_orchestrators SET last_seen_at = ? WHERE node_id = ?`, t.UTC(), nodeID)
	return err
}

// --- Grants ---

func (s *SQLiteStorage) UpsertGrant(g Grant) error {
	if g.MaxTokensPerOp == 0 {
		g.MaxTokensPerOp = 8000
	}
	if g.MaxOpsPerHour == 0 {
		g.MaxOpsPerHour = 30
	}
	_, err := s.db.Exec(
		`INSERT INTO peer_agent_grants
			(peer_node_id, local_agent, max_tokens_per_op, max_ops_per_hour, active)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(peer_node_id, local_agent) DO UPDATE SET
			max_tokens_per_op = excluded.max_tokens_per_op,
			max_ops_per_hour = excluded.max_ops_per_hour,
			active = excluded.active`,
		g.PeerNodeID, g.LocalAgent, g.MaxTokensPerOp, g.MaxOpsPerHour, boolToInt(g.Active),
	)
	return err
}

func (s *SQLiteStorage) GetGrant(peerNodeID, localAgent string) (*Grant, error) {
	row := s.db.QueryRow(
		`SELECT id, peer_node_id, local_agent, max_tokens_per_op, max_ops_per_hour, active, created_at
		 FROM peer_agent_grants WHERE peer_node_id = ? AND local_agent = ?`,
		peerNodeID, localAgent,
	)
	return scanGrant(row)
}

func (s *SQLiteStorage) ListGrants(peerNodeID string) ([]Grant, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if peerNodeID == "" {
		rows, err = s.db.Query(
			`SELECT id, peer_node_id, local_agent, max_tokens_per_op, max_ops_per_hour, active, created_at
			 FROM peer_agent_grants ORDER BY peer_node_id, local_agent`)
	} else {
		rows, err = s.db.Query(
			`SELECT id, peer_node_id, local_agent, max_tokens_per_op, max_ops_per_hour, active, created_at
			 FROM peer_agent_grants WHERE peer_node_id = ? ORDER BY local_agent`, peerNodeID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		g, err := scanGrantRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (s *SQLiteStorage) DeleteGrant(peerNodeID, localAgent string) error {
	_, err := s.db.Exec(
		`DELETE FROM peer_agent_grants WHERE peer_node_id = ? AND local_agent = ?`,
		peerNodeID, localAgent,
	)
	return err
}

// --- Audit ---

func (s *SQLiteStorage) InsertAudit(e AuditEntry) (int64, error) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.Status == "" {
		e.Status = AuditStatusStarted
	}
	res, err := s.db.Exec(
		`INSERT INTO peer_audit_log
			(ts, direction, peer_node_id, peer_handle, agent, op_id,
			 tokens_in, tokens_out, cost_usd, status, denial_reason, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp.UTC(), e.Direction, e.PeerNodeID, e.PeerHandle, e.Agent, e.OpID,
		e.TokensIn, e.TokensOut, e.CostUSD, e.Status, e.DenialReason, e.DurationMS,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStorage) FinalizeAudit(id int64, f AuditFinalize) error {
	_, err := s.db.Exec(
		`UPDATE peer_audit_log SET
			status = ?, tokens_in = ?, tokens_out = ?, cost_usd = ?,
			duration_ms = ?, denial_reason = ?
		 WHERE id = ?`,
		f.Status, f.TokensIn, f.TokensOut, f.CostUSD, f.DurationMS, f.DenialReason, id,
	)
	return err
}

func (s *SQLiteStorage) ListAudit(f AuditFilter) ([]AuditEntry, error) {
	var (
		clauses []string
		args    []any
	)
	if f.PeerNodeID != "" {
		clauses = append(clauses, "peer_node_id = ?")
		args = append(args, f.PeerNodeID)
	}
	if f.Agent != "" {
		clauses = append(clauses, "agent = ?")
		args = append(args, f.Agent)
	}
	if f.Direction != "" {
		clauses = append(clauses, "direction = ?")
		args = append(args, f.Direction)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q := fmt.Sprintf(
		`SELECT id, ts, direction, peer_node_id, peer_handle, agent, op_id,
			tokens_in, tokens_out, cost_usd, status, denial_reason, duration_ms
		 FROM peer_audit_log%s ORDER BY ts DESC LIMIT ?`, where)
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		e, err := scanAuditRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (s *SQLiteStorage) CountOpsInWindow(peerNodeID, localAgent string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM peer_audit_log
		 WHERE peer_node_id = ? AND agent = ? AND ts >= ? AND status != ?`,
		peerNodeID, localAgent, since.UTC(), AuditStatusDenied,
	).Scan(&n)
	return n, err
}

// --- row scanners ---

// rowScanner abstracts *sql.Row vs *sql.Rows so the per-field scan logic
// only lives once.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPeer(r rowScanner) (*Peer, error) {
	var (
		p        Peer
		trust    string
		lastSeen sql.NullTime
	)
	err := r.Scan(&p.NodeID, &p.Handle, &p.Endpoint, &p.SharedSecret, &trust,
		&p.AddedBy, &p.AddedAt, &lastSeen, &p.Notes)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.TrustLevel = TrustLevel(trust)
	if lastSeen.Valid {
		p.LastSeenAt = lastSeen.Time
	}
	return &p, nil
}

func scanPeerRows(r rowScanner) (*Peer, error) {
	return scanPeer(r)
}

func scanGrant(r rowScanner) (*Grant, error) {
	var (
		g      Grant
		active int
	)
	err := r.Scan(&g.ID, &g.PeerNodeID, &g.LocalAgent, &g.MaxTokensPerOp,
		&g.MaxOpsPerHour, &active, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.Active = active != 0
	return &g, nil
}

func scanGrantRows(r rowScanner) (*Grant, error) {
	return scanGrant(r)
}

func scanAuditRows(r rowScanner) (*AuditEntry, error) {
	var e AuditEntry
	err := r.Scan(&e.ID, &e.Timestamp, &e.Direction, &e.PeerNodeID, &e.PeerHandle,
		&e.Agent, &e.OpID, &e.TokensIn, &e.TokensOut, &e.CostUSD,
		&e.Status, &e.DenialReason, &e.DurationMS)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- Settings ---

func (s *SQLiteStorage) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM peering_settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *SQLiteStorage) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO peering_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}
