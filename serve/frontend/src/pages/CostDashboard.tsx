import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'

export function CostDashboard() {
  const { data: stats, loading: statsLoading } = useAPI(() => api.getStats())
  const { data: processes, loading: procsLoading } = useAPI(() => api.getProcesses())

  if (statsLoading || procsLoading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  // Aggregate costs per agent
  const agentCosts: Record<string, { cost: number; tokens: number; processes: number }> = {}
  for (const p of processes || []) {
    if (!agentCosts[p.agent]) {
      agentCosts[p.agent] = { cost: 0, tokens: 0, processes: 0 }
    }
    agentCosts[p.agent].cost += p.metrics.cost_usd
    agentCosts[p.agent].tokens += p.metrics.input_tokens + p.metrics.output_tokens
    agentCosts[p.agent].processes++
  }

  const sortedAgents = Object.entries(agentCosts).sort((a, b) => b[1].cost - a[1].cost)
  const maxCost = sortedAgents.length > 0 ? sortedAgents[0][1].cost : 1

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">Cost Dashboard</h2>

      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <CostCard label="Total Cost" value={`$${stats.total_cost_usd.toFixed(4)}`} />
          <CostCard label="Input Tokens" value={stats.total_input_tokens.toLocaleString()} />
          <CostCard label="Output Tokens" value={stats.total_output_tokens.toLocaleString()} />
          <CostCard label="Tool Calls" value={stats.total_tool_calls.toLocaleString()} />
        </div>
      )}

      <div>
        <h3 className="text-lg font-semibold mb-3">Cost by Agent</h3>
        {sortedAgents.length === 0 ? (
          <p className="text-muted-foreground text-sm">No cost data yet.</p>
        ) : (
          <div className="space-y-3">
            {sortedAgents.map(([name, data]) => (
              <div key={name} className="space-y-1">
                <div className="flex items-center justify-between text-sm">
                  <span className="font-medium">{name}</span>
                  <span className="text-muted-foreground">
                    ${data.cost.toFixed(4)} &middot; {data.tokens.toLocaleString()} tokens &middot; {data.processes} process{data.processes !== 1 ? 'es' : ''}
                  </span>
                </div>
                <div className="h-3 bg-muted rounded-full overflow-hidden">
                  <div
                    className="h-full bg-gradient-to-r from-indigo-500 to-purple-500 rounded-full transition-all"
                    style={{ width: `${(data.cost / maxCost) * 100}%` }}
                  />
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Per-process table */}
      <div>
        <h3 className="text-lg font-semibold mb-3">Per-Process Breakdown</h3>
        <div className="rounded-lg border border-border overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-muted/50">
                <th className="text-left p-2 font-medium text-muted-foreground">Process</th>
                <th className="text-left p-2 font-medium text-muted-foreground">Agent</th>
                <th className="text-right p-2 font-medium text-muted-foreground">Input</th>
                <th className="text-right p-2 font-medium text-muted-foreground">Output</th>
                <th className="text-right p-2 font-medium text-muted-foreground">Cost</th>
              </tr>
            </thead>
            <tbody>
              {(processes || []).filter(p => p.metrics.cost_usd > 0).sort((a, b) => b.metrics.cost_usd - a.metrics.cost_usd).map(p => (
                <tr key={p.id} className="border-t border-border">
                  <td className="p-2 font-mono">{p.id}</td>
                  <td className="p-2">{p.agent}</td>
                  <td className="p-2 text-right text-muted-foreground">{p.metrics.input_tokens.toLocaleString()}</td>
                  <td className="p-2 text-right text-muted-foreground">{p.metrics.output_tokens.toLocaleString()}</td>
                  <td className="p-2 text-right font-medium">${p.metrics.cost_usd.toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

function CostCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="p-4 rounded-lg bg-card border border-border">
      <p className="text-xs text-muted-foreground mb-1">{label}</p>
      <p className="text-2xl font-bold">{value}</p>
    </div>
  )
}
