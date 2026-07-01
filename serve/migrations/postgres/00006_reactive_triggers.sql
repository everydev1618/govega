-- +goose Up
-- Reactive triggers for composed agents (reactive-agents Phase 2).
--
-- Stores each agent's declared triggers (the events it wakes on) as a JSON
-- array, mirroring skills/tools/team. Restored into dsl.Agent.Triggers at boot
-- so a Hera-created reactive agent survives restarts. See
-- docs/reactive-agents-design.md (D1) + serve.restoreComposedAgents.

ALTER TABLE composed_agents
    ADD COLUMN IF NOT EXISTS triggers TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE composed_agents
    DROP COLUMN IF EXISTS triggers;
