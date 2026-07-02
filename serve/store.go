package serve

import (
	"fmt"
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
)

// Store persists events and process snapshots for historical queries.
type Store interface {
	// Init creates tables if they don't exist.
	Init() error

	// Close closes the store.
	Close() error

	// InsertEvent records an orchestration event.
	InsertEvent(e StoreEvent) error

	// InsertProcessSnapshot records a process state snapshot.
	InsertProcessSnapshot(s ProcessSnapshot) error

	// InsertWorkflowRun records a workflow execution.
	InsertWorkflowRun(r WorkflowRun) error

	// UpdateWorkflowRun updates a workflow run status.
	UpdateWorkflowRun(runID string, status string, result string) error

	// UpdateWorkflowRunSteps replaces the per-step checkpoint JSON on a
	// workflow run so interrupted runs show where they died.
	UpdateWorkflowRunSteps(runID string, stepsJSON string) error

	// ReconcileOrphanedWorkflowRuns marks runs stuck at status='running'
	// as 'interrupted'. Called at boot: runs execute in-process, so any
	// 'running' row at startup died with the previous server. Returns
	// the number of runs reconciled.
	ReconcileOrphanedWorkflowRuns() (int64, error)

	// ListEvents returns recent events, newest first.
	ListEvents(limit int) ([]StoreEvent, error)

	// SearchEvents returns events matching the filter, newest first.
	// Used by the activity log endpoint (refs govega#33). totalCount is
	// the unpaginated match count so the FE can render a hit total
	// without re-querying.
	SearchEvents(filter ActivityFilter) (events []StoreEvent, totalCount int, err error)

	// ListProcessSnapshots returns the latest snapshot per process.
	ListProcessSnapshots() ([]ProcessSnapshot, error)

	// ListWorkflowRuns returns recent workflow runs.
	ListWorkflowRuns(limit int) ([]WorkflowRun, error)

	// InsertComposedAgent persists a composed agent definition.
	InsertComposedAgent(a ComposedAgent) error

	// ListComposedAgents returns all composed agents.
	ListComposedAgents() ([]ComposedAgent, error)

	// DeleteComposedAgent removes a composed agent by name.
	DeleteComposedAgent(name string) error

	// InsertChatMessage persists a chat message. `activities` is the
	// list of completed tool calls captured during the streaming turn
	// that produced this message; pass nil for user messages or any
	// message without tool calls.
	InsertChatMessage(agent, role, content string, activities []vega.ToolActivity) error

	// ListChatMessages returns chat history for an agent.
	ListChatMessages(agent string) ([]ChatMessage, error)

	// DeleteChatMessages removes all chat messages for an agent.
	DeleteChatMessages(agent string) error

	// SweepRetention deletes rows older than the per-table retention
	// windows. A zero duration keeps that table's rows forever. Returns
	// the number of rows deleted per table.
	SweepRetention(policy RetentionPolicy) (RetentionSweepResult, error)

	// UpsertMCPServer persists an MCP server connection config so it
	// auto-reconnects on restart.
	UpsertMCPServer(name, configJSON string) error

	// DeleteMCPServer removes a persisted MCP server connection.
	DeleteMCPServer(name string) error

	// ListMCPServers returns all persisted MCP server configs.
	ListMCPServers() ([]MCPServerConfig, error)

	// SetMCPServerDisabled enables or disables a persisted MCP server.
	// Errors when no server with that name exists.
	SetMCPServerDisabled(name string, disabled bool) error

	// UpsertMemoryPage creates a page if absent, or overwrites Content +
	// Frontmatter + advances UpdatedAt on conflict (PK = scope, scope_id,
	// user_id, path). CreatedAt is preserved across updates. Refs govega#71.
	UpsertMemoryPage(p MemoryPage) error

	// GetMemoryPage returns one page, or nil when not found.
	GetMemoryPage(scope MemoryScope, scopeID, userID, path string) (*MemoryPage, error)

	// ListMemoryPages returns every page under (scope, scopeID, userID).
	// When pathPrefix is non-empty, results are filtered to paths that
	// start with the prefix. Ordered by updated_at DESC.
	ListMemoryPages(scope MemoryScope, scopeID, userID, pathPrefix string) ([]MemoryPage, error)

	// DeleteMemoryPage removes one page. Also clears any link rows where
	// the page appears as from_path or to_path.
	DeleteMemoryPage(scope MemoryScope, scopeID, userID, path string) error

	// RenameMemoryPage moves a page from oldPath to newPath, and rewrites
	// every link row that referenced oldPath (as from_path or to_path) to
	// point at newPath. Atomic: either both succeed or neither does.
	RenameMemoryPage(scope MemoryScope, scopeID, userID, oldPath, newPath string) error

	// SearchMemoryPages does a case-insensitive substring search across
	// path and content, ranked by updated_at DESC. Limit defaults to 25.
	SearchMemoryPages(scope MemoryScope, scopeID, userID, query string, limit int) ([]MemoryPage, error)

	// ReplaceMemoryLinks replaces the set of out-edges from fromPath
	// atomically: removes existing rows where from_path = fromPath,
	// inserts one row per (fromPath, to) in toPaths. Empty toPaths just
	// clears the out-edges. Refs govega#71.
	ReplaceMemoryLinks(scope MemoryScope, scopeID, userID, fromPath string, toPaths []string) error

	// ListMemoryLinks returns every link under (scope, scopeID, userID).
	ListMemoryLinks(scope MemoryScope, scopeID, userID string) ([]MemoryLink, error)

	// ListMemoryScopeIDs returns every distinct scope_id that has at least
	// one page under (scope, userID). Used by the graph endpoint to
	// enumerate per-agent wikis when scope=all.
	ListMemoryScopeIDs(scope MemoryScope, userID string) ([]string, error)

	// UpsertUserMemory creates or updates a memory layer for a user+agent.
	UpsertUserMemory(userID, agent, layer, content string) error

	// GetUserMemory returns all memory layers for a user+agent.
	GetUserMemory(userID, agent string) ([]UserMemory, error)

	// DeleteUserMemory removes all memory for a user+agent.
	DeleteUserMemory(userID, agent string) error

	// InsertMemoryItem saves a memory item. If an item already exists with the
	// same (user_id, agent, type, content), its tags are merged and updated_at
	// advances rather than inserting a duplicate row. Items missing a Type are
	// stored as MemoryTypeReference.
	InsertMemoryItem(item MemoryItem) (int64, error)

	// SearchMemoryItems searches memory items by keyword across topic, content, and tags.
	SearchMemoryItems(userID, agent, query string, limit int) ([]MemoryItem, error)

	// SearchMemoryItemsByType is like SearchMemoryItems but additionally
	// filters to a single MemoryType.
	SearchMemoryItemsByType(userID, agent, query string, typ MemoryType, limit int) ([]MemoryItem, error)

	// DeleteMemoryItem removes a memory item by ID.
	DeleteMemoryItem(id int64) error

	// ListAllUserMemory returns every row in user_memory. Bulk-load helper
	// used by the wiki-memory migration (govega#71). Removed once the
	// legacy user_memory table is dropped.
	ListAllUserMemory() ([]UserMemory, error)

	// ListAllMemoryItems returns every row in memory_items. Bulk-load
	// helper for the wiki-memory migration. Removed alongside the table.
	ListAllMemoryItems() ([]MemoryItem, error)

	// ListMemoryItemsByTopic returns memory items for a given user+agent+topic.
	ListMemoryItemsByTopic(userID, agent, topic string) ([]MemoryItem, error)

	// UpsertScheduledJob creates or replaces a scheduled job.
	UpsertScheduledJob(job ScheduledJob) error

	// DeleteScheduledJob removes a scheduled job by name.
	DeleteScheduledJob(name string) error

	// ListScheduledJobs returns all scheduled jobs.
	ListScheduledJobs() ([]ScheduledJob, error)

	// GetScheduledJobByID returns one job by its server-generated id.
	// Returns nil, nil when not found.
	GetScheduledJobByID(id string) (*ScheduledJob, error)

	// MarkScheduledJobRun stamps the supplied time on last_run_at.
	MarkScheduledJobRun(name string, at time.Time) error

	// GetAgentBudget returns the persisted budget record for agentName,
	// or nil when the agent has no row yet (i.e. budget is implicitly
	// "no cap, soft-alert threshold default, disabled"). Refs govega#47.
	GetAgentBudget(agentName string) (*AgentBudget, error)

	// UpsertAgentBudget creates or replaces a per-agent budget row.
	// CreatedAt is preserved on update; UpdatedAt advances. Refs govega#47.
	UpsertAgentBudget(b AgentBudget) error

	// AgentSpendInPeriod returns the sum of cost_usd across the latest
	// snapshot of every process for agentName whose started_at falls
	// inside [from, to). When from/to are zero, the bound is treated
	// as unbounded on that side.
	AgentSpendInPeriod(agentName string, from, to time.Time) (float64, error)

	// InsertAgentBrainFile persists an attachment for the given agent.
	// content is read in-memory; callers must enforce size limits.
	InsertAgentBrainFile(f AgentBrainFile) error

	// ListAgentBrainFiles returns the metadata for every brain file
	// attached to agentName. Content is not loaded.
	ListAgentBrainFiles(agentName string) ([]AgentBrainFile, error)

	// GetAgentBrainFile returns one brain file, content included.
	// Returns nil, nil when the file doesn't exist or doesn't belong to
	// the agent.
	GetAgentBrainFile(agentName, id string) (*AgentBrainFile, error)

	// DeleteAgentBrainFile removes a brain file. Returns an error if the
	// file doesn't exist on the given agent.
	DeleteAgentBrainFile(agentName, id string) error

	// InsertWorkspaceFile records a file write by an agent.
	InsertWorkspaceFile(f WorkspaceFile) error

	// ListWorkspaceFiles returns workspace file records, optionally filtered by agent.
	ListWorkspaceFiles(agent string) ([]WorkspaceFile, error)

	// ListWorkspaceFileAgents returns distinct agent names that have written files.
	ListWorkspaceFileAgents() ([]string, error)

	// UpsertSetting creates or updates a setting.
	UpsertSetting(s Setting) error

	// GetSetting returns a setting by key.
	GetSetting(key string) (*Setting, error)

	// ListSettings returns all settings.
	ListSettings() ([]Setting, error)

	// DeleteSetting removes a setting by key.
	DeleteSetting(key string) error

	// CreateChannel creates a new channel.
	CreateChannel(id, name, description, createdBy string, team []string, mode string) error

	// GetChannel returns a channel by name.
	GetChannel(name string) (*Channel, error)

	// GetChannelByName returns minimal channel info for the dsl.ChannelBackend interface.
	GetChannelByName(name string) (*dsl.ChannelInfo, error)

	// ListAllChannels returns all channels as ChannelInfo.
	ListAllChannels() ([]dsl.ChannelInfo, error)

	// ListChannelsForAgent returns channels where the agent is a team member.
	ListChannelsForAgent(agent string) ([]dsl.ChannelInfo, error)

	// ListChannels returns all channels with unread counts for the given user.
	ListChannels(userID string) ([]Channel, error)

	// DeleteChannel removes a channel by name.
	DeleteChannel(name string) error

	// UpdateChannelTeam updates the team members of a channel.
	UpdateChannelTeam(name string, team []string) error

	// UpdateChannelMeta partially updates a channel's display fields.
	// Pass nil to leave a field unchanged; empty-string pointer clears.
	// Returns sql.ErrNoRows if the channel doesn't exist.
	UpdateChannelMeta(currentName string, newName, newDescription *string) error

	// FindChannelForAgents returns the channel where both agents are team members.
	FindChannelForAgents(agent1, agent2 string) (channelID string, channelName string, err error)

	// InsertInboxItem creates a new inbox item.
	InsertInboxItem(fromAgent, subject, body, priority string) (int64, error)

	// InsertResolvedInboxItem creates an inbox item that is already resolved.
	// Used by the success path of classifyDispatchOutcome so "Task
	// completed by X" entries skip the pending queue and land directly in
	// Done — keeps the orchestrator's working set focused on items that
	// actually need a decision. The resolution string is required and
	// surfaces in the UI's Done column.
	InsertResolvedInboxItem(fromAgent, subject, body, resolution string) (int64, error)

	// ListInboxItems returns inbox items filtered by status.
	ListInboxItems(status string, limit int) ([]InboxItem, error)

	// GetInboxItem returns a single inbox item by ID.
	GetInboxItem(id int64) (*InboxItem, error)

	// ResolveInboxItem marks an inbox item as resolved.
	ResolveInboxItem(id int64, resolution string) error

	// DeleteResolvedInboxItems removes all resolved inbox items and their replies.
	DeleteResolvedInboxItems() (int64, error)

	// DeleteInboxItem removes a single inbox item (and its replies) by id,
	// regardless of status. Returns sql.ErrNoRows if no item matches.
	DeleteInboxItem(id int64) error

	// TriageInboxItems increments triage_count + stamps last_triaged_at
	// for each pending id, then auto-resolves any whose post-increment
	// count is >= threshold. Returns the ids that were auto-aged on
	// this call so callers can log/report. Already-resolved items and
	// unknown ids are silently ignored. The threshold guard is what
	// stops the orchestrator burning tokens re-reading items it can't
	// decide every heartbeat.
	TriageInboxItems(ids []int64, threshold int) ([]int64, error)

	// InsertChannelMessage inserts a message into a channel.
	InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string, activities []vega.ToolActivity) (int64, error)

	// ListChannelMessages returns top-level messages for a channel with reply counts.
	ListChannelMessages(channelID string, limit int) ([]ChannelMessage, error)

	// RecentChannelMessages returns the last N messages (lightweight, for status checks).
	RecentChannelMessages(channelID string, limit int) ([]dsl.ChannelMessage, error)

	// ListThreadMessages returns all replies in a thread.
	ListThreadMessages(channelID string, threadID int64) ([]ChannelMessage, error)

	// MarkChannelRead updates the read cursor for a channel.
	MarkChannelRead(channelID, userID string) error

	// MarkChatRead updates the read cursor for a DM conversation.
	MarkChatRead(agent, userID string) error

	// ChatUnreadCounts returns agent → unread message count for DMs.
	ChatUnreadCounts(userID string) (map[string]int, error)

	// ResetData clears all transient data (chat, memory, agents, files, etc.)
	// but preserves settings and prompt history.
	ResetData() error

	// InsertPromptHistory records an original user prompt to iris.
	InsertPromptHistory(prompt string) (int64, error)

	// ListPromptHistory returns prompt history entries, newest first.
	ListPromptHistory(limit int) ([]PromptHistoryItem, error)

	// SearchPromptHistory searches prompt history by keyword.
	SearchPromptHistory(query string, limit int) ([]PromptHistoryItem, error)

	// DeletePromptHistory removes a prompt history entry by ID.
	DeletePromptHistory(id int64) error

	// InsertTask creates a new task.
	InsertTask(t Task) error

	// GetTask returns a task by id, or (nil, nil) if not found.
	GetTask(id string) (*Task, error)

	// ListTasks returns tasks matching the filter, newest-updated first.
	ListTasks(f TaskFilter) ([]Task, error)

	// UpdateTask applies a partial update.
	UpdateTask(id string, u TaskUpdate) error

	// DeleteTask removes a task and its comments / process links.
	DeleteTask(id string) error

	// AddTaskComment appends a comment and bumps the task's updated_at.
	AddTaskComment(taskID, author, content string) (int64, error)

	// ListTaskComments returns comments oldest-first.
	ListTaskComments(taskID string) ([]TaskComment, error)

	// LinkTaskProcess associates a process with a task (idempotent).
	LinkTaskProcess(taskID, processID string) error

	// ListTaskProcesses returns process IDs linked to a task.
	ListTaskProcesses(taskID string) ([]string, error)

	// ListMyTasks returns tasks assigned to a specific agent.
	ListMyTasks(assignee string, status []string, limit int) ([]Task, error)

	// ListUnassignedTasks returns tasks with empty assignee.
	ListUnassignedTasks(limit int) ([]Task, error)

	// UpdateTaskStatus is a focused setter for the agent tool layer.
	UpdateTaskStatus(id, status string) error

	// AssignTask sets the assignee without changing status.
	AssignTask(id, assignee string) error

	// ClaimTask atomically sets assignee to caller and status to 'doing'.
	ClaimTask(id, assignee string) error

	// TaskStatsByAssignee returns per-assignee kanban task counters in a
	// single query. Used by the agents API to surface "how productive is
	// this agent" stats. Empty assignees are excluded.
	TaskStatsByAssignee() (map[string]AgentStatsResponse, error)
}

