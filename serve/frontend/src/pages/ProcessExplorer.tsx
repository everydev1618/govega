import { useState, useEffect, useRef } from 'react'
import { useAPI } from '../hooks/useAPI'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import { Modal } from '../components/Modal'
import { StatusBadge } from '../components/StatusBadge'
import type { ProcessResponse, ProcessDetailResponse } from '../lib/types'

export function ProcessExplorer() {
  const { data: processes, loading, refetch } = useAPI(() => api.getProcesses())
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<ProcessDetailResponse | null>(null)
  const [sortKey, setSortKey] = useState<'started_at' | 'status' | 'agent'>('started_at')

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
        <div className="flex gap-2 text-sm">
          {(['started_at', 'status', 'agent'] as const).map(key => (
            <button key={key} onClick={() => setSortKey(key)}
              className={`px-3 py-1 rounded ${sortKey === key ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50'}`}>
              {key === 'started_at' ? 'Time' : key.charAt(0).toUpperCase() + key.slice(1)}
            </button>
          ))}
        </div>
      </div>

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

      <Modal
        open={!!(selectedId && detail)}
        onClose={() => setSelectedId(null)}
        title={detail ? `Process ${detail.id}` : ''}
      >
        {detail && (
          <div className="space-y-4">
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
              <div className="space-y-2">
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
      </Modal>
    </div>
  )
}
