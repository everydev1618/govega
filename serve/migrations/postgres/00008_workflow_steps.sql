-- +goose Up
-- Per-step checkpoints for workflow durability (govega#114 Phase 5): the
-- server persists step lifecycle events on the run row so an interrupted
-- run shows where it died.

ALTER TABLE workflow_runs
    ADD COLUMN IF NOT EXISTS steps TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE workflow_runs
    DROP COLUMN IF EXISTS steps;
