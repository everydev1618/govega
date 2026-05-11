package serve

// PostgresStore is the Postgres-backed implementation of Store (refs
// govega#61). Lives alongside SQLiteStore — pick via Config.DBKind.
//
// SQLite stays the zero-config default for single-user / dev / hobby
// hosts; Postgres is for multi-writer hosted deployments where SQLite's
// single-writer model and lack of row-level isolation start to bite.
//
// Implementation uses database/sql via the pgx stdlib driver so the
// query-running shape mirrors SQLiteStore as closely as possible. The
// dialect differences (placeholder syntax, upsert, autoincrement,
// blob, timestamp) are spelled out per-method rather than wrapped in
// a translation layer — easier to read, easier to debug.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements Store on top of Postgres.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens a connection pool against the supplied URL
// (postgres:// scheme). Pings once to fail fast on a bad URL.
func NewPostgresStore(url string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

// Close shuts down the connection pool.
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// Init creates the schema. Idempotent — every CREATE uses IF NOT EXISTS
// so re-running against an upgraded database is safe.
func (s *PostgresStore) Init() error {
	if _, err := s.db.Exec(postgresSchema); err != nil {
		return fmt.Errorf("postgres init: %w", err)
	}
	return nil
}

// postgresSchema mirrors the SQLite schema with Postgres-native types.
// Indexes match the SQLite ones so query plans are equivalent.
const postgresSchema = `
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
    description TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    team        TEXT NOT NULL DEFAULT '[]',
    mode        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS channel_messages (
    id              BIGSERIAL PRIMARY KEY,
    channel_id      TEXT NOT NULL,
    agent           TEXT NOT NULL,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    thread_id       BIGINT,
    metadata        TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tool_activities TEXT NOT NULL DEFAULT '[]',
    sender          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_channel_messages_channel ON channel_messages(channel_id);
CREATE INDEX IF NOT EXISTS idx_channel_messages_thread ON channel_messages(thread_id);

CREATE TABLE IF NOT EXISTS channel_reads (
    channel_id  TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    last_read_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (channel_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat_reads (
    agent      TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    last_read_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent, user_id)
);

CREATE TABLE IF NOT EXISTS inbox_items (
    id          BIGSERIAL PRIMARY KEY,
    from_agent  TEXT NOT NULL,
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL,
    priority    TEXT NOT NULL DEFAULT 'normal',
    status      TEXT NOT NULL DEFAULT 'pending',
    resolution  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ
);

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
    creator     TEXT NOT NULL DEFAULT '',
    parent_id   TEXT NOT NULL DEFAULT '',
    tags        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS task_comments (
    id         BIGSERIAL PRIMARY KEY,
    task_id    TEXT NOT NULL,
    author     TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_comments_task ON task_comments(task_id);

CREATE TABLE IF NOT EXISTS task_processes (
    task_id    TEXT NOT NULL,
    process_id TEXT NOT NULL,
    linked_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, process_id)
);
`

// --- Stubs ---
//
// Phase 3 will replace these with real implementations. They return
// ErrPostgresNotImplemented so attempting to run the server with
// DBKind=postgres before Phase 3 lands fails loudly rather than
// silently mis-behaving.

// errPostgresNotImplemented is returned by the unfinished method stubs.
// Phase 3 of govega#61 swaps them out for real implementations.
var errPostgresNotImplemented = fmt.Errorf("postgres: method not yet implemented (refs govega#61 phase 3)")

func (s *PostgresStore) InsertEvent(StoreEvent) error    { return errPostgresNotImplemented }
func (s *PostgresStore) InsertProcessSnapshot(ProcessSnapshot) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) InsertWorkflowRun(WorkflowRun) error      { return errPostgresNotImplemented }
func (s *PostgresStore) UpdateWorkflowRun(string, string, string) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) ListEvents(int) ([]StoreEvent, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) SearchEvents(ActivityFilter) ([]StoreEvent, int, error) {
	return nil, 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListProcessSnapshots() ([]ProcessSnapshot, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListWorkflowRuns(int) ([]WorkflowRun, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) InsertComposedAgent(ComposedAgent) error    { return errPostgresNotImplemented }
func (s *PostgresStore) ListComposedAgents() ([]ComposedAgent, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteComposedAgent(string) error           { return errPostgresNotImplemented }
func (s *PostgresStore) InsertChatMessage(string, string, string, []vega.ToolActivity) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) ListChatMessages(string) ([]ChatMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteChatMessages(string) error            { return errPostgresNotImplemented }
func (s *PostgresStore) UpsertUserMemory(string, string, string, string) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) GetUserMemory(string, string) ([]UserMemory, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteUserMemory(string, string) error      { return errPostgresNotImplemented }
func (s *PostgresStore) InsertMemoryItem(MemoryItem) (int64, error) { return 0, errPostgresNotImplemented }
func (s *PostgresStore) SearchMemoryItems(string, string, string, int) ([]MemoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) SearchMemoryItemsByType(string, string, string, MemoryType, int) ([]MemoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteMemoryItem(int64) error               { return errPostgresNotImplemented }
func (s *PostgresStore) ListMemoryItemsByTopic(string, string, string) ([]MemoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) UpsertScheduledJob(ScheduledJob) error      { return errPostgresNotImplemented }
func (s *PostgresStore) DeleteScheduledJob(string) error            { return errPostgresNotImplemented }
func (s *PostgresStore) ListScheduledJobs() ([]ScheduledJob, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) GetScheduledJobByID(string) (*ScheduledJob, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) MarkScheduledJobRun(string, time.Time) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) AgentSpendInPeriod(string, time.Time, time.Time) (float64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) InsertAgentBrainFile(AgentBrainFile) error  { return errPostgresNotImplemented }
func (s *PostgresStore) ListAgentBrainFiles(string) ([]AgentBrainFile, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) GetAgentBrainFile(string, string) (*AgentBrainFile, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteAgentBrainFile(string, string) error  { return errPostgresNotImplemented }
func (s *PostgresStore) InsertWorkspaceFile(WorkspaceFile) error    { return errPostgresNotImplemented }
func (s *PostgresStore) ListWorkspaceFiles(string) ([]WorkspaceFile, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListWorkspaceFileAgents() ([]string, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) UpsertSetting(Setting) error                { return errPostgresNotImplemented }
func (s *PostgresStore) GetSetting(string) (*Setting, error)        { return nil, errPostgresNotImplemented }
func (s *PostgresStore) ListSettings() ([]Setting, error)           { return nil, errPostgresNotImplemented }
func (s *PostgresStore) DeleteSetting(string) error                 { return errPostgresNotImplemented }
func (s *PostgresStore) CreateChannel(string, string, string, string, []string, string) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) GetChannel(string) (*Channel, error)        { return nil, errPostgresNotImplemented }
func (s *PostgresStore) GetChannelByName(string) (*dsl.ChannelInfo, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListAllChannels() ([]dsl.ChannelInfo, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListChannelsForAgent(string) ([]dsl.ChannelInfo, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListChannels(string) ([]Channel, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeleteChannel(string) error                 { return errPostgresNotImplemented }
func (s *PostgresStore) UpdateChannelTeam(string, []string) error   { return errPostgresNotImplemented }
func (s *PostgresStore) UpdateChannelMeta(string, *string, *string) error {
	return errPostgresNotImplemented
}
func (s *PostgresStore) FindChannelForAgents(string, string) (string, string, error) {
	return "", "", errPostgresNotImplemented
}
func (s *PostgresStore) InsertInboxItem(string, string, string, string) (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListInboxItems(string, int) ([]InboxItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) GetInboxItem(int64) (*InboxItem, error)     { return nil, errPostgresNotImplemented }
func (s *PostgresStore) ResolveInboxItem(int64, string) error       { return errPostgresNotImplemented }
func (s *PostgresStore) DeleteResolvedInboxItems() (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) InsertChannelMessage(string, string, string, string, *int64, string, string, []vega.ToolActivity) (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListChannelMessages(string, int) ([]ChannelMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) RecentChannelMessages(string, int) ([]dsl.ChannelMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListThreadMessages(string, int64) ([]ChannelMessage, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) MarkChannelRead(string, string) error       { return errPostgresNotImplemented }
func (s *PostgresStore) MarkChatRead(string, string) error          { return errPostgresNotImplemented }
func (s *PostgresStore) ChatUnreadCounts(string) (map[string]int, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ResetData() error                           { return errPostgresNotImplemented }
func (s *PostgresStore) InsertPromptHistory(string) (int64, error)  { return 0, errPostgresNotImplemented }
func (s *PostgresStore) ListPromptHistory(int) ([]PromptHistoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) SearchPromptHistory(string, int) ([]PromptHistoryItem, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) DeletePromptHistory(int64) error            { return errPostgresNotImplemented }
func (s *PostgresStore) InsertTask(Task) error                      { return errPostgresNotImplemented }
func (s *PostgresStore) GetTask(string) (*Task, error)              { return nil, errPostgresNotImplemented }
func (s *PostgresStore) ListTasks(TaskFilter) ([]Task, error)       { return nil, errPostgresNotImplemented }
func (s *PostgresStore) UpdateTask(string, TaskUpdate) error        { return errPostgresNotImplemented }
func (s *PostgresStore) DeleteTask(string) error                    { return errPostgresNotImplemented }
func (s *PostgresStore) AddTaskComment(string, string, string) (int64, error) {
	return 0, errPostgresNotImplemented
}
func (s *PostgresStore) ListTaskComments(string) ([]TaskComment, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) LinkTaskProcess(string, string) error       { return errPostgresNotImplemented }
func (s *PostgresStore) ListTaskProcesses(string) ([]string, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListMyTasks(string, []string, int) ([]Task, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) ListUnassignedTasks(int) ([]Task, error) {
	return nil, errPostgresNotImplemented
}
func (s *PostgresStore) UpdateTaskStatus(string, string) error      { return errPostgresNotImplemented }
func (s *PostgresStore) AssignTask(string, string) error            { return errPostgresNotImplemented }
func (s *PostgresStore) ClaimTask(string, string) error             { return errPostgresNotImplemented }
func (s *PostgresStore) TaskStatsByAssignee() (map[string]AgentStatsResponse, error) {
	return nil, errPostgresNotImplemented
}

// Compile-time assertion that PostgresStore satisfies Store.
var _ Store = (*PostgresStore)(nil)

// silence unused-import warnings for now — strings will be used by
// method implementations in Phase 3.
var _ = strings.TrimSpace
