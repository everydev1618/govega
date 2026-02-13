import { useState } from 'react'
import { useSSE } from '../hooks/useSSE'

export function EventStream() {
  const { events, connected } = useSSE('/api/events', 500)
  const [filter, setFilter] = useState('')

  const filtered = filter
    ? events.filter(e => e.type.includes(filter) || e.agent?.includes(filter) || e.process_id?.includes(filter))
    : events

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Event Stream</h2>
        <span className={`text-xs px-2 py-1 rounded-full ${connected ? 'bg-green-900/50 text-green-400' : 'bg-red-900/50 text-red-400'}`}>
          {connected ? 'Connected' : 'Disconnected'}
        </span>
      </div>

      <input
        type="text"
        placeholder="Filter events..."
        value={filter}
        onChange={e => setFilter(e.target.value)}
        className="w-full px-3 py-2 rounded-lg bg-card border border-border text-sm focus:outline-none focus:border-primary"
      />

      <div className="space-y-1">
        {filtered.length === 0 && (
          <p className="text-muted-foreground text-sm">Waiting for events...</p>
        )}
        {filtered.map((event, i) => (
          <div key={i} className="flex items-center gap-3 p-2 rounded bg-card border border-border text-sm font-mono">
            <span className="text-xs text-muted-foreground w-20 shrink-0">
              {new Date(event.timestamp).toLocaleTimeString()}
            </span>
            <EventTypeBadge type={event.type} />
            <span className="text-muted-foreground">{event.process_id}</span>
            {event.agent && <span className="text-foreground">{event.agent}</span>}
          </div>
        ))}
      </div>
    </div>
  )
}

function EventTypeBadge({ type }: { type: string }) {
  const colors: Record<string, string> = {
    'process.started': 'text-blue-400',
    'process.completed': 'text-green-400',
    'process.failed': 'text-red-400',
    'workflow.completed': 'text-emerald-400',
    'workflow.failed': 'text-rose-400',
  }
  return <span className={`text-xs w-36 shrink-0 ${colors[type] || 'text-muted-foreground'}`}>{type}</span>
}
