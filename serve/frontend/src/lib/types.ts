export interface ProcessResponse {
  id: string
  agent: string
  task?: string
  status: string
  started_at: string
  completed_at?: string
  parent_id?: string
  spawn_depth: number
  spawn_reason?: string
  metrics: MetricsResponse
}

export interface ProcessDetailResponse extends ProcessResponse {
  messages: MessageResponse[]
}

export interface MessageResponse {
  role: string
  content: string
}

export interface MetricsResponse {
  iterations: number
  input_tokens: number
  output_tokens: number
  cost_usd: number
  tool_calls: number
  errors: number
  last_active_at?: string
}

export interface AgentResponse {
  name: string
  model?: string
  system?: string
  tools?: string[]
  team?: string[]
  process_id?: string
  process_status?: string
  source?: string
}

export interface WorkflowResponse {
  name: string
  description?: string
  steps: number
  inputs?: Record<string, InputResponse>
}

export interface InputResponse {
  type?: string
  description?: string
  required: boolean
  default?: unknown
  enum?: string[]
}

export interface StatsResponse {
  total_processes: number
  running_processes: number
  completed_processes: number
  failed_processes: number
  total_input_tokens: number
  total_output_tokens: number
  total_cost_usd: number
  total_tool_calls: number
  total_errors: number
  uptime: string
}

export interface SpawnTreeNode {
  process_id: string
  agent_name: string
  task?: string
  status: string
  spawn_depth: number
  spawn_reason?: string
  started_at: string
  children?: SpawnTreeNode[]
}

export interface MCPServerResponse {
  name: string
  connected: boolean
  transport?: string
  url?: string
  command?: string
  tools: string[]
}

export interface BrokerEvent {
  type: string
  process_id?: string
  agent?: string
  data?: unknown
  timestamp: string
}

export interface WorkflowRunResponse {
  run_id: string
  status: string
}

// --- Population Types ---

export interface PopulationSearchResult {
  kind: string
  name: string
  version?: string
  description?: string
  tags?: string[]
  score?: number
}

export interface PopulationInfoResponse {
  kind: string
  name: string
  version?: string
  description?: string
  author?: string
  tags?: string[]
  persona?: string
  skills?: string[]
  recommended_skills?: string[]
  system_prompt?: string
  installed: boolean
  installed_path?: string
}

export interface PopulationInstalledItem {
  kind: string
  name: string
  version?: string
  path?: string
}

export interface CreateAgentRequest {
  name: string
  model: string
  persona?: string
  skills?: string[]
  team?: string[]
  system?: string
  temperature?: number
}

export interface UpdateAgentRequest {
  name?: string
  model?: string
  system?: string
  team?: string[]
  temperature?: number
}

export interface CreateAgentResponse {
  name: string
  model: string
  tools?: string[]
  process_id?: string
}

// --- File Browser Types ---

export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
  content_type?: string
}

export interface FileContentResponse {
  path: string
  content_type: string
  content: string
  encoding: string
  size: number
}

// --- File Metadata Types ---

export interface WorkspaceFileMetadata {
  id: number
  path: string
  agent: string
  process_id: string
  operation: string
  description?: string
  created_at: string
}

export interface FileMetadataResponse {
  files: WorkspaceFileMetadata[]
  agents: string[]
}

// --- Settings Types ---

export interface Setting {
  key: string
  value: string
  sensitive: boolean
  created_at: string
  updated_at: string
}

// --- MCP Connection Types ---

export interface MCPRegistryEntry {
  name: string
  description: string
  required_env?: string[]
  optional_env?: string[]
  builtin_go?: boolean
  connected: boolean
  existing_settings?: Record<string, string>
}

export interface ConnectMCPRequest {
  name: string
  env?: Record<string, string>
  transport?: string
  command?: string
  args?: string[]
  url?: string
  headers?: Record<string, string>
  timeout?: number
}

export interface ConnectMCPResponse {
  name: string
  connected: boolean
  tools?: string[]
  error?: string
}

// --- Streaming Chat Types ---

export interface ChatEventMetrics {
  input_tokens: number
  output_tokens: number
  cost_usd: number
  duration_ms: number
}

export interface ChatEvent {
  type: 'text_delta' | 'tool_start' | 'tool_end' | 'error' | 'done'
  delta?: string
  tool_call_id?: string
  tool_name?: string
  arguments?: Record<string, unknown>
  result?: string
  duration_ms?: number
  error?: string
  nested_agent?: string
  metrics?: ChatEventMetrics
}

export interface ToolCallState {
  id: string
  name: string
  arguments: Record<string, unknown>
  result?: string
  duration_ms?: number
  status: 'running' | 'completed' | 'error'
  collapsed: boolean
  nested_agent?: string
}
