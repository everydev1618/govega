const BASE = ''

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

export const api = {
  getProcesses: () => fetchAPI<import('./types').ProcessResponse[]>('/api/processes'),
  getProcess: (id: string) => fetchAPI<import('./types').ProcessDetailResponse>(`/api/processes/${id}`),
  killProcess: (id: string) => fetchAPI<{ status: string }>(`/api/processes/${id}`, { method: 'DELETE' }),
  getAgents: () => fetchAPI<import('./types').AgentResponse[]>('/api/agents'),
  getWorkflows: () => fetchAPI<import('./types').WorkflowResponse[]>('/api/workflows'),
  runWorkflow: (name: string, inputs: Record<string, unknown>) =>
    fetchAPI<import('./types').WorkflowRunResponse>(`/api/workflows/${name}/run`, {
      method: 'POST',
      body: JSON.stringify({ inputs }),
    }),
  getMCPServers: () => fetchAPI<import('./types').MCPServerResponse[]>('/api/mcp/servers'),
  getStats: () => fetchAPI<import('./types').StatsResponse>('/api/stats'),
  getSpawnTree: () => fetchAPI<import('./types').SpawnTreeNode[]>('/api/spawn-tree'),

  // Population
  populationSearch: (q: string, kind?: string) => {
    const params = new URLSearchParams({ q })
    if (kind) params.set('kind', kind)
    return fetchAPI<import('./types').PopulationSearchResult[]>(`/api/population/search?${params}`)
  },
  populationInfo: (kind: string, name: string) =>
    fetchAPI<import('./types').PopulationInfoResponse>(`/api/population/info/${kind}/${name}`),
  populationInstall: (name: string) =>
    fetchAPI<{ status: string; name: string }>('/api/population/install', {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),
  populationInstalled: (kind?: string) => {
    const params = kind ? `?kind=${kind}` : ''
    return fetchAPI<import('./types').PopulationInstalledItem[]>(`/api/population/installed${params}`)
  },

  // Files
  getFiles: (path?: string) => {
    const params = path ? `?path=${encodeURIComponent(path)}` : ''
    return fetchAPI<import('./types').FileEntry[]>(`/api/files${params}`)
  },
  getFileContent: (path: string) =>
    fetchAPI<import('./types').FileContentResponse>(`/api/files/read?path=${encodeURIComponent(path)}`),
  deleteFile: (path: string) =>
    fetchAPI<{ status: string; path: string }>(`/api/files?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),
  getFileMetadata: (agent?: string) => {
    const params = agent ? `?agent=${encodeURIComponent(agent)}` : ''
    return fetchAPI<import('./types').FileMetadataResponse>(`/api/files/metadata${params}`)
  },

  // Settings
  getSettings: () => fetchAPI<import('./types').Setting[]>('/api/settings'),
  upsertSetting: (key: string, value: string, sensitive: boolean) =>
    fetchAPI<{ status: string }>('/api/settings', {
      method: 'PUT',
      body: JSON.stringify({ key, value, sensitive }),
    }),
  deleteSetting: (key: string) =>
    fetchAPI<{ status: string }>(`/api/settings/${key}`, { method: 'DELETE' }),

  // Agent composition
  createAgent: (req: import('./types').CreateAgentRequest) =>
    fetchAPI<import('./types').CreateAgentResponse>('/api/agents', {
      method: 'POST',
      body: JSON.stringify(req),
    }),
  deleteAgent: (name: string) =>
    fetchAPI<{ status: string }>(`/api/agents/${name}`, { method: 'DELETE' }),

  // Chat
  chatHistory: (agent: string) =>
    fetchAPI<{ role: string; content: string }[]>(`/api/agents/${agent}/chat`),
  chat: (agent: string, message: string) =>
    fetchAPI<{ response: string }>(`/api/agents/${agent}/chat`, {
      method: 'POST',
      body: JSON.stringify({ message }),
    }),
  resetChat: (agent: string) =>
    fetchAPI<{ status: string }>(`/api/agents/${agent}/chat`, { method: 'DELETE' }),

  // Streaming chat
  chatStream: (
    agent: string,
    message: string,
    onEvent: (event: import('./types').ChatEvent) => void,
    signal?: AbortSignal,
  ): Promise<void> => {
    return fetch(`${BASE}/api/agents/${agent}/chat/stream`, {
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
}
