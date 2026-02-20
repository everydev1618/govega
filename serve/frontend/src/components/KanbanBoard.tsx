import type { ProcessResponse } from '../lib/types'

interface KanbanBoardProps {
  processes: ProcessResponse[]
  selectedId: string | null
  onSelect: (id: string) => void
}

const columns = [
  { key: 'pending', label: 'Pending', color: 'yellow' },
  { key: 'running', label: 'Running', color: 'blue' },
  { key: 'completed', label: 'Completed', color: 'green' },
  { key: 'failed', label: 'Failed', color: 'red' },
] as const

const columnBg: Record<string, string> = {
  pending: 'bg-yellow-900/10 border-yellow-900/20',
  running: 'bg-blue-900/10 border-blue-900/20',
  completed: 'bg-green-900/10 border-green-900/20',
  failed: 'bg-red-900/10 border-red-900/20',
}

const countBadge: Record<string, string> = {
  pending: 'bg-yellow-900/50 text-yellow-400',
  running: 'bg-blue-900/50 text-blue-400',
  completed: 'bg-green-900/50 text-green-400',
  failed: 'bg-red-900/50 text-red-400',
}

const statusBadgeColors: Record<string, string> = {
  running: 'bg-blue-900/50 text-blue-400',
  pending: 'bg-yellow-900/50 text-yellow-400',
  completed: 'bg-green-900/50 text-green-400',
  failed: 'bg-red-900/50 text-red-400',
  timeout: 'bg-red-900/50 text-red-400',
}

function groupByStatus(processes: ProcessResponse[]) {
  const groups: Record<string, ProcessResponse[]> = {
    pending: [],
    running: [],
    completed: [],
    failed: [],
  }
  for (const p of processes) {
    const key = p.status === 'timeout' ? 'failed' : p.status
    if (groups[key]) {
      groups[key].push(p)
    }
  }
  // Sort each group: newest first
  for (const key of Object.keys(groups)) {
    groups[key].sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
  }
  return groups
}

function formatDuration(startedAt: string, completedAt?: string): string {
  const start = new Date(startedAt).getTime()
  const end = completedAt ? new Date(completedAt).getTime() : Date.now()
  const seconds = Math.floor((end - start) / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remaining = seconds % 60
  if (minutes < 60) return `${minutes}m ${remaining}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${minutes % 60}m`
}

export function KanbanBoard({ processes, selectedId, onSelect }: KanbanBoardProps) {
  const groups = groupByStatus(processes)

  return (
    <div className="flex gap-3 min-h-[60vh]">
      {columns.map(col => {
        const items = groups[col.key]
        return (
          <div
            key={col.key}
            className={`flex-1 min-w-0 rounded-lg border p-3 flex flex-col ${columnBg[col.key]}`}
          >
            {/* Column header */}
            <div className="flex items-center justify-between mb-3">
              <span className="text-sm font-semibold text-foreground">{col.label}</span>
              <span className={`text-xs px-2 py-0.5 rounded-full font-mono ${countBadge[col.key]}`}>
                {items.length}
              </span>
            </div>

            {/* Cards */}
            <div className="flex-1 overflow-y-auto space-y-2 min-h-0">
              {items.length === 0 && (
                <p className="text-xs text-muted-foreground/50 text-center py-8">No processes</p>
              )}
              {items.map(p => (
                <div
                  key={p.id}
                  onClick={() => onSelect(p.id)}
                  className={`p-3 rounded-lg border cursor-pointer transition-colors ${
                    selectedId === p.id
                      ? 'border-primary bg-accent'
                      : 'border-border bg-card hover:border-primary/50'
                  } ${p.status === 'running' ? 'animate-pulse-subtle' : ''}`}
                >
                  {/* Agent name + status */}
                  <div className="flex items-center justify-between mb-1">
                    <span className="font-semibold text-sm truncate">{p.agent}</span>
                    <span className={`text-xs px-2 py-0.5 rounded shrink-0 ml-2 ${statusBadgeColors[p.status] || 'bg-muted text-muted-foreground'}`}>
                      {p.status}
                    </span>
                  </div>

                  {/* Task */}
                  {p.task && (
                    <p className="text-xs text-muted-foreground truncate mb-2">{p.task}</p>
                  )}

                  {/* Metrics row */}
                  <div className="flex items-center gap-3 text-xs text-muted-foreground">
                    <span title="Duration">{formatDuration(p.started_at, p.completed_at)}</span>
                    <span title="Iterations">{p.metrics.iterations} iter</span>
                    <span title="Tool calls">{p.metrics.tool_calls} tools</span>
                    {p.metrics.cost_usd > 0 && (
                      <span title="Cost">${p.metrics.cost_usd.toFixed(3)}</span>
                    )}
                  </div>

                  {/* Process ID */}
                  <div className="mt-1.5 font-mono text-[10px] text-muted-foreground/50 truncate">
                    {p.id}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}
