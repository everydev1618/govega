-- +goose Up
-- Triage counter for the orchestrator's inbox.
--
-- The orchestrator's list_inbox tool increments triage_count every time
-- an item is returned to it. After N reads without a resolution, the
-- store auto-ages the item to status='resolved' with a synthetic
-- resolution so the orchestrator stops paying token cost on items it
-- can't decide. See dsl.RegisterInboxTools + serve.Store.TriageInboxItems.

ALTER TABLE agent_inbox
    ADD COLUMN IF NOT EXISTS triage_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_triaged_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE agent_inbox
    DROP COLUMN IF EXISTS last_triaged_at,
    DROP COLUMN IF EXISTS triage_count;
