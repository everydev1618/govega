import { useEffect, useState } from 'react'
import { api, type ReactiveEvent } from '../lib/api'
import { useAPI } from '../hooks/useAPI'

type FilterKey = 'all' | 'fired' | 'gated' | 'signal' | 'agent'

const FILTERS: { key: FilterKey; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'fired', label: 'Fired' },
  { key: 'gated', label: 'Gated' },
  { key: 'signal', label: 'signal.*' },
  { key: 'agent', label: 'agent.*' },
]

function matches(e: ReactiveEvent, f: FilterKey): boolean {
  switch (f) {
    case 'fired': return e.type === 'reactive.fired'
    case 'gated': return e.type.startsWith('reactive.gated')
    case 'signal': return e.type.startsWith('signal.')
    case 'agent': return e.type.startsWith('agent.')
    default: return true
  }
}

export function Reactive() {
  const { data, loading, refetch } = useAPI(() => api.getReactiveActivity(200))
  const [filter, setFilter] = useState<FilterKey>('all')

  // Poll for liveness — the reactive loop runs on its own clock.
  useEffect(() => {
    const id = setInterval(refetch, 4000)
    return () => clearInterval(id)
  }, [refetch])

  const events = data?.events ?? []
  const shown = events.filter(e => matches(e, filter))

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold">Reactive</h2>
          <p className="text-sm text-muted-foreground mt-0.5">
            What events made agents think — and what the salience gate ignored.
          </p>
        </div>
        <span className="text-xs text-muted-foreground tabular-nums">
          {events.length} event{events.length === 1 ? '' : 's'}
        </span>
      </div>

      <div className="flex flex-wrap gap-2">
        {FILTERS.map(({ key, label }) => (
          <button
            key={key}
            onClick={() => setFilter(key)}
            className={`text-xs px-2.5 py-1 rounded-full border transition-colors ${
              filter === key
                ? 'bg-primary text-primary-foreground border-primary'
                : 'bg-card border-border text-muted-foreground hover:text-foreground'
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      <div className="space-y-1">
        {loading && events.length === 0 && (
          <p className="text-muted-foreground text-sm">Loading reactive activity…</p>
        )}
        {!loading && shown.length === 0 && (
          <p className="text-muted-foreground text-sm">
            No reactive activity yet. Give an agent a <span className="font-mono">triggers:</span> block,
            or fire one with <span className="font-mono">POST /api/v1/events</span>.
          </p>
        )}
        {shown.map(e => (
          <div key={e.id} className="flex items-center gap-3 p-2 rounded bg-card border border-border text-sm font-mono">
            <span className="text-xs text-muted-foreground w-20 shrink-0">
              {new Date(e.timestamp).toLocaleTimeString()}
            </span>
            <ReactiveBadge type={e.type} />
            {e.agent_name && <span className="text-foreground shrink-0">{e.agent_name}</span>}
            <span className="text-muted-foreground truncate">{detailOf(e)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// detailOf surfaces the most useful secondary fact per row: for a router
// decision, the triggering event type (stored in result); for a spine event,
// its payload.
function detailOf(e: ReactiveEvent): string {
  if (e.type.startsWith('reactive.')) return e.result ? `← ${e.result}` : ''
  if (e.data && e.data !== 'null' && e.data !== '{}') return e.data
  return ''
}

function ReactiveBadge({ type }: { type: string }) {
  let cls = 'text-muted-foreground'
  if (type === 'reactive.fired') cls = 'text-green-400'
  else if (type.startsWith('reactive.gated')) cls = 'text-amber-400'
  else if (type.startsWith('signal.')) cls = 'text-primary'
  else if (type === 'agent.completed') cls = 'text-blue-400'
  else if (type === 'schedule.fired' || type === 'memory.wrote') cls = 'text-cyan-400'
  return <span className={`text-xs w-40 shrink-0 ${cls}`}>{type}</span>
}
