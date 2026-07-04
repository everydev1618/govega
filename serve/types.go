package serve

import (
	"time"

	vega "github.com/everydev1618/govega"
	"github.com/everydev1618/govega/dsl"
)

// --- API Response Types ---

// ProcessResponse is the API representation of a process.
type ProcessResponse struct {
	ID          string          `json:"id"`
	Agent       string          `json:"agent"`
	Task        string          `json:"task,omitempty"`
	Status      string          `json:"status"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	ParentID    string          `json:"parent_id,omitempty"`
	SpawnDepth  int             `json:"spawn_depth"`
	SpawnReason string          `json:"spawn_reason,omitempty"`
	Metrics     MetricsResponse `json:"metrics"`
}

// ProcessDetailResponse includes conversation history.
type ProcessDetailResponse struct {
	ProcessResponse
	Messages []MessageResponse `json:"messages"`
}

// MessageResponse is a conversation message.
type MessageResponse struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MetricsResponse is the API representation of process metrics.
type MetricsResponse struct {
	Iterations   int       `json:"iterations"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	ToolCalls    int       `json:"tool_calls"`
	Errors       int       `json:"errors"`
	LastActiveAt time.Time `json:"last_active_at,omitempty"`
}

// AgentStatus is the high-level lifecycle state of an agent surfaced to
// the API. Distinct from the underlying vega.Process state machine: this
// answers "should the user think this agent is busy / broken?" rather than
// "what state is the underlying conversation in?".
//
// govega's process model has no provisioning/paused/stopping concepts
// (those are deployment-tier states), so this enum is intentionally a
// subset of the apex-host-mgmt mental model. New states will be added
// additively as govega's lifecycle grows.
type AgentStatus string

const (
	// AgentStatusIdle: agent is defined but has no active work in flight.
	// Includes "never spawned," "pending spawn," and "completed last task."
	AgentStatusIdle AgentStatus = "idle"
	// AgentStatusRunning: process is actively working on a task right now.
	AgentStatusRunning AgentStatus = "running"
	// AgentStatusError: last terminal state was a failure or timeout.
	AgentStatusError AgentStatus = "error"
	// AgentStatusProvisioning: agent record exists but the underlying
	// environment isn't ready to accept work yet (workspace setup,
	// dependency install, MCP boot, first-run hooks). RESERVED — govega
	// currently creates agents synchronously and never emits this value.
	// Frontends can handle it today as future-stable for when async
	// creation lands; until then the POST /agents response duration
	// covers the provisioning window. (refs #53)
	AgentStatusProvisioning AgentStatus = "provisioning"
)

// AgentHealth is an orthogonal "is anything wrong?" axis. Lifecycle status
// answers "where is this in its life," health answers "is anything wrong
// with how it's running."
type AgentHealth string

const (
	AgentHealthUnknown   AgentHealth = "unknown"
	AgentHealthHealthy   AgentHealth = "healthy"
	AgentHealthDegraded  AgentHealth = "degraded"
	AgentHealthUnhealthy AgentHealth = "unhealthy"
)

// AgentStatsResponse aggregates per-agent kanban task counters. Computed
// from the tasks table at request time. SuccessRate is nil if there are
// no terminal tasks (done + canceled = 0) so the frontend can render a
// "—" rather than a misleading 0%.
type AgentStatsResponse struct {
	AssignedTasks  int      `json:"assigned_tasks"`
	CompletedTasks int      `json:"completed_tasks"`
	SuccessRate    *float64 `json:"success_rate"`
}

