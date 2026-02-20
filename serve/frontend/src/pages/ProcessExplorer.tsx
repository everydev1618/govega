import { useState, useEffect, useRef } from 'react'
import { useAPI } from '../hooks/useAPI'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import { KanbanBoard } from '../components/KanbanBoard'
import type { ProcessResponse, ProcessDetailResponse } from '../lib/types'

type ViewMode = 'list' | 'kanban'

export function ProcessExplorer() {
  const { data: processes, loading, refetch } = useAPI(() => api.getProcesses())
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<ProcessDetailResponse | null>(null)
  const [sortKey, setSortKey] = useState<'started_at' | 'status' | 'agent'>('started_at')
  const [viewMode, setViewMode] = useState<ViewMode>('kanban')

  // SSE: auto-refresh on process lifecycle events
  const { events } = useSSE()
  const lastEventRef = useRef(0)
  useEffect(() => {
    if (events.length === 0) return
    const latest = events[0]
    const ts = new Date(latest.timestamp).getTime()
    if (ts > lastEventRef.current && latest.type.startsWith('process.')) {
      lastEventRef.current = ts
      refetch()
    }
  }, [events, refetch])

  // Poll every 5s while any process is running (metrics aren't pushed via SSE)
  useEffect(() => {
    const hasRunning = processes?.some(p => p.status === 'running')
    if (!hasRunning) return
    const id = setInterval(refetch, 5000)
    return () => clearInterval(id)
  }, [processes, refetch])

  const sorted = processes ? [...processes].sort((a, b) => {
    if (sortKey === 'started_at') return new Date(b.started_at).getTime() - new Date(a.started_at).getTime()
    if (sortKey === 'status') return a.status.localeCompare(b.status)
    return a.agent.localeCompare(b.agent)
  }) : []

  const openDetail = async (id: string) => {
    setSelectedId(id)
    const d = await api.getProcess(id)
    setDetail(d)
  }

  const handleKill = async (id: string) => {
    await api.killProcess(id)
    refetch()
    if (selectedId === id) setSelectedId(null)
  }

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Process Explorer</h2>
        <div className="flex items-center gap-4">
          {/* View toggle */}
          <div className="flex gap-1 text-sm border border-border rounded-lg p-0.5">
            <button
              onClick={() => setViewMode('kanban')}
              className={`px-3 py-1 rounded-md transition-colors ${viewMode === 'kanban' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50'}`}
            >
              Board
            </button>
            <button
              onClick={() => setViewMode('list')}
              className={`px-3 py-1 rounded-md transition-colors ${viewMode === 'list' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50'}`}
            >
              List
            </button>
          </div>

          {/* Sort controls — only in list mode */}
          {viewMode === 'list' && (
            <div className="flex gap-2 text-sm">
              {(['started_at', 'status', 'agent'] as const).map(key => (
                <button key={key} onClick={() => setSortKey(key)}
                  className={`px-3 py-1 rounded ${sortKey === key ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50'}`}>
                  {key === 'started_at' ? 'Time' : key.charAt(0).toUpperCase() + key.slice(1)}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="flex gap-4">
        {/* Main content area */}
        <div className="flex-1 min-w-0">
          {viewMode === 'kanban' ? (
            <KanbanBoard
              processes={processes || []}
              selectedId={selectedId}
              onSelect={openDetail}
            />
          ) : (
            <div className="space-y-2">
              {sorted.length === 0 && <p className="text-muted-foreground text-sm">No processes running.</p>}
              {sorted.map((p: ProcessResponse) => (
                <div key={p.id} onClick={() => openDetail(p.id)}
                  className={`p-3 rounded-lg border cursor-pointer transition-colors ${selectedId === p.id ? 'border-primary bg-accent' : 'border-border bg-card hover:border-primary/50'}`}>
                  <div className="flex items-center justify-between mb-1">
                    <span className="font-mono text-sm">{p.id}</span>
                    <StatusBadge status={p.status} />
                  </div>
                  <div className="flex items-center justify-between text-sm text-muted-foreground">
                    <span>{p.agent}</span>
                    <span>{new Date(p.started_at).toLocaleTimeString()}</span>
                  </div>
                  {p.task && <p className="text-xs text-muted-foreground mt-1 truncate">{p.task}</p>}
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Detail panel */}
        {selectedId && detail && (
          <div className="w-96 shrink-0 border border-border rounded-lg bg-card p-4 space-y-4 max-h-[80vh] overflow-auto">
            <div className="flex items-center justify-between">
              <h3 className="font-bold">Process {detail.id}</h3>
              <button onClick={() => setSelectedId(null)} className="text-muted-foreground hover:text-foreground">&times;</button>
            </div>
            <div className="grid grid-cols-2 gap-2 text-sm">
              <div className="text-muted-foreground">Agent</div><div>{detail.agent}</div>
              <div className="text-muted-foreground">Status</div><div><StatusBadge status={detail.status} /></div>
              <div className="text-muted-foreground">Tokens</div><div>{detail.metrics.input_tokens + detail.metrics.output_tokens}</div>
              <div className="text-muted-foreground">Cost</div><div>${detail.metrics.cost_usd.toFixed(4)}</div>
              <div className="text-muted-foreground">Tool Calls</div><div>{detail.metrics.tool_calls}</div>
            </div>
            {(detail.status === 'running' || detail.status === 'pending') && (
              <button onClick={() => handleKill(detail.id)}
                className="w-full py-1.5 rounded bg-destructive text-white text-sm hover:bg-destructive/80">
                Kill Process
              </button>
            )}
            <div>
              <h4 className="text-sm font-semibold mb-2">Messages ({detail.messages.length})</h4>
              <div className="space-y-2 max-h-96 overflow-auto">
                {detail.messages.map((m, i) => (
                  <div key={i} className={`p-2 rounded text-xs ${m.role === 'user' ? 'bg-blue-900/20 border border-blue-900/30' : m.role === 'assistant' ? 'bg-muted' : 'bg-yellow-900/20 border border-yellow-900/30'}`}>
                    <span className="font-bold text-muted-foreground">{m.role}</span>
                    <pre className="mt-1 whitespace-pre-wrap break-words">{m.content.slice(0, 500)}{m.content.length > 500 ? '...' : ''}</pre>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    running: 'bg-blue-900/50 text-blue-400',
    pending: 'bg-yellow-900/50 text-yellow-400',
    completed: 'bg-green-900/50 text-green-400',
    failed: 'bg-red-900/50 text-red-400',
    timeout: 'bg-orange-900/50 text-orange-400',
  }
  return (
    <span className={`text-xs px-2 py-0.5 rounded ${colors[status] || 'bg-muted text-muted-foreground'}`}>
      {status}
    </span>
  )
}
