const BASE = ''

export async function fetchAPI<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`${res.status}: ${body}`)
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
}
