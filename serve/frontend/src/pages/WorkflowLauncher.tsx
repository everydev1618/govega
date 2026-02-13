import { useState } from 'react'
import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'
import type { WorkflowResponse } from '../lib/types'

export function WorkflowLauncher() {
  const { data: workflows, loading } = useAPI(() => api.getWorkflows())
  const [selected, setSelected] = useState<WorkflowResponse | null>(null)
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [result, setResult] = useState<{ runId: string; status: string } | null>(null)
  const [launching, setLaunching] = useState(false)

  const selectWorkflow = (wf: WorkflowResponse) => {
    setSelected(wf)
    setResult(null)
    const defaults: Record<string, string> = {}
    if (wf.inputs) {
      for (const [name, input] of Object.entries(wf.inputs)) {
        defaults[name] = input.default ? String(input.default) : ''
      }
    }
    setInputs(defaults)
  }

  const launch = async () => {
    if (!selected) return
    setLaunching(true)
    try {
      // Convert string inputs to appropriate types
      const parsed: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(inputs)) {
        parsed[k] = v
      }
      const res = await api.runWorkflow(selected.name, parsed)
      setResult({ runId: res.run_id, status: res.status })
    } catch (err) {
      setResult({ runId: '', status: `Error: ${err}` })
    } finally {
      setLaunching(false)
    }
  }

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">Workflow Launcher</h2>

      <div className="flex gap-6">
        {/* Workflow list */}
        <div className="w-64 space-y-2">
          {workflows?.map(wf => (
            <button key={wf.name} onClick={() => selectWorkflow(wf)}
              className={`w-full text-left p-3 rounded-lg border transition-colors ${
                selected?.name === wf.name ? 'border-primary bg-accent' : 'border-border bg-card hover:border-primary/50'
              }`}>
              <div className="font-semibold text-sm">{wf.name}</div>
              {wf.description && <div className="text-xs text-muted-foreground mt-0.5">{wf.description}</div>}
              <div className="text-xs text-muted-foreground mt-1">{wf.steps} steps</div>
            </button>
          ))}
        </div>

        {/* Input form */}
        {selected && (
          <div className="flex-1 p-4 rounded-lg bg-card border border-border space-y-4">
            <h3 className="font-semibold">Launch: {selected.name}</h3>

            {selected.inputs && Object.entries(selected.inputs).map(([name, input]) => (
              <div key={name}>
                <label className="block text-sm mb-1">
                  {name}
                  {input.required && <span className="text-red-400 ml-1">*</span>}
                  {input.description && <span className="text-muted-foreground ml-2 text-xs">({input.description})</span>}
                </label>
                {input.enum ? (
                  <select
                    value={inputs[name] || ''}
                    onChange={e => setInputs(p => ({ ...p, [name]: e.target.value }))}
                    className="w-full px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary"
                  >
                    <option value="">Select...</option>
                    {input.enum.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                ) : (
                  <input
                    type="text"
                    value={inputs[name] || ''}
                    onChange={e => setInputs(p => ({ ...p, [name]: e.target.value }))}
                    placeholder={input.default ? `Default: ${input.default}` : ''}
                    className="w-full px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary"
                  />
                )}
              </div>
            ))}

            <button onClick={launch} disabled={launching}
              className="px-4 py-2 rounded bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50">
              {launching ? 'Launching...' : 'Launch Workflow'}
            </button>

            {result && (
              <div className={`p-3 rounded text-sm ${result.status === 'running' ? 'bg-blue-900/20 border border-blue-900/30' : 'bg-red-900/20 border border-red-900/30'}`}>
                {result.runId ? (
                  <span>Run <span className="font-mono">{result.runId}</span> — {result.status}</span>
                ) : (
                  <span>{result.status}</span>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
