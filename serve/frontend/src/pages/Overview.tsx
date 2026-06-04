import { useAPI } from '../hooks/useAPI'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import { Link } from 'react-router-dom'
import { AgentOrgChart } from '../components/AgentOrgChart'

export function Overview() {
  const { data: stats, loading } = useAPI(() => api.getStats())
  const { data: agents } = useAPI(() => api.getAgents())
  const { data: workflows } = useAPI(() => api.getWorkflows())
  const { events, connected } = useSSE()

  if (loading) return <PageSkeleton />

  const hasAgents = agents && agents.length > 2 // more than just hera + iris
  const hasWorkflows = workflows && workflows.length > 0

  return (
    <div className="space-y-7">
      <div className="flex items-center justify-between pb-4 border-b border-rule">
        <div>
          <p className="anno mb-1.5">Dashboard</p>
          <h1 className="text-2xl font-semibold text-ink tracking-tight">Overview</h1>
        </div>
        <span className={`chip ${connected ? 'chip-dot' : 'chip-signal'}`}>
          {connected ? 'Live' : 'Disconnected'}
        </span>
      </div>

      {/* Getting started — shown when the server is mostly empty */}
      {!hasAgents && (
        <div className="p-5 rounded-sm border border-rule bg-paper-deep space-y-4">
          <div>
            <p className="anno mb-1.5">Get started</p>
            <h3 className="text-lg font-semibold text-ink">Three ways in.</h3>
          </div>
          <p className="text-sm text-ink-soft max-w-prose">
            Talk to Iris and she'll route your goals across agents, ask Hera to build new ones, and set up schedules. Or go deeper:
          </p>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <StepCard
              step={1}
              title="Chat with Iris"
              description="Describe what you need. Iris orchestrates across all agents, calls Hera to create new ones, and sets up schedules."
              to="/"
              cta="Open Chat"
            />
            <StepCard
              step={2}
              title="Browse the Population"
              description="Explore the library of agent personas and skill packs. Install them and ask Iris to put them to work."
              to="/population"
              cta="Open Population"
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
        <div className="grid grid-cols-2 md:grid-cols-4 gap-0 border-y border-rule">
          <StatCard label="Running" value={stats.running_processes} tone="running" border="right" />
          <StatCard label="Completed" value={stats.completed_processes} border="right" />
          <StatCard label="Total cost" value={`$${stats.total_cost_usd.toFixed(4)}`} border="right" />
          <StatCard label="Uptime" value={stats.uptime} />
        </div>
      )}

      {/* Agent org chart */}
      {agents && agents.length > 0 && (
        <AgentOrgChart agents={agents} />
      )}

      {/* Workflows summary */}
      {hasWorkflows && (
        <div>
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-base font-semibold text-ink">Workflows</h3>
            <Link to="/workflows" className="text-xs text-brand hover:underline">View all →</Link>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-0 border-y border-rule">
            {workflows!.slice(0, 6).map((wf, idx) => (
              <Link
                key={wf.name}
                to="/workflows"
                className={`p-4 bg-paper hover:bg-paper-deep transition-colors group ${idx % 3 !== 2 ? 'md:border-r md:border-rule' : ''} ${idx !== workflows!.slice(0,6).length - 1 ? 'border-b border-rule md:border-b' : ''}`}
              >
                <div className="flex items-baseline gap-2">
                  <span className="font-medium text-sm text-ink group-hover:text-brand transition-colors">{wf.name}</span>
                  <span className="text-2xs font-mono text-ink-faint tnum ml-auto">{wf.steps} step{wf.steps !== 1 ? 's' : ''}</span>
                </div>
                {wf.description && (
                  <p className="text-xs text-ink-soft mt-1 truncate">{wf.description}</p>
                )}
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Recent events */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-base font-semibold text-ink">Recent events</h3>
          <span className="anno">/api/v1/events</span>
        </div>
        {events.length === 0 ? (
          <p className="text-ink-soft text-sm py-6 border-y border-rule">
            No events yet. Compose an agent or launch a workflow to see activity.
          </p>
        ) : (
          <div className="border-y border-rule divide-y divide-rule">
            {events.slice(0, 10).map((event, i) => (
              <div key={i} className="flex items-center gap-3 px-3 py-2 text-sm hover:bg-paper-deep/60 transition-colors">
                <EventBadge type={event.type} />
                <span className="text-ink-soft font-mono text-xs">{event.agent || event.process_id}</span>
                <span className="ml-auto text-2xs text-ink-faint font-mono tnum">
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
    <div className="p-4 rounded-sm border border-rule bg-paper space-y-2.5">
      <div className="flex items-baseline gap-2">
        <span className="anno tnum">§{step}</span>
        <h4 className="font-semibold text-sm text-ink">{title}</h4>
      </div>
      <p className="text-xs text-ink-soft leading-relaxed">{description}</p>
      <Link
        to={to}
        className="inline-flex items-center gap-1.5 mt-1 px-2.5 py-1.5 rounded-sm bg-ink text-paper text-xs font-medium hover:bg-brand-deep transition-colors"
      >
        {cta} <span className="font-mono text-2xs">↗</span>
      </Link>
    </div>
  )
}

function QuickLink({ to, label, count, sublabel }: {
  to: string; label: string; count: number | null; sublabel: string
}) {
  return (
    <Link to={to} className="p-3 rounded-sm bg-paper border border-rule hover:border-brand transition-colors group">
      <p className="text-xs text-ink-faint font-mono uppercase tracking-wider mb-1">{label}</p>
      {count !== null ? (
        <p className="text-base text-ink group-hover:text-brand transition-colors">
          <span className="font-semibold tnum">{count}</span>
          <span className="text-xs text-ink-faint ml-1.5">{sublabel}</span>
        </p>
      ) : (
        <p className="text-xs text-ink-soft group-hover:text-brand transition-colors">{sublabel} →</p>
      )}
    </Link>
  )
}

function StatCard({ label, value, tone, border }: {
  label: string
  value: string | number
  tone?: 'running' | 'signal'
  border?: 'right'
}) {
  const valueColor = tone === 'running' ? 'text-running' : tone === 'signal' ? 'text-signal' : 'text-ink'
  const borderClass = border === 'right' ? 'md:border-r md:border-rule' : ''
  return (
    <div className={`px-4 py-4 bg-paper ${borderClass}`}>
      <p className="anno mb-1.5">{label}</p>
      <p className={`text-2xl font-semibold tnum ${valueColor}`}>{value}</p>
    </div>
  )
}

function EventBadge({ type }: { type: string }) {
  // Color encodes outcome: running = success, signal = failure, ink-soft = neutral
  const colors: Record<string, string> = {
    'process.started': 'text-ink',
    'process.completed': 'text-running',
    'process.failed': 'text-signal',
  }
  const color = colors[type] || 'text-ink-soft'
  return (
    <span className={`text-2xs font-mono ${color} flex-shrink-0 w-28`}>
      {type}
    </span>
  )
}

function PageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="h-8 w-48 bg-paper-deep rounded-sm animate-pulse" />
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-20 bg-paper-deep rounded-sm animate-pulse" />
        ))}
      </div>
    </div>
  )
}
