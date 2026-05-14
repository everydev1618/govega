package peering

import (
	"errors"
	"time"
)

// TrustLevel enumerates the trust state of a peer orchestrator. See
// docs/peering-design.md §4.1.
type TrustLevel string

const (
	// TrustTrusted: peer may invoke any granted agent without per-op review.
	TrustTrusted TrustLevel = "trusted"
	// TrustScoped: same as TrustTrusted at the protocol layer; reserved for
	// future per-grant policy hooks. Current default for new peers.
	TrustScoped TrustLevel = "scoped"
	// TrustPaused: peer connections are accepted but every Invoke is denied.
	// Useful to quarantine a peer without losing the relationship.
	TrustPaused TrustLevel = "paused"
)

// Peer is one row of the peer_orchestrators table.
type Peer struct {
	NodeID       string
	Handle       string
	Endpoint     string
	SharedSecret string
	TrustLevel   TrustLevel
	AddedBy      string
	AddedAt      time.Time
	LastSeenAt   time.Time
	Notes        string
}

// Grant is one row of the peer_agent_grants table. It authorizes a single
// (peer, local-agent) pair.
type Grant struct {
	ID             int64
	PeerNodeID     string
	LocalAgent     string
	MaxTokensPerOp int
	MaxOpsPerHour  int
	Active         bool
	CreatedAt      time.Time
}

// Direction values for audit rows.
const (
	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"
)

// Audit status values. Started is written before dispatch so unfinished ops
// are visible; the row is updated to one of the terminal states on finish.
const (
	AuditStatusStarted   = "started"
	AuditStatusOK        = "ok"
	AuditStatusDenied    = "denied"
	AuditStatusError     = "error"
	AuditStatusCancelled = "cancelled"
)

// AuditEntry is one row of the peer_audit_log table.
type AuditEntry struct {
	ID           int64
	Timestamp    time.Time
	Direction    string
	PeerNodeID   string
	PeerHandle   string
	Agent        string
	OpID         int64
	TokensIn     int
	TokensOut    int
	CostUSD      float64
	Status       string
	DenialReason string
	DurationMS   int
}

// AuditFinalize carries the fields written by FinalizeAudit when an op ends.
type AuditFinalize struct {
	Status       string
	TokensIn     int
	TokensOut    int
	CostUSD      float64
	DurationMS   int
	DenialReason string
}

// AuditFilter narrows ListAudit. Zero-value fields are ignored.
type AuditFilter struct {
	PeerNodeID string
	Agent      string
	Direction  string
	Limit      int
}

// Store is the persistence surface required by the peering package. Both a
// SQLite-backed and a Postgres-backed implementation live in this package
// (NewSQLiteStorage, NewPostgresStorage); keeping the interface inside
// peering keeps the rest of govega's Store interface uncluttered.
type Store interface {
	// Peers.
	UpsertPeer(p Peer) error
	GetPeer(nodeID string) (*Peer, error)
	ListPeers() ([]Peer, error)
	DeletePeer(nodeID string) error
	TouchPeerLastSeen(nodeID string, t time.Time) error

	// Grants.
	UpsertGrant(g Grant) error
	GetGrant(peerNodeID, localAgent string) (*Grant, error)
	ListGrants(peerNodeID string) ([]Grant, error)
	DeleteGrant(peerNodeID, localAgent string) error

	// Audit.
	InsertAudit(e AuditEntry) (int64, error)
	FinalizeAudit(id int64, f AuditFinalize) error
	ListAudit(f AuditFilter) ([]AuditEntry, error)
	// CountOpsInWindow counts non-denied audit rows for (peer, agent) since
	// `since`. Denied rows do NOT count — see TestAudit_CountOpsInWindow_ExcludesDenied.
	CountOpsInWindow(peerNodeID, localAgent string, since time.Time) (int, error)

	// Settings. A tiny KV table for peering-local configuration that wants
	// stability across restarts (notably: the local NodeID). Returns "" + nil
	// when the key is unset so callers don't need a separate "exists" check.
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

// SettingNodeID is the settings key under which the locally-generated
// orchestrator NodeID is persisted (e.g. "vega:01ABC..."). See
// LoadOrGenerateNodeID in node.go.
const SettingNodeID = "local_node_id"

// ErrNilDB is returned by ApplySQLiteSchema / NewSQLiteStorage when handed a
// nil *sql.DB. Better than a panic deep inside a database/sql call site.
var ErrNilDB = errors.New("peering: nil *sql.DB")
