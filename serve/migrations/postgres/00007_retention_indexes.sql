-- +goose Up
-- Indexes supporting the retention sweeper (govega#114 Phase 5): the sweep
-- deletes by timestamp cutoff, which would otherwise full-scan the three
-- unbounded tables on every pass.

CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_snapshots_at ON process_snapshots(snapshot_at);
CREATE INDEX IF NOT EXISTS idx_chat_created ON chat_messages(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_events_timestamp;
DROP INDEX IF EXISTS idx_snapshots_at;
DROP INDEX IF EXISTS idx_chat_created;
