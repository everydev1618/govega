// Backend routes are versioned under /api/v1 (see commit 33031a9). The
// frontend uses BASE so callers can write path-only strings.
const BASE = '/api/v1'

export class APIError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
  }
}

async function parseErrorResponse(res: Response): Promise<APIError> {
  const body = await res.text()
  try {
    const json = JSON.parse(body)
    if (json.error) return new APIError(res.status, json.error)
  } catch { /* not JSON, fall through */ }
  return new APIError(res.status, body)
}

export async function fetchAPI<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  if (!res.ok) {
    throw await parseErrorResponse(res)
  }
  return res.json()
}

// ReactiveEvent is one row from the reactive activity log: a router decision
// (reactive.fired / reactive.gated:*) or a spine event agents react to.
export interface ReactiveEvent {
  id: number
  type: string
  agent_name?: string
  timestamp: string
  data?: string
  result?: string
  error?: string
}

export const api = {
  // Reactive activity — the trigger router's decisions + the spine events.
  getReactiveActivity: (limit = 200) =>
    fetchAPI<{ events: ReactiveEvent[]; count: number }>(`/reactive/activity?limit=${limit}`),

  // Company
  getCompany: () => fetchAPI<import('./types').CompanyResponse>('/company'),
  getTenantConfig: () => fetchAPI<import('./types').TenantConfigResponse>('/tenant/config'),

  // Agent templates
  exportAgentTemplate: (name: string) =>
    fetchAPI<import('./types').AgentTemplateResponse>(`/agents/${encodeURIComponent(name)}/template`),
  importAgentTemplate: (template: import('./types').AgentTemplateResponse) =>
    fetchAPI<import('./types').CreateAgentResponse>('/agents/import', {
      method: 'POST',
      body: JSON.stringify(template),
    }),

  getProcesses: () => fetchAPI<import('./types').ProcessResponse[]>('/processes'),
  getProcess: (id: string) => fetchAPI<import('./types').ProcessDetailResponse>(`/processes/${id}`),
  killProcess: (id: string) => fetchAPI<{ status: string }>(`/processes/${id}`, { method: 'DELETE' }),

  // Tasks
  listTasks: (params?: { status?: string; assignee?: string }) => {
    const q = new URLSearchParams()
    if (params?.status) q.set('status', params.status)
    if (params?.assignee) q.set('assignee', params.assignee)
    const qs = q.toString()
    return fetchAPI<import('./types').Task[]>(`/tasks${qs ? `?${qs}` : ''}`)
  },
  createTask: (req: import('./types').CreateTaskRequest) =>
    fetchAPI<import('./types').Task>('/tasks', { method: 'POST', body: JSON.stringify(req) }),
  getTask: (id: string) => fetchAPI<import('./types').TaskDetail>(`/tasks/${id}`),
  updateTask: (id: string, req: import('./types').UpdateTaskRequest) =>
    fetchAPI<import('./types').Task>(`/tasks/${id}`, { method: 'PATCH', body: JSON.stringify(req) }),
  deleteTask: (id: string) =>
    fetchAPI<{ status: string }>(`/tasks/${id}`, { method: 'DELETE' }),
  addTaskComment: (id: string, content: string, author?: string) =>
    fetchAPI<import('./types').TaskComment>(`/tasks/${id}/comments`, {
      method: 'POST',
      body: JSON.stringify({ content, author }),
    }),
  linkTaskProcess: (id: string, processId: string) =>
    fetchAPI<{ status: string }>(`/tasks/${id}/processes`, {
      method: 'POST',
      body: JSON.stringify({ process_id: processId }),
    }),

  getAgents: () => fetchAPI<import('./types').AgentResponse[]>('/agents'),
  getWorkflows: () => fetchAPI<import('./types').WorkflowResponse[]>('/workflows'),
  runWorkflow: (name: string, inputs: Record<string, unknown>) =>
    fetchAPI<import('./types').WorkflowRunResponse>(`/workflows/${name}/run`, {
      method: 'POST',
      body: JSON.stringify({ inputs }),
    }),
  getMCPServers: () => fetchAPI<import('./types').MCPServerResponse[]>('/mcp/servers'),
  getMCPRegistry: () => fetchAPI<import('./types').MCPRegistryEntry[]>('/mcp/registry'),
  connectMCPServer: (req: import('./types').ConnectMCPRequest) =>
    fetchAPI<import('./types').ConnectMCPResponse>('/mcp/servers', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  disconnectMCPServer: (name: string) =>
    fetchAPI<{ status: string }>(`/mcp/servers/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  refreshMCPServer: (name: string) =>
    fetchAPI<import('./types').ConnectMCPResponse>(`/mcp/servers/${encodeURIComponent(name)}/refresh`, {
      method: 'POST',
    }),
  getMCPServerConfig: (name: string) =>
    fetchAPI<import('./types').MCPServerConfigResponse>(`/mcp/servers/${encodeURIComponent(name)}/config`),
  updateMCPServer: (name: string, req: import('./types').ConnectMCPRequest) =>
    fetchAPI<import('./types').ConnectMCPResponse>(`/mcp/servers/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(req),
    }),
  duplicateMCPServer: (name: string, newName: string) =>
    fetchAPI<import('./types').ConnectMCPResponse>(`/mcp/servers/${encodeURIComponent(name)}/duplicate`, {
      method: 'POST',
      body: JSON.stringify({ new_name: newName }),
    }),
  toggleMCPServer: (name: string, disabled: boolean) =>
    fetchAPI<import('./types').ConnectMCPResponse | { status: string }>(`/mcp/servers/${encodeURIComponent(name)}/disable`, {
      method: 'PUT',
      body: JSON.stringify({ disabled }),
    }),
  getStats: () => fetchAPI<import('./types').StatsResponse>('/stats'),
  getSpawnTree: () => fetchAPI<import('./types').SpawnTreeNode[]>('/spawn-tree'),

  // Population
  populationSearch: (q: string, kind?: string) => {
    const params = new URLSearchParams({ q })
    if (kind) params.set('kind', kind)
    return fetchAPI<import('./types').PopulationSearchResult[]>(`/population/search?${params}`)
  },
  populationInfo: (kind: string, name: string) =>
    fetchAPI<import('./types').PopulationInfoResponse>(`/population/info/${kind}/${name}`),
  populationInstall: (name: string) =>
    fetchAPI<{ status: string; name: string }>('/population/install', {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),
  populationInstalled: (kind?: string) => {
    const params = kind ? `?kind=${kind}` : ''
    return fetchAPI<import('./types').PopulationInstalledItem[]>(`/population/installed${params}`)
  },

  // Files
  getFiles: (path?: string) => {
    const params = path ? `?path=${encodeURIComponent(path)}` : ''
    return fetchAPI<import('./types').FileEntry[]>(`/files${params}`)
  },
  getFileContent: (path: string) =>
    fetchAPI<import('./types').FileContentResponse>(`/files/read?path=${encodeURIComponent(path)}`),
  deleteFile: (path: string) =>
    fetchAPI<{ status: string; path: string }>(`/files?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),
  getFileMetadata: (agent?: string) => {
    const params = agent ? `?agent=${encodeURIComponent(agent)}` : ''
    return fetchAPI<import('./types').FileMetadataResponse>(`/files/metadata${params}`)
  },

  // Settings
  getSettings: () => fetchAPI<import('./types').Setting[]>('/settings'),
  upsertSetting: (key: string, value: string, sensitive: boolean) =>
    fetchAPI<{ status: string }>('/settings', {
      method: 'PUT',
      body: JSON.stringify({ key, value, sensitive }),
    }),
  deleteSetting: (key: string) =>
    fetchAPI<{ status: string }>(`/settings/${key}`, { method: 'DELETE' }),

  // Wiki memory (govega#71). Default scope is the shared user wiki to
  // preserve the existing call site. scope=all unions user + every agent
  // wiki; scope=agent requires the agent name.
  getMemoryGraph: (params?: import('./types').MemoryGraphParams) => {
    const q = new URLSearchParams()
    if (params?.scope) q.set('scope', params.scope)
    if (params?.agent) q.set('agent', params.agent)
    const qs = q.toString()
    return fetchAPI<import('./types').MemoryGraph>(`/memory/graph${qs ? `?${qs}` : ''}`)
  },
  listMemoryPages: () =>
    fetchAPI<import('./types').MemoryPageMetadata[] | null>('/memory/pages')
      .then((res) => res ?? []),
  getMemoryPage: async (path: string) => {
    try {
      return await fetchAPI<import('./types').MemoryPage>(
        `/memory/page?path=${encodeURIComponent(path)}`,
      )
    } catch (err) {
      if (err instanceof APIError && err.status === 404) return null
      throw err
    }
  },

  // Schedules
  getSchedules: () => fetchAPI<import('./types').ScheduledJob[]>('/schedules'),
  deleteSchedule: (name: string) =>
    fetchAPI<{ status: string }>(`/schedules/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  toggleSchedule: (name: string, enabled: boolean) =>
    fetchAPI<{ status: string }>(`/schedules/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify({ enabled }),
    }),

  // Agent composition
  createAgent: (req: import('./types').CreateAgentRequest) =>
    fetchAPI<import('./types').CreateAgentResponse>('/agents', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  updateAgent: (name: string, req: import('./types').UpdateAgentRequest) =>
    fetchAPI<{ status: string }>(`/agents/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(req),
    }),
  deleteAgent: (name: string) =>
    fetchAPI<{ status: string }>(`/agents/${name}`, { method: 'DELETE' }),

  // Chat
  chatHistory: (agent: string) =>
    fetchAPI<{ role: string; content: string }[]>(`/agents/${agent}/chat`),
  chat: (agent: string, message: string) =>
    fetchAPI<{ response: string }>(`/agents/${agent}/chat`, {
      method: 'POST',
      body: JSON.stringify({ message }),
    }),
  resetChat: (agent: string) =>
    fetchAPI<{ status: string }>(`/agents/${agent}/chat`, { method: 'DELETE' }),

  // Chat status — check if agent has an active stream
  chatStatus: (agent: string) =>
    fetchAPI<{ streaming: boolean }>(`/agents/${agent}/chat/status`),

  // Reconnect to an active stream — replays buffered events then continues live
  chatStreamReconnect: (
    agent: string,
    onEvent: (event: import('./types').ChatEvent) => void,
    signal?: AbortSignal,
  ): Promise<void> => {
    return fetch(`${BASE}/agents/${agent}/chat/stream`, {
      method: 'GET',
      headers: {},
      signal,
    }).then(async (res) => {
      if (!res.ok) return // server error
      // 0.2.0: reconnect is always SSE. When there's nothing to resume,
      // the server emits a `no_active_stream` event followed by `done`
      // and closes. Caller's onEvent handler can switch on event.type.

      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })

        const lines = buffer.split('\n')
        buffer = lines.pop()!

        let currentData: string | null = null
        for (const line of lines) {
          if (line.startsWith('data: ')) {
            currentData = line.slice(6)
          } else if (line === '' && currentData !== null) {
            try {
              onEvent(JSON.parse(currentData))
            } catch { /* skip malformed */ }
            currentData = null
          }
        }
      }
    }).catch((err) => {
      if (err.name === 'AbortError') return
      // Silently ignore reconnection failures
    })
  },

  // Channels
  getChannels: () => fetchAPI<import('./types').Channel[]>('/channels'),
  getChannel: (name: string) =>
    fetchAPI<import('./types').Channel>(`/channels/${encodeURIComponent(name)}`),
  createChannel: (req: import('./types').CreateChannelRequest) =>
    fetchAPI<import('./types').Channel>('/channels', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  deleteChannel: (name: string) =>
    fetchAPI<{ status: string }>(`/channels/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  getChannelMessages: (name: string, limit?: number) => {
    const params = limit ? `?limit=${limit}` : ''
    return fetchAPI<import('./types').ChannelMessage[]>(`/channels/${encodeURIComponent(name)}/messages${params}`)
  },
  getThreadMessages: (name: string, messageId: number) =>
    fetchAPI<import('./types').ChannelMessage[]>(`/channels/${encodeURIComponent(name)}/messages/${messageId}/thread`),
  postChannelMessage: (name: string, message: string, threadId?: number, agent?: string) =>
    fetchAPI<{ message_id: number; thread_id?: number }>(`/channels/${encodeURIComponent(name)}/messages`, {
      method: 'POST',
      body: JSON.stringify({ message, thread_id: threadId, agent }),
    }),
  channelStream: (
    name: string,
    message: string,
    onEvent: (event: import('./types').ChannelEvent) => void,
    signal?: AbortSignal,
    threadId?: number,
    agent?: string,
  ): Promise<void> => {
    return fetch(`${BASE}/api/channels/${encodeURIComponent(name)}/stream`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, thread_id: threadId, agent }),
      signal,
    }).then(async (res) => {
      if (!res.ok) {
        throw await parseErrorResponse(res)
      }
      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })

        const lines = buffer.split('\n')
        buffer = lines.pop()!

        let currentData: string | null = null
        for (const line of lines) {
          if (line.startsWith('data: ')) {
            currentData = line.slice(6)
          } else if (line === '' && currentData !== null) {
            try {
              onEvent(JSON.parse(currentData))
            } catch { /* skip malformed */ }
            currentData = null
          }
        }
      }
    }).catch((err) => {
      if (err.name === 'AbortError') return
      throw err
    })
  },

  // Read tracking
  markChannelRead: (name: string) =>
    fetchAPI<{ status: string }>(`/channels/${encodeURIComponent(name)}/read`, { method: 'POST' }),
  markChatRead: (agent: string) =>
    fetchAPI<{ status: string }>(`/agents/${encodeURIComponent(agent)}/chat/read`, { method: 'POST' }),
  chatUnreadCounts: () =>
    fetchAPI<Record<string, number>>('/chat/unread'),

  // Inbox
  getInbox: (status?: string) => {
    const params = status ? `?status=${encodeURIComponent(status)}` : ''
    return fetchAPI<import('./types').InboxItem[]>(`/inbox${params}`)
  },
  clearResolvedInbox: () =>
    fetchAPI<{ deleted: number }>('/inbox/resolved', { method: 'DELETE' }),

  // Nuclear reset — wipes all data and restores to YAML-defined state
  resetProject: () =>
    fetchAPI<{ status: string }>('/reset', { method: 'POST' }),

  // Streaming chat
  chatStream: (
    agent: string,
    message: string,
    onEvent: (event: import('./types').ChatEvent) => void,
    signal?: AbortSignal,
  ): Promise<void> => {
    return fetch(`${BASE}/agents/${agent}/chat/stream`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message }),
      signal,
    }).then(async (res) => {
      if (!res.ok) {
        throw await parseErrorResponse(res)
      }
      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })

        // Parse SSE frames from buffer.
        const lines = buffer.split('\n')
        buffer = lines.pop()! // keep incomplete last line

        let currentData: string | null = null
        for (const line of lines) {
          if (line.startsWith('data: ')) {
            currentData = line.slice(6)
          } else if (line === '' && currentData !== null) {
            try {
              onEvent(JSON.parse(currentData))
            } catch { /* skip malformed */ }
            currentData = null
          }
        }
      }
    }).catch((err) => {
      if (err.name === 'AbortError') return
      throw err
    })
  },

  // Peering (federation over AIRE). Endpoints respond 404 with
  // {"error": "peering not enabled"} when VEGA_PEERING_ADDR is unset; the
  // React modal treats that as "feature off" and stays hidden.
  getPeeringStatus: () =>
    fetchAPI<import('./types').PeeringStatus>('/peering/status'),
  listPeers: () =>
    fetchAPI<import('./types').PeerDTO[]>('/peering/peers'),
  addPeer: (body: import('./types').AddPeerRequest) =>
    fetchAPI<import('./types').PeerDTO>('/peering/peers', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updatePeer: (nodeID: string, body: { trust_level: string }) =>
    fetchAPI<import('./types').PeerDTO>(`/peering/peers/${encodeURIComponent(nodeID)}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),
  deletePeer: (nodeID: string) =>
    fetchAPI<{ deleted: string; grants_removed: number }>(
      `/peering/peers/${encodeURIComponent(nodeID)}`,
      { method: 'DELETE' },
    ),
  listGrants: (peer?: string) => {
    const q = peer ? `?peer=${encodeURIComponent(peer)}` : ''
    return fetchAPI<import('./types').GrantDTO[]>(`/peering/grants${q}`)
  },
  upsertGrant: (peerID: string, agent: string, body: { max_tokens_per_op: number; max_ops_per_hour: number; active: boolean }) =>
    fetchAPI<import('./types').GrantDTO>(
      `/peering/grants/${encodeURIComponent(peerID)}/${encodeURIComponent(agent)}`,
      { method: 'PUT', body: JSON.stringify(body) },
    ),
  deleteGrant: (peerID: string, agent: string) =>
    fetchAPI(`/peering/grants/${encodeURIComponent(peerID)}/${encodeURIComponent(agent)}`, {
      method: 'DELETE',
    }),
  listAudit: (params?: { peer?: string; agent?: string; direction?: string; limit?: number }) => {
    const q = new URLSearchParams()
    if (params?.peer) q.set('peer', params.peer)
    if (params?.agent) q.set('agent', params.agent)
    if (params?.direction) q.set('direction', params.direction)
    if (params?.limit) q.set('limit', String(params.limit))
    const qs = q.toString() ? `?${q.toString()}` : ''
    return fetchAPI<import('./types').AuditDTO[]>(`/peering/audit${qs}`)
  },
  listLiveOps: () =>
    fetchAPI<import('./types').AuditDTO[]>('/peering/live'),
  createInvite: () =>
    fetchAPI<import('./types').InviteDTO>('/peering/invites', { method: 'POST' }),
  returnInvite: (received: import('./types').InviteDTO) =>
    fetchAPI<import('./types').InviteDTO>('/peering/invites/return', {
      method: 'POST',
      body: JSON.stringify(received),
    }),
}
