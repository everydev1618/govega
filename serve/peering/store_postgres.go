package peering

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PostgresStorage implements Store on top of *sql.DB opened against a
// Postgres database via the pgx stdlib driver. The DDL lives in
// serve/migrations/postgres/00004_peering.sql and is applied by the goose
// runner inside PostgresStore.Init (serve/store_postgres_migrate.go).
type PostgresStorage struct {
	db *sql.DB
}

// NewPostgresStorage returns a Store backed by db. Migrations must already
// have been applied by the caller.
func NewPostgresStorage(db *sql.DB) *PostgresStorage {
	return &PostgresStorage{db: db}
}

// Compile-time assertion that both backends satisfy Store.
var (
	_ Store = (*SQLiteStorage)(nil)
	_ Store = (*PostgresStorage)(nil)
)

// --- Peers ---

func (s *PostgresStorage) UpsertPeer(p Peer) error {
	if p.TrustLevel == "" {
		p.TrustLevel = TrustScoped
	}
	_, err := s.db.Exec(
		`INSERT INTO peer_orchestrators
			(node_id, handle, endpoint, shared_secret, trust_level, added_by, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT(node_id) DO UPDATE SET
			handle = EXCLUDED.handle,
			endpoint = EXCLUDED.endpoint,
			shared_secret = EXCLUDED.shared_secret,
			trust_level = EXCLUDED.trust_level,
			added_by = EXCLUDED.added_by,
			notes = EXCLUDED.notes`,
		p.NodeID, p.Handle, p.Endpoint, p.SharedSecret, string(p.TrustLevel), p.AddedBy, p.Notes,
	)
	return err
}

func (s *PostgresStorage) GetPeer(nodeID string) (*Peer, error) {
	row := s.db.QueryRow(
		`SELECT node_id, handle, endpoint, shared_secret, trust_level,
			added_by, added_at, last_seen_at, notes
		 FROM peer_orchestrators WHERE node_id = $1`, nodeID,
	)
	return scanPeer(row)
}

func (s *PostgresStorage) ListPeers() ([]Peer, error) {
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

func (s *PostgresStorage) DeletePeer(nodeID string) error {
	_, err := s.db.Exec(`DELETE FROM peer_orchestrators WHERE node_id = $1`, nodeID)
	return err
}

func (s *PostgresStorage) TouchPeerLastSeen(nodeID string, t time.Time) error {
	_, err := s.db.Exec(`UPDATE peer_orchestrators SET last_seen_at = $1 WHERE node_id = $2`, t.UTC(), nodeID)
	return err
}

// --- Grants ---

func (s *PostgresStorage) UpsertGrant(g Grant) error {
	if g.MaxTokensPerOp == 0 {
		g.MaxTokensPerOp = 8000
	}
	if g.MaxOpsPerHour == 0 {
		g.MaxOpsPerHour = 30
	}
	_, err := s.db.Exec(
		`INSERT INTO peer_agent_grants
			(peer_node_id, local_agent, max_tokens_per_op, max_ops_per_hour, active)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT(peer_node_id, local_agent) DO UPDATE SET
			max_tokens_per_op = EXCLUDED.max_tokens_per_op,
			max_ops_per_hour = EXCLUDED.max_ops_per_hour,
			active = EXCLUDED.active`,
		g.PeerNodeID, g.LocalAgent, g.MaxTokensPerOp, g.MaxOpsPerHour, g.Active,
	)
	return err
}

func (s *PostgresStorage) GetGrant(peerNodeID, localAgent string) (*Grant, error) {
	row := s.db.QueryRow(
		`SELECT id, peer_node_id, local_agent, max_tokens_per_op, max_ops_per_hour, active, created_at
		 FROM peer_agent_grants WHERE peer_node_id = $1 AND local_agent = $2`,
		peerNodeID, localAgent,
	)
	return scanGrantPG(row)
}

func (s *PostgresStorage) ListGrants(peerNodeID string) ([]Grant, error) {
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
			 FROM peer_agent_grants WHERE peer_node_id = $1 ORDER BY local_agent`, peerNodeID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		g, err := scanGrantPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (s *PostgresStorage) DeleteGrant(peerNodeID, localAgent string) error {
	_, err := s.db.Exec(
		`DELETE FROM peer_agent_grants WHERE peer_node_id = $1 AND local_agent = $2`,
		peerNodeID, localAgent,
	)
	return err
}

// --- Audit ---

func (s *PostgresStorage) InsertAudit(e AuditEntry) (int64, error) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.Status == "" {
		e.Status = AuditStatusStarted
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO peer_audit_log
			(ts, direction, peer_node_id, peer_handle, agent, op_id,
			 tokens_in, tokens_out, cost_usd, status, denial_reason, duration_ms)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id`,
		e.Timestamp.UTC(), e.Direction, e.PeerNodeID, e.PeerHandle, e.Agent, e.OpID,
		e.TokensIn, e.TokensOut, e.CostUSD, e.Status, e.DenialReason, e.DurationMS,
	).Scan(&id)
	return id, err
}

func (s *PostgresStorage) FinalizeAudit(id int64, f AuditFinalize) error {
	_, err := s.db.Exec(
		`UPDATE peer_audit_log SET
			status = $1, tokens_in = $2, tokens_out = $3, cost_usd = $4,
			duration_ms = $5, denial_reason = $6
		 WHERE id = $7`,
		f.Status, f.TokensIn, f.TokensOut, f.CostUSD, f.DurationMS, f.DenialReason, id,
	)
	return err
}

func (s *PostgresStorage) ListAudit(f AuditFilter) ([]AuditEntry, error) {
	var (
		clauses []string
		args    []any
	)
	pos := 1
	addClause := func(col string, val any) {
		clauses = append(clauses, fmt.Sprintf("%s = $%d", col, pos))
		args = append(args, val)
		pos++
	}
	if f.PeerNodeID != "" {
		addClause("peer_node_id", f.PeerNodeID)
	}
	if f.Agent != "" {
		addClause("agent", f.Agent)
	}
	if f.Direction != "" {
		addClause("direction", f.Direction)
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
		 FROM peer_audit_log%s ORDER BY ts DESC LIMIT $%d`, where, pos)
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

func (s *PostgresStorage) CountOpsInWindow(peerNodeID, localAgent string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM peer_audit_log
		 WHERE peer_node_id = $1 AND agent = $2 AND ts >= $3 AND status != $4`,
		peerNodeID, localAgent, since.UTC(), AuditStatusDenied,
	).Scan(&n)
	return n, err
}

// scanGrantPG mirrors scanGrant but expects a BOOLEAN for active rather
// than SQLite's INTEGER 0/1.
func scanGrantPG(r rowScanner) (*Grant, error) {
	var g Grant
	err := r.Scan(&g.ID, &g.PeerNodeID, &g.LocalAgent, &g.MaxTokensPerOp,
		&g.MaxOpsPerHour, &g.Active, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}
