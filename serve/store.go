package serve

import "time"

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

	// InsertMemoryItem saves a memory item.
	InsertMemoryItem(item MemoryItem) (int64, error)

	// SearchMemoryItems searches memory items by keyword across topic, content, and tags.
	SearchMemoryItems(userID, agent, query string, limit int) ([]MemoryItem, error)

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
	Role    string `json:"role"`
	Content string `json:"content"`
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
	Name        string   `json:"name"`
	Model       string   `json:"model"`
	Persona     string   `json:"persona,omitempty"`
	Skills      []string `json:"skills,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Team        []string `json:"team,omitempty"`
	System      string   `json:"system,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// MemoryItem is a persisted memory entry for project-aware recall.
type MemoryItem struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	Agent     string    `json:"agent"`
	Topic     string    `json:"topic"`
	Content   string    `json:"content"`
	Tags      string    `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
