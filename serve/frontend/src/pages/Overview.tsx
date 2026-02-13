import { useAPI } from '../hooks/useAPI'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import { Link } from 'react-router-dom'

export function Overview() {
  const { data: stats, loading } = useAPI(() => api.getStats())
  const { data: agents } = useAPI(() => api.getAgents())
  const { data: workflows } = useAPI(() => api.getWorkflows())
  const { events, connected } = useSSE()

  if (loading) return <PageSkeleton />

  const hasAgents = agents && agents.length > 1 // more than just the default assistant
  const hasWorkflows = workflows && workflows.length > 0

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Overview</h2>
        <span className={`text-xs px-2 py-1 rounded-full ${connected ? 'bg-green-900/50 text-green-400' : 'bg-red-900/50 text-red-400'}`}>
          {connected ? 'Live' : 'Disconnected'}
        </span>
      </div>

      {/* Getting started — shown when the server is mostly empty */}
      {!hasAgents && (
        <div className="p-5 rounded-lg border border-border bg-card space-y-5">
          <h3 className="text-lg font-semibold">Get Started</h3>
          <p className="text-sm text-muted-foreground">
            Vega lets you build AI agents from reusable personas and skills, then orchestrate them with workflows. Here's how:
          </p>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <StepCard
              step={1}
              title="Browse & Install"
              description="Explore the population library. Install personas (agent personalities) and skills (tool collections)."
              to="/population"
              cta="Open Population"
            />
            <StepCard
              step={2}
              title="Compose an Agent"
              description="Pick a persona, attach skills, and create a live agent. It spawns immediately with all its tools."
              to="/agents"
              cta="Open Agents"
            />
            <StepCard
              step={3}
              title="Run a Workflow"
              description="Send tasks to your agents through workflows, or load a .vega.yaml config with multi-step pipelines."
              to="/workflows"
              cta="Open Workflows"
            />
          </div>
        </div>
      )}

      {/* Quick actions — always shown */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <QuickLink to="/population" label="Population" count={null} sublabel="Browse library" />
        <QuickLink to="/agents" label="Agents" count={agents?.length ?? 0} sublabel="active" />
        <QuickLink to="/workflows" label="Workflows" count={workflows?.length ?? 0} sublabel="available" />
        <QuickLink to="/processes" label="Processes" count={stats?.total_processes ?? 0} sublabel="total" />
      </div>

      {/* Stats grid */}
      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <StatCard label="Running" value={stats.running_processes} color="text-blue-400" />
          <StatCard label="Completed" value={stats.completed_processes} color="text-green-400" />
          <StatCard label="Total Cost" value={`$${stats.total_cost_usd.toFixed(4)}`} />
          <StatCard label="Uptime" value={stats.uptime} />
        </div>
      )}

      {/* Active agents summary */}
      {agents && agents.length > 0 && (
        <div>
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-lg font-semibold">Agents</h3>
            <Link to="/agents" className="text-xs text-primary hover:underline">View all</Link>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {agents.slice(0, 6).map(agent => (
              <div key={agent.name} className="p-3 rounded-lg bg-card border border-border">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-sm">{agent.name}</span>
                  {agent.source === 'composed' && (
                    <span className="text-xs px-1.5 py-0.5 rounded bg-purple-900/50 text-purple-400">composed</span>
                  )}
                  {agent.process_status && (
                    <span className={`text-xs px-1.5 py-0.5 rounded ml-auto ${
                      agent.process_status === 'running' ? 'bg-blue-900/50 text-blue-400' :
                      agent.process_status === 'completed' ? 'bg-green-900/50 text-green-400' :
                      'bg-muted text-muted-foreground'
                    }`}>
                      {agent.process_status}
                    </span>
                  )}
                </div>
                {agent.model && (
                  <p className="text-xs text-muted-foreground mt-1 font-mono">{agent.model}</p>
                )}
                {agent.tools && agent.tools.length > 0 && (
                  <p className="text-xs text-muted-foreground mt-1">{agent.tools.length} tools</p>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Workflows summary */}
      {hasWorkflows && (
        <div>
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-lg font-semibold">Workflows</h3>
            <Link to="/workflows" className="text-xs text-primary hover:underline">View all</Link>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {workflows!.slice(0, 6).map(wf => (
              <Link
                key={wf.name}
                to="/workflows"
                className="p-3 rounded-lg bg-card border border-border hover:border-primary/50 transition-colors"
              >
                <span className="font-medium text-sm">{wf.name}</span>
                {wf.description && (
                  <p className="text-xs text-muted-foreground mt-1">{wf.description}</p>
                )}
                <p className="text-xs text-muted-foreground mt-1">{wf.steps} steps</p>
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Recent events */}
      <div>
        <h3 className="text-lg font-semibold mb-3">Recent Events</h3>
        {events.length === 0 ? (
          <p className="text-muted-foreground text-sm">No events yet. Compose an agent or launch a workflow to see activity.</p>
        ) : (
          <div className="space-y-2">
            {events.slice(0, 10).map((event, i) => (
              <div key={i} className="flex items-center gap-3 p-3 rounded-lg bg-card border border-border text-sm">
                <EventBadge type={event.type} />
                <span className="text-muted-foreground">{event.agent || event.process_id}</span>
                <span className="ml-auto text-xs text-muted-foreground">
                  {new Date(event.timestamp).toLocaleTimeString()}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function StepCard({ step, title, description, to, cta }: {
  step: number; title: string; description: string; to: string; cta: string
}) {
  return (
    <div className="p-4 rounded-lg border border-border bg-background space-y-2">
      <div className="flex items-center gap-2">
        <span className="w-6 h-6 rounded-full bg-primary text-primary-foreground text-xs font-bold flex items-center justify-center">
          {step}
        </span>
        <h4 className="font-semibold text-sm">{title}</h4>
      </div>
      <p className="text-xs text-muted-foreground">{description}</p>
      <Link
        to={to}
        className="inline-block mt-1 px-3 py-1.5 rounded bg-primary text-primary-foreground text-xs font-medium hover:opacity-90 transition-opacity"
      >
        {cta}
      </Link>
    </div>
  )
}

function QuickLink({ to, label, count, sublabel }: {
  to: string; label: string; count: number | null; sublabel: string
}) {
  return (
    <Link to={to} className="p-3 rounded-lg bg-card border border-border hover:border-primary/50 transition-colors">
      <p className="text-sm font-medium">{label}</p>
      {count !== null ? (
        <p className="text-lg font-bold">{count} <span className="text-xs font-normal text-muted-foreground">{sublabel}</span></p>
      ) : (
        <p className="text-xs text-muted-foreground">{sublabel}</p>
      )}
    </Link>
  )
}

function StatCard({ label, value, color }: { label: string; value: string | number; color?: string }) {
  return (
    <div className="p-4 rounded-lg bg-card border border-border">
      <p className="text-xs text-muted-foreground mb-1">{label}</p>
      <p className={`text-2xl font-bold ${color || ''}`}>{value}</p>
    </div>
  )
}

function EventBadge({ type }: { type: string }) {
  const colors: Record<string, string> = {
    'process.started': 'bg-blue-900/50 text-blue-400',
    'process.completed': 'bg-green-900/50 text-green-400',
    'process.failed': 'bg-red-900/50 text-red-400',
  }
  return (
    <span className={`text-xs px-2 py-0.5 rounded ${colors[type] || 'bg-muted text-muted-foreground'}`}>
      {type.replace('process.', '')}
    </span>
  )
}

function PageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="h-8 w-48 bg-muted rounded animate-pulse" />
      <div className="grid grid-cols-4 gap-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-20 bg-muted rounded animate-pulse" />
        ))}
      </div>
    </div>
  )
}