// AgentResponse is the API representation of an agent definition.
type AgentResponse struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Title       string `json:"title,omitempty"`
	// Description is a short paragraph of body text describing the agent's
	// purpose. User-facing — distinct from `system` (the LLM-facing prompt).
	Description string `json:"description,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	// Icon is a Lucide icon name; pairs with AvatarGradient for the
	// frontend's circular agent badge.
	Icon string `json:"icon,omitempty"`
	// AvatarGradient is a 2-stop CSS color array, e.g. ["#EF4444", "#DC2626"].
	AvatarGradient []string `json:"avatar_gradient,omitempty"`
	// IsOrchestrator is true when this agent is the tenant's configured
	// orchestrator (the "main agent" — Iris by default, renamable per
	// tenant). Lets frontends mark the orchestrator with special
	// affordances ("talk to your orchestrator" surface) without
	// hardcoding the name.
	IsOrchestrator bool `json:"is_orchestrator,omitempty"`
	// IsBuilder is true when this agent is the tenant's configured
	// builder (Hera by default; in Apex tenants this is always "apex").
	// Internal meta-agent — typically filtered out of public listings.
	IsBuilder bool             `json:"is_builder,omitempty"`
	Model     string           `json:"model,omitempty"`
	System    string           `json:"system,omitempty"`
	Tools     []string         `json:"tools,omitempty"`
	Team      []string         `json:"team,omitempty"`
	Triggers  []dsl.TriggerDef `json:"triggers,omitempty"`
	ProcessID string           `json:"process_id,omitempty"`
	// Status is the high-level agent lifecycle state. Always present.
	Status AgentStatus `json:"status"`
	// Health is the orthogonal "is anything wrong?" signal. Always present.
	Health    AgentHealth `json:"health"`
	Streaming bool        `json:"streaming,omitempty"`
	Source    string      `json:"source,omitempty"`
	// CreatedAt is when the agent was first persisted (composed agents) or
	// the server start time (YAML agents). Pointer so untracked agents omit.
	CreatedAt *time.Time `json:"created_at,omitempty"`
	// UpdatedAt is the last time the agent definition changed.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// LastActivity is the most recent moment the agent's running process
	// did work (last token, last tool call). Nil if the agent has never run.
	LastActivity *time.Time `json:"last_activity,omitempty"`
	// Stats is the per-agent kanban task summary. Always present; zero
	// values mean "no tasks" rather than "not implemented."
	Stats AgentStatsResponse `json:"stats"`
	// ReportsTo lists the agents whose `team` includes this agent — i.e.
	// the agent's supervisors. Inverse direction of `team`. Computed from
	// the document at request time.
	ReportsTo []string `json:"reports_to,omitempty"`
}

// WorkflowResponse is the API representation of a workflow definition.
type WorkflowResponse struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	Steps       int                      `json:"steps"`
	Inputs      map[string]InputResponse `json:"inputs,omitempty"`
}

// InputResponse describes a workflow input.
type InputResponse struct {
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// StatsResponse contains aggregate metrics.
type StatsResponse struct {
	TotalProcesses           int     `json:"total_processes"`
	RunningProcesses         int     `json:"running_processes"`
	CompletedProcesses       int     `json:"completed_processes"`
	FailedProcesses          int     `json:"failed_processes"`
	TotalInputTokens         int     `json:"total_input_tokens"`
	TotalOutputTokens        int     `json:"total_output_tokens"`
	TotalCacheCreationTokens int     `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens     int     `json:"total_cache_read_tokens"`
	TotalCostUSD             float64 `json:"total_cost_usd"`
	TotalToolCalls           int     `json:"total_tool_calls"`
	TotalErrors              int     `json:"total_errors"`
	Uptime                   string  `json:"uptime"`
}

// SpawnTreeNodeResponse is the API representation of a spawn tree node.
type SpawnTreeNodeResponse struct {
	ProcessID   string                  `json:"process_id"`
	AgentName   string                  `json:"agent_name"`
	Task        string                  `json:"task,omitempty"`
	Status      string                  `json:"status"`
	SpawnDepth  int                     `json:"spawn_depth"`
	SpawnReason string                  `json:"spawn_reason,omitempty"`
	StartedAt   time.Time               `json:"started_at"`
	Children    []SpawnTreeNodeResponse `json:"children,omitempty"`
}