// MemoryScope distinguishes a shared-user wiki from a per-agent
// working-notes wiki. Refs govega#71.
type MemoryScope string

const (
	// MemoryScopeUser is the shared wiki for a user, readable and
	// writable by every agent that talks to them. ScopeID equals UserID.
	MemoryScopeUser MemoryScope = "user"

	// MemoryScopeAgent is an agent's private working notes about a
	// specific user. ScopeID is the agent name; UserID is the user the
	// notes pertain to.
	MemoryScopeAgent MemoryScope = "agent"
)

// MemoryPage is a single page in a wiki-style memory store. Path is a
// logical slash-separated address (e.g. "MEMORY.md", "topics/sushi.md"),
// not a filesystem path. Refs govega#71.
type MemoryPage struct {
	Scope       MemoryScope `json:"scope"`
	ScopeID     string      `json:"scope_id"`
	UserID      string      `json:"user_id"`
	Path        string      `json:"path"`
	Content     string      `json:"content"`
	Frontmatter string      `json:"frontmatter,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// MemoryLink is a directed link between two memory pages, extracted
// from a page's content at write time. Drives the graph endpoint.
type MemoryLink struct {
	Scope    MemoryScope `json:"scope"`
	ScopeID  string      `json:"scope_id"`
	UserID   string      `json:"user_id"`
	FromPath string      `json:"from_path"`
	ToPath   string      `json:"to_path"`
}

// UserMemory is a persisted memory layer for a user+agent pair.
type UserMemory struct {
	UserID    string    `json:"user_id"`
	Agent     string    `json:"agent"`
	Layer     string    `json:"layer"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChatMessage is a persisted chat message.
type ChatMessage struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	// ToolActivities captures completed tool calls from the assistant
	// turn that produced this message. Empty for user messages; populated
	// for assistant messages that invoked tools during streaming.
	ToolActivities []vega.ToolActivity `json:"tool_activities,omitempty"`
}

