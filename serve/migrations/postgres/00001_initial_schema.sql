-- +goose Up
-- Initial schema baseline (refs govega#62). Every table from the
-- pre-goose Postgres init lives here verbatim. Existing fresh
-- Postgres installs sit at this version after first boot; future
-- changes ship as new numbered migrations.

CREATE TABLE IF NOT EXISTS events (
    id          BIGSERIAL PRIMARY KEY,
    type        TEXT NOT NULL,
    process_id  TEXT NOT NULL DEFAULT '',
    agent_name  TEXT NOT NULL DEFAULT '',
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    data        TEXT NOT NULL DEFAULT '',
    result      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_events_process ON events(process_id);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_agent ON events(agent_name);

CREATE TABLE IF NOT EXISTS process_snapshots (
    id            BIGSERIAL PRIMARY KEY,
    process_id    TEXT NOT NULL,
    agent_name    TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    parent_id     TEXT NOT NULL DEFAULT '',
    input_tokens  BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    snapshot_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_snapshots_process ON process_snapshots(process_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_agent ON process_snapshots(agent_name);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id         BIGSERIAL PRIMARY KEY,
    run_id     TEXT NOT NULL UNIQUE,
    workflow   TEXT NOT NULL,
    inputs     TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL DEFAULT 'running',
    result     TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS composed_agents (
    name            TEXT PRIMARY KEY,
    persona         TEXT NOT NULL DEFAULT '',
    skills          TEXT NOT NULL DEFAULT '[]',
    team            TEXT NOT NULL DEFAULT '[]',
    model           TEXT NOT NULL DEFAULT '',
    system          TEXT NOT NULL DEFAULT '',
    temperature     DOUBLE PRECISION,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tools           TEXT NOT NULL DEFAULT '[]',
    display_name    TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL DEFAULT '',
    avatar          TEXT NOT NULL DEFAULT '',
    icon            TEXT NOT NULL DEFAULT '',
    avatar_gradient TEXT NOT NULL DEFAULT '[]',
    updated_at      TIMESTAMPTZ,
    description     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id              BIGSERIAL PRIMARY KEY,
    agent           TEXT NOT NULL,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tool_activities TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_agent ON chat_messages(agent);

CREATE TABLE IF NOT EXISTS user_memory (
    user_id    TEXT NOT NULL,
    agent      TEXT NOT NULL,
    layer      TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, agent, layer)
);

CREATE TABLE IF NOT EXISTS scheduled_jobs (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    cron          TEXT NOT NULL,
    agent_name    TEXT NOT NULL,
    message       TEXT NOT NULL,
    schedule_json TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_agent ON scheduled_jobs(agent_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduled_jobs_name ON scheduled_jobs(name);

CREATE TABLE IF NOT EXISTS agent_brain_files (
    id         TEXT PRIMARY KEY,
    agent_name TEXT NOT NULL,
    name       TEXT NOT NULL,
    mime_type  TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    content    BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_agent_brain_files_agent ON agent_brain_files(agent_name);

CREATE TABLE IF NOT EXISTS memory_items (
    id         BIGSERIAL PRIMARY KEY,
    user_id    TEXT NOT NULL,
    agent      TEXT NOT NULL,
    type       TEXT NOT NULL DEFAULT 'reference',
    topic      TEXT NOT NULL DEFAULT '',
    content    TEXT NOT NULL,
    tags       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_memory_items_user_agent ON memory_items(user_id, agent);
CREATE INDEX IF NOT EXISTS idx_memory_items_topic ON memory_items(user_id, agent, topic);
CREATE INDEX IF NOT EXISTS idx_memory_items_dedup ON memory_items(user_id, agent, type, content);

CREATE TABLE IF NOT EXISTS workspace_files (
    id          BIGSERIAL PRIMARY KEY,
    path        TEXT NOT NULL,
    agent       TEXT NOT NULL DEFAULT '',
    process_id  TEXT NOT NULL DEFAULT '',
    operation   TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_workspace_files_agent ON workspace_files(agent);

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    sensitive  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS mcp_servers (
    name       TEXT PRIMARY KEY,
    config     TEXT NOT NULL,
    disabled   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS channels (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    team        TEXT DEFAULT '[]',
    mode        TEXT DEFAULT '',
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS channel_messages (
    id              BIGSERIAL PRIMARY KEY,
    channel_id      TEXT NOT NULL,
    thread_id       BIGINT,
    agent           TEXT DEFAULT '',
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    metadata        TEXT DEFAULT '{}',
    tool_activities TEXT NOT NULL DEFAULT '[]',
    sender          TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_messages_channel ON channel_messages(channel_id, created_at);
CREATE INDEX IF NOT EXISTS idx_channel_messages_thread ON channel_messages(thread_id);

CREATE TABLE IF NOT EXISTS channel_read_cursors (
    channel_id   TEXT NOT NULL,
    user_id      TEXT NOT NULL DEFAULT 'default',
    last_read_id BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat_read_cursors (
    agent        TEXT NOT NULL,
    user_id      TEXT NOT NULL DEFAULT 'default',
    last_read_id BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent, user_id)
);

CREATE TABLE IF NOT EXISTS agent_inbox (
    id          BIGSERIAL PRIMARY KEY,
    from_agent  TEXT NOT NULL,
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    priority    TEXT NOT NULL DEFAULT 'normal',
    status      TEXT NOT NULL DEFAULT 'pending',
    resolution  TEXT DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agent_inbox_status ON agent_inbox(status, created_at);

CREATE TABLE IF NOT EXISTS inbox_replies (
    id         BIGSERIAL PRIMARY KEY,
    inbox_id   BIGINT NOT NULL REFERENCES agent_inbox(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    agent      TEXT NOT NULL DEFAULT '',
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_inbox_replies_inbox ON inbox_replies(inbox_id, created_at);

CREATE TABLE IF NOT EXISTS prompt_history (
    id         BIGSERIAL PRIMARY KEY,
    prompt     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'todo',
    priority    TEXT NOT NULL DEFAULT 'normal',
    assignee    TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    due_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_tasks_status   ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);

CREATE TABLE IF NOT EXISTS task_comments (
    id         BIGSERIAL PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author     TEXT NOT NULL DEFAULT '',
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_comments_task ON task_comments(task_id, created_at);

CREATE TABLE IF NOT EXISTS task_processes (
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    process_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, process_id)
);

-- +goose Down
-- Down for the initial baseline drops every table — destructive enough
-- that we keep it as a clean reset path rather than something to run
-- in production. Goose will refuse to run this against a populated
-- system unless explicitly asked.
DROP TABLE IF EXISTS task_processes;
DROP TABLE IF EXISTS task_comments;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS prompt_history;
DROP TABLE IF EXISTS inbox_replies;
DROP TABLE IF EXISTS agent_inbox;
DROP TABLE IF EXISTS chat_read_cursors;
DROP TABLE IF EXISTS channel_read_cursors;
DROP TABLE IF EXISTS channel_messages;
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS mcp_servers;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS workspace_files;
DROP TABLE IF EXISTS memory_items;
DROP TABLE IF EXISTS agent_brain_files;
DROP TABLE IF EXISTS scheduled_jobs;
DROP TABLE IF EXISTS user_memory;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS composed_agents;
DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS process_snapshots;
DROP TABLE IF EXISTS events;
