-- +goose Up
-- Peering tables for orchestrator-to-orchestrator federation over AIRE.
-- See docs/peering-design.md §4 for the trust + permission model.

CREATE TABLE IF NOT EXISTS peer_orchestrators (
    node_id        TEXT PRIMARY KEY,
    handle         TEXT NOT NULL DEFAULT '',
    endpoint       TEXT NOT NULL,
    shared_secret  TEXT NOT NULL,
    trust_level    TEXT NOT NULL DEFAULT 'scoped',
    added_by       TEXT NOT NULL DEFAULT '',
    added_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at   TIMESTAMPTZ,
    notes          TEXT NOT NULL DEFAULT ''
);
-- Partial unique index: handles are optional, but when present must be unique.
CREATE UNIQUE INDEX IF NOT EXISTS idx_peer_orchestrators_handle
    ON peer_orchestrators(handle) WHERE handle != '';

CREATE TABLE IF NOT EXISTS peer_agent_grants (
    id                BIGSERIAL PRIMARY KEY,
    peer_node_id      TEXT NOT NULL,
    local_agent       TEXT NOT NULL,
    max_tokens_per_op INTEGER NOT NULL DEFAULT 8000,
    max_ops_per_hour  INTEGER NOT NULL DEFAULT 30,
    active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(peer_node_id, local_agent)
);
CREATE INDEX IF NOT EXISTS idx_peer_agent_grants_peer
    ON peer_agent_grants(peer_node_id);

-- Audit rows are written before dispatch (status='started') and updated to
-- a terminal status on completion. Denials get a row with status='denied'
-- and a denial_reason so they're visible in the Federation modal.
CREATE TABLE IF NOT EXISTS peer_audit_log (
    id             BIGSERIAL PRIMARY KEY,
    ts             TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    direction      TEXT NOT NULL,
    peer_node_id   TEXT NOT NULL,
    peer_handle    TEXT NOT NULL DEFAULT '',
    agent          TEXT NOT NULL,
    op_id          BIGINT NOT NULL,
    tokens_in      INTEGER NOT NULL DEFAULT 0,
    tokens_out     INTEGER NOT NULL DEFAULT 0,
    cost_usd       DOUBLE PRECISION NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'started',
    denial_reason  TEXT NOT NULL DEFAULT '',
    duration_ms    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_peer_audit_peer_agent_ts
    ON peer_audit_log(peer_node_id, agent, ts DESC);
CREATE INDEX IF NOT EXISTS idx_peer_audit_ts
    ON peer_audit_log(ts DESC);

-- Peering-local key/value settings (local NodeID, etc.).
CREATE TABLE IF NOT EXISTS peering_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS peering_settings;
DROP TABLE IF EXISTS peer_audit_log;
DROP TABLE IF EXISTS peer_agent_grants;
DROP TABLE IF EXISTS peer_orchestrators;
