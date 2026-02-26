package serve

import "time"

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

// AgentResponse is the API representation of an agent definition.
type AgentResponse struct {
	Name          string   `json:"name"`
	Model         string   `json:"model,omitempty"`
	System        string   `json:"system,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	Team          []string `json:"team,omitempty"`
	ProcessID     string   `json:"process_id,omitempty"`
	ProcessStatus string   `json:"process_status,omitempty"`
	Source        string   `json:"source,omitempty"`
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
	TotalProcesses         int     `json:"total_processes"`
	RunningProcesses       int     `json:"running_processes"`
	CompletedProcesses     int     `json:"completed_processes"`
	FailedProcesses        int     `json:"failed_processes"`
	TotalInputTokens       int     `json:"total_input_tokens"`
	TotalOutputTokens      int     `json:"total_output_tokens"`
	TotalCacheCreationTokens int   `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens   int     `json:"total_cache_read_tokens"`
	TotalCostUSD           float64 `json:"total_cost_usd"`
	TotalToolCalls         int     `json:"total_tool_calls"`
	TotalErrors            int     `json:"total_errors"`
	Uptime                 string  `json:"uptime"`
}

// SpawnTreeNodeResponse is the API representation of a spawn tree node.
type SpawnTreeNodeResponse struct {
	ProcessID   string                   `json:"process_id"`
	AgentName   string                   `json:"agent_name"`
	Task        string                   `json:"task,omitempty"`
	Status      string                   `json:"status"`
	SpawnDepth  int                      `json:"spawn_depth"`
	SpawnReason string                   `json:"spawn_reason,omitempty"`
	StartedAt   time.Time                `json:"started_at"`
	Children    []SpawnTreeNodeResponse  `json:"children,omitempty"`
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
	Type      string `json:"type"`
	ProcessID string `json:"process_id,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Data      any    `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// MemoryResponse is the API representation of user memory.
type MemoryResponse struct {
	UserID  string       `json:"user_id"`
	Agent   string       `json:"agent"`
	Layers  []UserMemory `json:"layers"`
}

// ChatStatusResponse indicates whether an agent has an active stream.
type ChatStatusResponse struct {
	Streaming bool `json:"streaming"`
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
	Name        string   `json:"name"`
	Model       string   `json:"model"`
	Persona     string   `json:"persona,omitempty"`
	Skills      []string `json:"skills,omitempty"`
	Team        []string `json:"team,omitempty"`
	System      string   `json:"system,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

// CreateAgentResponse is returned when a new agent is composed.
type CreateAgentResponse struct {
	Name      string   `json:"name"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools,omitempty"`
	ProcessID string   `json:"process_id,omitempty"`
}

// UpdateAgentRequest is the request to update an existing composed agent.
type UpdateAgentRequest struct {
	Name        *string  `json:"name,omitempty"`
	Model       *string  `json:"model,omitempty"`
	System      *string  `json:"system,omitempty"`
	Team        []string `json:"team,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
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
