import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'

export function Schedules() {
  const { data: schedules, loading, refetch } = useAPI(() => api.getSchedules())
  const [error, setError] = useState<string | null>(null)

  const handleToggle = async (name: string, currentEnabled: boolean) => {
    setError(null)
    try {
      await api.toggleSchedule(name, !currentEnabled)
      refetch()
    } catch (err: any) {
      setError(err.message || 'Failed to toggle schedule')
    }
  }

  const handleDelete = async (name: string) => {
    setError(null)
    try {
      await api.deleteSchedule(name)
      refetch()
    } catch (err: any) {
      setError(err.message || 'Failed to delete schedule')
    }
  }

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-2xl font-bold">Schedules</h2>
        <p className="text-sm text-muted-foreground">
          Cron jobs that send messages to agents on a schedule.
        </p>
      </div>

      {error && (
        <div className="p-3 rounded bg-red-900/30 text-red-400 text-sm">{error}</div>
      )}

      {!schedules || schedules.length === 0 ? (
        <p className="text-muted-foreground text-sm">No scheduled jobs. Use Mother to create schedules via chat.</p>
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-muted/50 border-b border-border">
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Cron</th>
                <th className="text-left px-4 py-2 font-medium">Agent</th>
                <th className="text-left px-4 py-2 font-medium">Message</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
                <th className="text-right px-4 py-2 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {schedules.map((job) => (
                <tr key={job.name} className="border-b border-border last:border-0 hover:bg-muted/30">
                  <td className="px-4 py-2 font-mono">{job.name}</td>
                  <td className="px-4 py-2 font-mono text-muted-foreground">{job.cron}</td>
                  <td className="px-4 py-2">
                    <Link
                      to={`/chat/${job.agent}`}
                      className="text-indigo-400 hover:text-indigo-300 transition-colors"
                    >
                      {job.agent}
                    </Link>
                  </td>
                  <td className="px-4 py-2 text-muted-foreground max-w-xs truncate" title={job.message}>
                    {job.message}
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={`text-xs px-2 py-0.5 rounded ${
                        job.enabled
                          ? 'bg-emerald-900/40 text-emerald-400'
                          : 'bg-zinc-800 text-zinc-400'
                      }`}
                    >
                      {job.enabled ? 'enabled' : 'disabled'}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-right space-x-2">
                    <button
                      onClick={() => handleToggle(job.name, job.enabled)}
                      className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
                    >
                      {job.enabled ? 'Disable' : 'Enable'}
                    </button>
                    <button
                      onClick={() => handleDelete(job.name)}
                      className="text-xs text-red-400 hover:text-red-300 transition-colors"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
