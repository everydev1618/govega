package serve

import (
	"fmt"
	"time"

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

	// ListEvents returns recent events, newest first.
	ListEvents(limit int) ([]StoreEvent, error)

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

	// InsertChatMessage persists a chat message.
	InsertChatMessage(agent, role, content string) error

	// ListChatMessages returns chat history for an agent.
	ListChatMessages(agent string) ([]ChatMessage, error)

	// DeleteChatMessages removes all chat messages for an agent.
	DeleteChatMessages(agent string) error

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

	// ListMemoryItemsByTopic returns memory items for a given user+agent+topic.
	ListMemoryItemsByTopic(userID, agent, topic string) ([]MemoryItem, error)

	// UpsertScheduledJob creates or replaces a scheduled job.
	UpsertScheduledJob(job ScheduledJob) error

	// DeleteScheduledJob removes a scheduled job by name.
	DeleteScheduledJob(name string) error

	// ListScheduledJobs returns all scheduled jobs.
	ListScheduledJobs() ([]ScheduledJob, error)

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

	// FindChannelForAgents returns the channel where both agents are team members.
	FindChannelForAgents(agent1, agent2 string) (channelID string, channelName string, err error)

	// InsertInboxItem creates a new inbox item.
	InsertInboxItem(fromAgent, subject, body, priority string) (int64, error)

	// ListInboxItems returns inbox items filtered by status.
	ListInboxItems(status string, limit int) ([]InboxItem, error)

	// GetInboxItem returns a single inbox item by ID.
	GetInboxItem(id int64) (*InboxItem, error)

	// ResolveInboxItem marks an inbox item as resolved.
	ResolveInboxItem(id int64, resolution string) error

	// DeleteResolvedInboxItems removes all resolved inbox items and their replies.
	DeleteResolvedInboxItems() (int64, error)

	// InsertChannelMessage inserts a message into a channel.
	InsertChannelMessage(channelID, agent, role, content string, threadID *int64, metadata, sender string) (int64, error)

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
}

// StoreEvent is a persisted orchestration event.
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
	System         string    `json:"system,omitempty"`
	Temperature    *float64  `json:"temperature,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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

// ScheduledJob is a persisted recurring agent trigger.
type ScheduledJob struct {
	Name      string    `json:"name"`
	Cron      string    `json:"cron"`
	AgentName string    `json:"agent"`
	Message   string    `json:"message"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
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
}