// ActivityFilter is the search filter for the activity log endpoint
// (refs govega#33). All fields are optional — a zero-value filter
// returns the most recent events.
type ActivityFilter struct {
	// Query is a substring matched against type, agent_name, data,
	// result, and error (case-insensitive LIKE). Empty means no text
	// filter.
	Query string
	// Type narrows to one event type (e.g. "process.failed").
	Type string
	// Agent narrows to one agent_name.
	Agent string
	// From and To bound the timestamp range. Zero values are unbounded.
	From time.Time
	To   time.Time
	// Limit caps the number of events returned. Defaults to 100; max 500.
	Limit int
	// Offset skips that many matching events (paginate alongside Limit).
	Offset int
}

// StoreEvent is a persisted orchestration event.
// RetentionPolicy sets per-table retention windows for SweepRetention.
// A zero duration keeps that table's rows forever.
type RetentionPolicy struct {
	Events       time.Duration
	Snapshots    time.Duration
	ChatMessages time.Duration
}

// RetentionSweepResult reports rows deleted per table by one sweep.
type RetentionSweepResult struct {
	Events       int64
	Snapshots    int64
	ChatMessages int64
}

type StoreEvent struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	ProcessID string    `json:"process_id"`
	AgentName string    `json:"agent_name"`
	Timestamp time.Time `json:"timestamp"`
	Data      string    `json:"data"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// ProcessSnapshot is a point-in-time process state.
type ProcessSnapshot struct {
	ID          int64     `json:"id"`
	ProcessID   string    `json:"process_id"`
	AgentName   string    `json:"agent_name"`
	Status      string    `json:"status"`
	ParentID    string    `json:"parent_id,omitempty"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	CostUSD     float64   `json:"cost_usd"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	SnapshotAt  time.Time `json:"snapshot_at"`
}

// ComposedAgent is a persisted agent created via the compose API.
type ComposedAgent struct {
	Name           string    `json:"name"`
	DisplayName    string    `json:"display_name,omitempty"`
	Title          string    `json:"title,omitempty"`
	Description    string    `json:"description,omitempty"`
	Avatar         string    `json:"avatar,omitempty"`
	Icon           string    `json:"icon,omitempty"`
	AvatarGradient []string  `json:"avatar_gradient,omitempty"`
	Model          string    `json:"model"`
	Persona        string    `json:"persona,omitempty"`
	Skills         []string  `json:"skills,omitempty"`
	Tools          []string  `json:"tools,omitempty"`
	Team           []string  `json:"team,omitempty"`
	System         string           `json:"system,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	Triggers       []dsl.TriggerDef `json:"triggers,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// MemoryType discriminates between memory categories so the agent can
