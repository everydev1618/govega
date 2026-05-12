-- +goose Up
-- Per-agent budget caps + enforcement state (refs govega#47).
-- period_start / period_end / observed_spend are derived at read time:
-- period is always the current calendar month UTC per the agent-budget
-- spec, and observed_spend is the existing AgentSpendInPeriod rollup
-- over process_snapshots.
CREATE TABLE IF NOT EXISTS agent_budgets (
    agent_name           TEXT PRIMARY KEY,
    budget_cap           DOUBLE PRECISION,
    soft_alert_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.8,
    enabled              BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS agent_budgets;