// MCPServerResponse is the API representation of an MCP server.
type MCPServerResponse struct {
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Disabled  bool     `json:"disabled,omitempty"`
	Transport string   `json:"transport,omitempty"`
	URL       string   `json:"url,omitempty"`
	Command   string   `json:"command,omitempty"`
	Tools     []string `json:"tools"`
	// Editable is true only when the server has a stored config row (i.e. it
	// was added via the UI/API). Servers auto-connected from the built-in
	// registry via env vars have no editable config; the FE hides Edit for
	// them and shows they're configured via the environment instead.
	Editable bool `json:"editable"`
}

// WorkflowRunRequest is the request to launch a workflow.
type WorkflowRunRequest struct {
	Inputs map[string]any `json:"inputs"`
}

// WorkflowRunResponse is returned when a workflow is launched.
type WorkflowRunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// BrokerEvent is an event sent via SSE.
type BrokerEvent struct {
	Type      string    `json:"type"`
	ProcessID string    `json:"process_id,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Data      any       `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// MemoryResponse is the API representation of user memory.
type MemoryResponse struct {
	UserID string       `json:"user_id"`
	Agent  string       `json:"agent"`
	Layers []UserMemory `json:"layers"`
}

// ChatStatusResponse indicates whether an agent has an active stream.
type ChatStatusResponse struct {
	Streaming bool `json:"streaming"`
}

// CompanyResponse is the API representation of company identity.
type CompanyResponse struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	LogoURL     string                   `json:"logo_url,omitempty"`
	AccentColor string                   `json:"accent_color,omitempty"`
	Siblings    []CompanySiblingResponse `json:"siblings,omitempty"`
}

// CompanySiblingResponse is the API representation of a sibling instance.
type CompanySiblingResponse struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Icon string `json:"icon,omitempty"`
}

// AgentTemplateResponse is the API representation of a portable agent template.
type AgentTemplateResponse struct {
	Version        string   `json:"version"`
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name,omitempty"`
	Title          string   `json:"title,omitempty"`
	Description    string   `json:"description,omitempty"`
	Avatar         string   `json:"avatar,omitempty"`
	Icon           string   `json:"icon,omitempty"`
	AvatarGradient []string `json:"avatar_gradient,omitempty"`
	Model          string   `json:"model"`
	System         string   `json:"system"`
	Tools          []string `json:"tools,omitempty"`
	Team           []string `json:"team,omitempty"`
	ExportedBy     string   `json:"exported_by,omitempty"`
	ExportedAt     string   `json:"exported_at,omitempty"`
}

// --- Channel Types ---

// Channel is a Slack-style group conversation space for a team of agents.
type Channel struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Team         []string  `json:"team"`
	Mode         string    `json:"mode,omitempty"` // "" = default (team-lead responds), "social" = all members respond
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	UnreadCount  int       `json:"unread_count"`
}

// ChannelMessage is a message in a channel, optionally part of a thread.
type ChannelMessage struct {
	ID        int64     `json:"id"`
	ChannelID string    `json:"channel_id"`
	ThreadID  *int64    `json:"thread_id,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Sender    string    `json:"sender,omitempty"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Icon / AvatarGradient are echoed from the sending agent so channel
	// UIs don't need a separate /api/v1/agents lookup to render identity.
	Icon           string   `json:"icon,omitempty"`
	AvatarGradient []string `json:"avatar_gradient,omitempty"`
	// Thread summary fields — populated for top-level messages (ThreadID nil).
	ReplyCount    int        `json:"reply_count,omitempty"`
	LatestReplyAt *time.Time `json:"latest_reply_at,omitempty"`
	// ReplySenders is the distinct list of sender names across all replies
	// in the thread. Lets the channel list show "Riley and Alex replied"
	// without a follow-up fetch per thread.
	ReplySenders []string `json:"reply_senders,omitempty"`
	// ToolActivities captures completed tool calls from the assistant
	// turn that produced this message. Empty for user messages.
	ToolActivities []vega.ToolActivity `json:"tool_activities,omitempty"`
}