// retrieve the right kind for the moment. Mirrors the four-class scheme
// used by Claude Code's auto-memory system.
type MemoryType string

const (
	// MemoryTypeUser captures stable facts about who the user is, their
	// role, and how they prefer to work.
	MemoryTypeUser MemoryType = "user"
	// MemoryTypeFeedback captures corrections and confirmations the user
	// has given the agent (rules of engagement, validated approaches).
	MemoryTypeFeedback MemoryType = "feedback"
	// MemoryTypeProject captures context about ongoing work, decisions,
	// and motivations behind the current task.
	MemoryTypeProject MemoryType = "project"
	// MemoryTypeReference captures pointers to external systems and
	// documents — the catch-all default when no other type fits.
	MemoryTypeReference MemoryType = "reference"
)

// Validate reports whether the type is one of the four canonical values.
// Empty strings and case variants are rejected so a typo at the boundary
// fails fast rather than silently becoming a fifth type.
func (t MemoryType) Validate() error {
	switch t {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return nil
	default:
		return fmt.Errorf("invalid memory type %q (want one of: user, feedback, project, reference)", string(t))
	}
}

// MemoryItem is a persisted memory entry for project-aware recall.
type MemoryItem struct {
	ID        int64      `json:"id"`
	UserID    string     `json:"user_id"`
	Agent     string     `json:"agent"`
	Type      MemoryType `json:"type"`
	Topic     string     `json:"topic"`
	Content   string     `json:"content"`
	Tags      string     `json:"tags"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ScheduledJob is a persisted recurring agent trigger. The legacy fields
// (Name, Cron, Message) drive the cron runner and the DSL tools; the
// newer ones (ID, Title, ScheduleJSON, LastRunAt, UpdatedAt) back the
// per-agent Routines CRUD surface added for govega#52.
type ScheduledJob struct {
	// ID is the server-generated stable identifier. For rows created
	// before the routines migration, ID == Name (backfilled by the
	// migration).
	ID string `json:"id"`
	// Name is the cron-runner key + DSL lookup key. For new routines the
	// API sets Name == ID; legacy rows keep their original slug.
	Name string `json:"name"`
	// Title is the user-facing display label (e.g. "Daily standup
	// reminder"). Empty for legacy rows; falls back to Name in API
	// responses.
	Title string `json:"title,omitempty"`
	Cron  string `json:"cron"`
	// ScheduleJSON is the marshalled Schedule struct as supplied by the
	// FE. Empty when the row was created via the DSL or the legacy flat
	// API; in that case responses synthesize a `Custom` schedule that
	// echoes Cron.
	ScheduleJSON string     `json:"-"`
	AgentName    string     `json:"agent"`
	Message      string     `json:"message"`
	Enabled      bool       `json:"enabled"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AgentBudget is the persisted per-agent budget record (refs govega#47).
// period_start / period_end / observed_spend aren't fields — period is
// always the current calendar month UTC, and observed_spend is the
// existing AgentSpendInPeriod rollup. The response surface composes
// those at read time.
type AgentBudget struct {
	AgentName          string    `json:"agent"`
	BudgetCap          *float64  `json:"budget_cap"`
	SoftAlertThreshold float64   `json:"soft_alert_threshold"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// AgentBrainFile is a per-agent knowledge attachment uploaded via
// `POST /api/v1/agents/{name}/brain` (refs govega#43). Content is stored
// inline as a SQLite blob — fine for the FE's MVP (an attachment list,
// no RAG). Move to an object store if/when retrieval-at-chat-time lands.
type AgentBrainFile struct {
	ID        string    `json:"id"`
	AgentName string    `json:"agent_id"` // FE calls this agent_id
	Name      string    `json:"name"`
	MimeType  string    `json:"mime_type,omitempty"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	// Content is never JSON-serialized — only used internally for upload/download.
	Content []byte `json:"-"`
}

// WorkspaceFile tracks a file written by an agent.
type WorkspaceFile struct {
	ID          int64     `json:"id"`
	Path        string    `json:"path"`
	Agent       string    `json:"agent"`
	ProcessID   string    `json:"process_id"`
	Operation   string    `json:"operation"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Setting is a persisted key-value configuration entry.
type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Sensitive bool      `json:"sensitive"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PromptHistoryItem is a persisted original prompt sent to iris.
type PromptHistoryItem struct {
	ID        int64     `json:"id"`
	Prompt    string    `json:"prompt"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkflowRun is a persisted workflow execution.
type WorkflowRun struct {
	ID        int64     `json:"id"`
	RunID     string    `json:"run_id"`
	Workflow  string    `json:"workflow"`
	Inputs    string    `json:"inputs"`
	Status    string    `json:"status"`
	Result    string    `json:"result,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Steps is a JSON array of per-step checkpoints ([]dsl.StepEvent),
	// updated as the run progresses.
	Steps string `json:"steps,omitempty"`
}
