import { useState, useEffect, useMemo } from 'react'
import { api } from '../lib/api'
import type { InboxItem, AgentResponse } from '../lib/types'
import { AgentAvatar } from '../components/chat/AgentAvatar'

export function InboxView() {
  const [items, setItems] = useState<InboxItem[]>([])
  const [agents, setAgents] = useState<AgentResponse[]>([])
  const [loading, setLoading] = useState(true)
  const [expandedId, setExpandedId] = useState<number | null>(null)

  useEffect(() => {
    Promise.all([
      api.getInbox().then(list => setItems(list ?? [])),
      api.getAgents().then(list => setAgents(list ?? [])),
    ])
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  // Poll for new items
  useEffect(() => {
    const id = setInterval(() => {
      api.getInbox().then(list => setItems(list ?? [])).catch(() => {})
    }, 10000)
    return () => clearInterval(id)
  }, [])

  const agentDisplayInfo = useMemo(() => {
    const m = new Map<string, { displayName: string; avatar: string }>()
    for (const a of agents) {
      m.set(a.name, { displayName: a.display_name || a.name, avatar: a.avatar || '' })
    }
    m.set('iris', { displayName: 'Iris', avatar: 'n2' })
    m.set('hera', { displayName: 'Hera', avatar: 'n6' })
    return m
  }, [agents])

  const pending = items.filter(i => i.status === 'pending')
  const resolved = items.filter(i => i.status === 'resolved')

  const clearResolved = async () => {
    try {
      await api.clearResolvedInbox()
      setItems(prev => prev.filter(i => i.status !== 'resolved'))
    } catch { /* ignore */ }
  }

  const priorityBadge = (priority: string) => {
    switch (priority) {
      case 'high': case 'urgent':
        return <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-red-500/20 text-red-400 font-medium">{priority}</span>
      case 'medium':
        return <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-yellow-500/20 text-yellow-400 font-medium">{priority}</span>
      default:
        return <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground font-medium">{priority || 'normal'}</span>
    }
  }

  const formatTime = (ts: string) => {
    const d = new Date(ts)
    const now = new Date()
    const diffMs = now.getTime() - d.getTime()
    const diffH = Math.floor(diffMs / (1000 * 60 * 60))
    if (diffH < 1) return 'just now'
    if (diffH < 24) return `${diffH}h ago`
    const diffD = Math.floor(diffH / 24)
    if (diffD < 7) return `${diffD}d ago`
    return d.toLocaleDateString()
  }

  const renderCard = (item: InboxItem) => {
    const info = agentDisplayInfo.get(item.from_agent)
    const isExpanded = expandedId === item.id

    return (
      <div
        key={item.id}
        className={`rounded-lg border border-border bg-card p-4 transition-colors cursor-pointer hover:border-muted-foreground/30 ${
          isExpanded ? 'ring-1 ring-primary/30' : ''
        }`}
        onClick={() => setExpandedId(isExpanded ? null : item.id)}
      >
        <div className="flex items-start gap-3">
          <AgentAvatar
            name={item.from_agent}
            displayName={info?.displayName}
            avatar={info?.avatar}
            size={7}
          />
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 mb-1">
              <span className="text-sm font-semibold text-foreground truncate">
                {info?.displayName || item.from_agent}
              </span>
              {item.priority && priorityBadge(item.priority)}
              <span className="text-[11px] text-muted-foreground/60 ml-auto flex-shrink-0">
                {formatTime(item.created_at)}
              </span>
            </div>
            <p className="text-sm font-medium text-foreground">{item.subject}</p>
            {item.body && !isExpanded && (
              <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{item.body}</p>
            )}
            {isExpanded && (
              <div className="mt-3 space-y-2">
                {item.body && (
                  <div className="text-sm text-foreground/90 whitespace-pre-wrap">{item.body}</div>
                )}
                {item.resolution && (
                  <div className="mt-2 p-2 rounded-md bg-green-500/10 border border-green-500/20">
                    <p className="text-xs text-green-400 font-medium mb-1">Resolution</p>
                    <p className="text-sm text-foreground/90">{item.resolution}</p>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    )
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-pulse text-muted-foreground">Loading inbox...</div>
      </div>
    )
  }

  return (
    <div className="max-w-2xl mx-auto">
      <h1 className="text-2xl font-bold mb-6">Inbox</h1>

      {items.length === 0 && (
        <div className="text-center py-16">
          <svg className="w-12 h-12 mx-auto text-muted-foreground/30 mb-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859M12 3v8.25m0 0l-3-3m3 3l3-3" />
          </svg>
          <p className="text-muted-foreground">Your inbox is empty</p>
          <p className="text-xs text-muted-foreground/60 mt-1">Agents will send items here when they need your attention</p>
        </div>
      )}

      {/* Pending */}
      {items.length > 0 && (
        <div className="mb-8">
          <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider mb-3">
            Pending ({pending.length})
          </h2>
          {pending.length > 0 ? (
            <div className="space-y-3">
              {pending.map(renderCard)}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground/50 py-4">Nothing pending</p>
          )}
        </div>
      )}

      {/* Done */}
      {resolved.length > 0 && (
        <div>
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider">
              Done ({resolved.length})
            </h2>
            <button
              onClick={clearResolved}
              className="text-xs text-muted-foreground hover:text-foreground px-2 py-1 rounded border border-border hover:bg-accent/50 transition-colors"
            >
              Clear
            </button>
          </div>
          <div className="space-y-3 opacity-60">
            {resolved.map(renderCard)}
          </div>
        </div>
      )}
    </div>
  )
}