// CreateChannelRequest is the request to create a channel.
type CreateChannelRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Team        []string `json:"team"`
	// Mode: "" (default — team-lead responds), "social" (all members
	// respond to every message), or "reactive" (members respond when
	// work-relevant). Defaults to "" if omitted.
	Mode string `json:"mode,omitempty"`
}

// UpdateChannelRequest is the request to partially update a channel's
// display fields. Pointer fields distinguish "not set" (omit, leave
// unchanged) from "set to empty."
type UpdateChannelRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ChannelPostRequest is the request to post a message to a channel.
type ChannelPostRequest struct {
	Message  string `json:"message"`
	ThreadID *int64 `json:"thread_id,omitempty"`
	Agent    string `json:"agent,omitempty"`
}

// ChannelEvent is an SSE event for channel activity.
type ChannelEvent struct {
	Type      string `json:"type"`
	Channel   string `json:"channel"`
	MessageID int64  `json:"message_id,omitempty"`
	ThreadID  *int64 `json:"thread_id,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Delta     string `json:"delta,omitempty"`
	// Tool-call fields — parity with ChatStreamEvent. Already on the wire
	// in the channel stream; documenting them on the schema (refs #55).
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	Result     string         `json:"result,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
	Metrics    any            `json:"metrics,omitempty"`
}

// InboxItem is a message posted to Iris's inbox by an agent.
type InboxItem struct {
	ID            int64      `json:"id"`
	FromAgent     string     `json:"from_agent"`
	Subject       string     `json:"subject"`
	Body          string     `json:"body,omitempty"`
	Priority      string     `json:"priority"`
	Status        string     `json:"status"`
	Resolution    string     `json:"resolution,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	TriageCount   int        `json:"triage_count"`
	LastTriagedAt *time.Time `json:"last_triaged_at,omitempty"`
}

// ErrorResponse is returned on API errors.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// FileEntry represents a file or directory in the workspace.
type FileEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	Size        int64  `json:"size"`
	ModTime     string `json:"mod_time"`
	ContentType string `json:"content_type,omitempty"`
}

// FileContentResponse is the response for reading a file's content.
type FileContentResponse struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
	Size        int64  `json:"size"`
}

// FileMetadataResponse is the response for file metadata queries.
type FileMetadataResponse struct {
	Files  []WorkspaceFile `json:"files"`
	Agents []string        `json:"agents"`
}

// --- Population & Agent Composition Types ---

// PopulationSearchResult is the API representation of a population search result.
type PopulationSearchResult struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Score       float64  `json:"score,omitempty"`
}

// PopulationInfoResponse is the API representation of population item details.
type PopulationInfoResponse struct {
	Kind              string   `json:"kind"`
	Name              string   `json:"name"`
	Version           string   `json:"version,omitempty"`
	Description       string   `json:"description,omitempty"`
	Author            string   `json:"author,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Persona           string   `json:"persona,omitempty"`
	Skills            []string `json:"skills,omitempty"`
	RecommendedSkills []string `json:"recommended_skills,omitempty"`
	SystemPrompt      string   `json:"system_prompt,omitempty"`
	Installed         bool     `json:"installed"`
	InstalledPath     string   `json:"installed_path,omitempty"`
}

// PopulationInstalledItem is the API representation of an installed population item.
type PopulationInstalledItem struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
}

// PopulationInstallRequest is the request to install a population item.
type PopulationInstallRequest struct {
	Name string `json:"name"`
}

// CreateAgentRequest is the request to compose a new agent.
type CreateAgentRequest struct {
	Name           string           `json:"name"`
	DisplayName    string           `json:"display_name,omitempty"`
	Title          string           `json:"title,omitempty"`
	Description    string           `json:"description,omitempty"`
	Avatar         string           `json:"avatar,omitempty"`
	Icon           string           `json:"icon,omitempty"`
	AvatarGradient []string         `json:"avatar_gradient,omitempty"`
	Model          string           `json:"model"`
	Persona        string           `json:"persona,omitempty"`
	Skills         []string         `json:"skills,omitempty"`
	Team           []string         `json:"team,omitempty"`
	System         string           `json:"system,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	Triggers       []dsl.TriggerDef `json:"triggers,omitempty"`
}

// CreateAgentResponse is returned when a new agent is composed.
type CreateAgentResponse struct {
	Name      string   `json:"name"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools,omitempty"`
	ProcessID string   `json:"process_id,omitempty"`
}

// --- Agent Routines (per-agent Schedules) — refs govega#52 ---

// AgentRoutineResponse is the wire shape returned by the per-agent
// schedules endpoints. Mirrors apex-host-mgmt's AgentRoutine type so the
// FE can render the Routines tab without any client-side conversion.
type AgentRoutineResponse struct {
	ID           string   `json:"id"`
	Agent        string   `json:"agent"`
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	Schedule     Schedule `json:"schedule"`
	// Cron is the derived expression — exposed for FE debugging and for
	// jobs created via DSL/legacy paths where the structured Schedule
	// can't be reconstructed.
	Cron      string     `json:"cron"`
	Enabled   bool       `json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at"`
	NextRunAt *time.Time `json:"next_run_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CreateRoutineRequest is the POST body for creating a routine on an agent.
type CreateRoutineRequest struct {
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	Schedule     Schedule `json:"schedule"`
	// Enabled defaults to true when omitted.
	Enabled *bool `json:"enabled,omitempty"`
}

// UpdateRoutineRequest is the PATCH body. Pointer-typed fields distinguish
// "omitted" (leave unchanged) from "set to empty".
type UpdateRoutineRequest struct {
	Title        *string   `json:"title,omitempty"`
	Instructions *string   `json:"instructions,omitempty"`
	Schedule     *Schedule `json:"schedule,omitempty"`
	Enabled      *bool     `json:"enabled,omitempty"`
}

// ActivityLogResponse is the wire shape for /api/v1/activity, the
// searchable activity log endpoint (refs govega#33). Carries the page
// of events plus a total match count so the FE can render pagination
// without a follow-up count query.
type ActivityLogResponse struct {
	Events []StoreEvent `json:"events"`
	Total  int          `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

// AgentBudgetResponse is the wire shape for the per-agent budget
// endpoints (refs govega#47). Composes the persisted cap + threshold
// + enabled state with the current-period observed_spend rollup so
// the FE renders the budget tab in one round-trip.
//
// budget_cap is nullable: null means "no cap configured." A null cap
// or enabled=false both disable the hard cutoff; soft_alert_threshold
// still drives the warning band when a cap is set.
type AgentBudgetResponse struct {
	Agent              string    `json:"agent"`
	BudgetCap          *float64  `json:"budget_cap"`
	SoftAlertThreshold float64   `json:"soft_alert_threshold"`
	Enabled            bool      `json:"enabled"`
	ObservedSpend      float64   `json:"observed_spend"`
	PeriodStart        time.Time `json:"period_start"`
	PeriodEnd          time.Time `json:"period_end"`
}

// UpdateAgentBudgetRequest is the partial-PUT body. Pointer fields
// distinguish "omitted" (leave unchanged) from "set to empty". Pass a
// non-nil pointer to a nil BudgetCap to clear the cap; pass nil to
// leave it. Threshold defaults to 0.8 on first write.
type UpdateAgentBudgetRequest struct {
	BudgetCap          *float64 `json:"budget_cap,omitempty"`
	SoftAlertThreshold *float64 `json:"soft_alert_threshold,omitempty"`
	Enabled            *bool    `json:"enabled,omitempty"`
}

// AgentSpendResponse is the wire shape for the per-agent spend rollup
// endpoint (refs govega#47). Aggregates cost_usd across the latest
// snapshot of every process for the agent within the queried period.
type AgentSpendResponse struct {
	Agent         string    `json:"agent"`
	ObservedSpend float64   `json:"observed_spend"`
	Period        string    `json:"period"`
	PeriodStart   time.Time `json:"period_start"`
	PeriodEnd     time.Time `json:"period_end"`
}

// ToggleAgentToolRequest is the PATCH body for the per-agent tool
// enable/disable surface (refs govega#44). Single-tool granularity so
// toggling two tools in quick succession can't race against a
// read-modify-write of the full tools array.
type ToggleAgentToolRequest struct {
	Enabled bool `json:"enabled"`
}

// AgentToolsResponse is the response from a per-agent tool toggle. It
// returns the agent's full current tool list so the FE can confirm the
// new state without a follow-up GET.
type AgentToolsResponse struct {
	Name  string   `json:"name"`
	Tools []string `json:"tools"`
}

// UpdateAgentRequest is the request to update an existing composed agent.
// Pointer-typed fields distinguish "not set" (omit, leave unchanged) from
// "set to empty" (clear). Slice fields use len() == 0 to mean unchanged.
type UpdateAgentRequest struct {
	Name           *string  `json:"name,omitempty"`
	DisplayName    *string  `json:"display_name,omitempty"`
	Title          *string  `json:"title,omitempty"`
	Description    *string  `json:"description,omitempty"`
	Avatar         *string  `json:"avatar,omitempty"`
	Icon           *string  `json:"icon,omitempty"`
	AvatarGradient []string `json:"avatar_gradient,omitempty"`
	Model          *string  `json:"model,omitempty"`
	System         *string  `json:"system,omitempty"`
	Team           []string `json:"team,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	// Triggers replaces the agent's reactive triggers when non-nil. An empty
	// (non-nil) slice clears them; nil leaves them unchanged.
	Triggers []dsl.TriggerDef `json:"triggers,omitempty"`
}

// --- MCP Connection Types ---

// MCPRegistryEntryResponse describes a registry entry for the connections page.
type MCPRegistryEntryResponse struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	RequiredEnv      []string          `json:"required_env,omitempty"`
	OptionalEnv      []string          `json:"optional_env,omitempty"`
	BuiltinGo        bool              `json:"builtin_go,omitempty"`
	Connected        bool              `json:"connected"`
	ExistingSettings map[string]string `json:"existing_settings,omitempty"`
	// Icon is a Lucide icon name for the FE's integrations grid
	// (refs govega#44). Empty for entries that haven't been tagged.
	Icon string `json:"icon,omitempty"`
	// Category groups integrations on the FE. Suggested taxonomy:
	// communication, dev_tools, crm, project_mgmt, cloud, data,
	// productivity, web. Empty for uncategorized.
	Category string `json:"category,omitempty"`
}

// ConnectMCPRequest is the request to connect an MCP server.
type ConnectMCPRequest struct {
	Name      string            `json:"name"`
	Env       map[string]string `json:"env,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Timeout   int               `json:"timeout,omitempty"`
}

// MCPServerConfig is a persisted MCP server connection for auto-reconnect.
type MCPServerConfig struct {
	Name       string `json:"name"`
	ConfigJSON string `json:"config"`   // JSON-serialized ConnectMCPRequest
	Disabled   bool   `json:"disabled"` // true = persisted but not connected
}

// MCPServerConfigResponse returns the persisted config for an MCP server,
// suitable for pre-filling an edit form.
type MCPServerConfigResponse struct {
	Name             string            `json:"name"`
	Transport        string            `json:"transport,omitempty"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	URL              string            `json:"url,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Timeout          int               `json:"timeout,omitempty"`
	EnvKeys          []string          `json:"env_keys,omitempty"`
	ExistingSettings map[string]string `json:"existing_settings,omitempty"`
	IsRegistry       bool              `json:"is_registry"`
}

// ConnectMCPResponse is returned when an MCP server is connected.
type ConnectMCPResponse struct {
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Tools     []string `json:"tools,omitempty"`
	Error     string   `json:"error,omitempty"`
}
