import { useState, useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAPI } from '../hooks/useAPI'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import type { PopulationInstalledItem, CreateAgentRequest, ProcessResponse } from '../lib/types'

export function AgentRegistry() {
  const navigate = useNavigate()
  const { data: agents, loading, refetch } = useAPI(() => api.getAgents())
  const [showComposer, setShowComposer] = useState(false)
  const [personas, setPersonas] = useState<PopulationInstalledItem[]>([])
  const [skills, setSkills] = useState<PopulationInstalledItem[]>([])
  const [form, setForm] = useState<CreateAgentRequest>({ name: '', model: 'claude-sonnet-4-20250514' })
  const [systemPreview, setSystemPreview] = useState('')
  const [selectedSkills, setSelectedSkills] = useState<string[]>([])
  const [selectedTeam, setSelectedTeam] = useState<string[]>([])
  const [creating, setCreating] = useState(false)
  const [expandedAgents, setExpandedAgents] = useState<Set<string>>(new Set())

  // Fetch live process data for metrics
  const { data: processes, refetch: refetchProcesses } = useAPI(() => api.getProcesses())
  const { events } = useSSE()
  const lastEventRef = useRef(0)
  useEffect(() => {
    if (events.length === 0) return
    const latest = events[0]
    const ts = new Date(latest.timestamp).getTime()
    if (ts > lastEventRef.current && latest.type.startsWith('process.')) {
      lastEventRef.current = ts
      refetchProcesses()
    }
  }, [events, refetchProcesses])

  // Poll every 5s while any agent process is running
  useEffect(() => {
    const hasRunning = processes?.some(p => p.status === 'running')
    if (!hasRunning) return
    const id = setInterval(refetchProcesses, 5000)
    return () => clearInterval(id)
  }, [processes, refetchProcesses])

  // Map process_id → process for quick lookup
  const processMap = new Map<string, ProcessResponse>()
  processes?.forEach(p => processMap.set(p.id, p))

  // Load installed personas & skills when composer opens.
  useEffect(() => {
    if (!showComposer) return
    api.populationInstalled('persona').then(setPersonas).catch(() => {})
    api.populationInstalled('skill').then(setSkills).catch(() => {})
  }, [showComposer])

  const onPersonaChange = async (personaName: string) => {
    setForm(f => ({ ...f, persona: personaName || undefined }))
    if (!personaName) {
      setSystemPreview('')
      return
    }
    try {
      const info = await api.populationInfo('persona', personaName)
      setSystemPreview(info.system_prompt || '')
    } catch {
      setSystemPreview('')
    }
  }

  const toggleSkill = (name: string) => {
    setSelectedSkills(prev =>
      prev.includes(name) ? prev.filter(s => s !== name) : [...prev, name]
    )
  }

  const toggleTeamMember = (name: string) => {
    setSelectedTeam(prev =>
      prev.includes(name) ? prev.filter(s => s !== name) : [...prev, name]
    )
  }

  const compose = async () => {
    setCreating(true)
    try {
      const req: CreateAgentRequest = {
        ...form,
        skills: selectedSkills.length > 0 ? selectedSkills : undefined,
        team: selectedTeam.length > 0 ? selectedTeam : undefined,
        system: systemPreview || undefined,
      }
      await api.createAgent(req)
      setShowComposer(false)
      setForm({ name: '', model: 'claude-sonnet-4-20250514' })
      setSelectedSkills([])
      setSelectedTeam([])
      setSystemPreview('')
      refetch()
    } catch {
      // ignore
    } finally {
      setCreating(false)
    }
  }

  const deleteAgent = async (name: string) => {
    try {
      await api.deleteAgent(name)
      refetch()
    } catch {
      // ignore
    }
  }

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Agent Registry</h2>
        <button
          onClick={() => setShowComposer(!showComposer)}
          className="px-4 py-2 rounded bg-primary text-primary-foreground text-sm font-medium"
        >
          {showComposer ? 'Cancel' : 'Compose Agent'}
        </button>
      </div>

      {/* Compose form */}
      {showComposer && (
        <div className="p-4 rounded-lg bg-card border border-border space-y-4">
          <h3 className="font-semibold">Compose New Agent</h3>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm text-muted-foreground mb-1">Name <span className="text-red-400">*</span></label>
              <input
                type="text"
                value={form.name}
                onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="my-agent"
                className="w-full px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary"
              />
            </div>
            <div>
              <label className="block text-sm text-muted-foreground mb-1">Model <span className="text-red-400">*</span></label>
              <input
                type="text"
                value={form.model}
                onChange={e => setForm(f => ({ ...f, model: e.target.value }))}
                className="w-full px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm text-muted-foreground mb-1">Persona</label>
            <select
              value={form.persona || ''}
              onChange={e => onPersonaChange(e.target.value)}
              className="w-full px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary"
            >
              <option value="">None</option>
              {personas.map(p => (
                <option key={p.name} value={p.name}>{p.name}</option>
              ))}
            </select>
          </div>

          {skills.length > 0 && (
            <div>
              <label className="block text-sm text-muted-foreground mb-1">Skills</label>
              <div className="flex flex-wrap gap-2">
                {skills.map(s => (
                  <button
                    key={s.name}
                    onClick={() => toggleSkill(s.name)}
                    className={`text-xs px-2.5 py-1 rounded border transition-colors ${
                      selectedSkills.includes(s.name)
                        ? 'border-primary bg-primary/20 text-primary'
                        : 'border-border bg-card text-muted-foreground hover:border-primary/50'
                    }`}
                  >
                    {s.name}
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Team picker — show other agents this agent can delegate to */}
          {agents && agents.length > 0 && (
            <div>
              <label className="block text-sm text-muted-foreground mb-1">Team <span className="text-xs">(agents this agent can delegate to)</span></label>
              <div className="flex flex-wrap gap-2">
                {agents.filter(a => a.name !== form.name).map(a => (
                  <button
                    key={a.name}
                    onClick={() => toggleTeamMember(a.name)}
                    className={`text-xs px-2.5 py-1 rounded border transition-colors ${
                      selectedTeam.includes(a.name)
                        ? 'border-green-500 bg-green-900/20 text-green-400'
                        : 'border-border bg-card text-muted-foreground hover:border-green-500/50'
                    }`}
                  >
                    {a.name}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div>
            <label className="block text-sm text-muted-foreground mb-1">System Prompt</label>
            <textarea
              value={systemPreview}
              onChange={e => setSystemPreview(e.target.value)}
              rows={6}
              className="w-full px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary font-mono text-xs"
              placeholder="System prompt (auto-filled from persona)"
            />
          </div>

          <button
            onClick={compose}
            disabled={creating || !form.name || !form.model}
            className="px-4 py-2 rounded bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
          >
            {creating ? 'Creating...' : 'Create Agent'}
          </button>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {agents?.map(agent => {
          const proc = agent.process_id ? processMap.get(agent.process_id) : undefined
          const isRunning = proc?.status === 'running'
          const isExpanded = expandedAgents.has(agent.name)
          const toggleExpand = () => {
            setExpandedAgents(prev => {
              const next = new Set(prev)
              if (next.has(agent.name)) next.delete(agent.name)
              else next.add(agent.name)
              return next
            })
          }
          const toolsExcludeDelegate = agent.tools?.filter(t => t !== 'delegate') ?? []
          const toolCount = toolsExcludeDelegate.length
          const teamCount = agent.team?.length ?? 0

          // Group tools by MCP server prefix (e.g. "synkedup__foo" → group "synkedup")
          const toolGroups = new Map<string, string[]>()
          for (const tool of toolsExcludeDelegate) {
            const sep = tool.indexOf('__')
            if (sep > 0) {
              const prefix = tool.substring(0, sep)
              const shortName = tool.substring(sep + 2)
              if (!toolGroups.has(prefix)) toolGroups.set(prefix, [])
              toolGroups.get(prefix)!.push(shortName)
            } else {
              if (!toolGroups.has('')) toolGroups.set('', [])
              toolGroups.get('')!.push(tool)
            }
          }

          return (
          <div key={agent.name} className="p-4 rounded-lg bg-card border border-border space-y-2">
            {/* Header: avatar + name + badges + action buttons */}
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <div className="flex-shrink-0 h-8 w-8 rounded-full bg-primary/20 text-primary flex items-center justify-center text-sm font-bold uppercase">
                  {agent.name[0]}
                </div>
                <h3 className="font-semibold">{agent.name}</h3>
                {agent.source === 'composed' && (
                  <span className="text-xs px-1.5 py-0.5 rounded bg-purple-900/50 text-purple-400">composed</span>
                )}
                {agent.process_status && (
                  <span className={`text-xs px-2 py-0.5 rounded ${
                    agent.process_status === 'running' ? 'bg-blue-900/50 text-blue-400' :
                    agent.process_status === 'completed' ? 'bg-green-900/50 text-green-400' :
                    'bg-muted text-muted-foreground'
                  }`}>
                    {agent.process_status}
                  </span>
                )}
                {isRunning && (
                  <span className="relative flex h-2 w-2">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75" />
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500" />
                  </span>
                )}
              </div>
              <div className="flex items-center gap-1.5">
                <button
                  onClick={() => navigate(`/chat/${agent.name}`)}
                  className="text-xs px-2.5 py-1 rounded bg-primary text-primary-foreground hover:bg-primary/90 transition-colors font-medium"
                  title={`Chat with ${agent.name}`}
                >
                  Chat
                </button>
                <button
                  onClick={() => navigate(`/files?agent=${encodeURIComponent(agent.name)}`)}
                  className="text-xs px-2 py-1 rounded bg-indigo-900/30 text-indigo-400 hover:bg-indigo-900/50 transition-colors"
                  title={`Files by ${agent.name}`}
                >
                  Files
                </button>
                {agent.source === 'composed' && (
                  <button
                    onClick={() => deleteAgent(agent.name)}
                    className="text-xs px-1.5 py-1 rounded bg-red-900/30 text-red-400 hover:bg-red-900/50 transition-colors"
                    title="Delete agent"
                  >
                    x
                  </button>
                )}
              </div>
            </div>

            {/* Model */}
            {agent.model && (
              <div className="font-mono text-xs text-muted-foreground pl-10">{agent.model}</div>
            )}

            {/* System prompt — compact, 2 lines */}
            {agent.system && (
              <p className="text-xs text-muted-foreground line-clamp-2 pl-10">{agent.system}</p>
            )}

            {/* Summary line + expand toggle */}
            <div className="flex items-center justify-between pl-10">
              <div className="text-xs text-muted-foreground">
                {toolCount > 0 && <span>{toolCount} tool{toolCount !== 1 ? 's' : ''}</span>}
                {toolCount > 0 && teamCount > 0 && <span className="mx-1.5">&middot;</span>}
                {teamCount > 0 && <span>{teamCount} team member{teamCount !== 1 ? 's' : ''}</span>}
                {toolCount === 0 && teamCount === 0 && agent.process_id && (
                  <span>PID {agent.process_id.substring(0, 8)}</span>
                )}
              </div>
              {(toolCount > 0 || teamCount > 0 || proc || agent.process_id) && (
                <button
                  onClick={toggleExpand}
                  className="text-xs text-muted-foreground hover:text-foreground transition-colors p-1"
                  title={isExpanded ? 'Collapse' : 'Expand details'}
                >
                  <svg
                    className={`h-4 w-4 transition-transform ${isExpanded ? 'rotate-180' : ''}`}
                    fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
                  >
                    <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                  </svg>
                </button>
              )}
            </div>

            {/* Expanded details */}
            {isExpanded && (
              <div className="space-y-3 pt-2 border-t border-border/50">
                {/* Live metrics */}
                {proc && (
                  <div className="grid grid-cols-3 gap-2 text-xs py-2 px-3 rounded bg-muted/50 border border-border/50">
                    <div>
                      <div className="text-muted-foreground">Iterations</div>
                      <div className="font-medium">{proc.metrics.iterations}</div>
                    </div>
                    <div>
                      <div className="text-muted-foreground">Tokens</div>
                      <div className="font-medium">{proc.metrics.input_tokens + proc.metrics.output_tokens}</div>
                    </div>
                    <div>
                      <div className="text-muted-foreground">Cost</div>
                      <div className="font-medium">${proc.metrics.cost_usd.toFixed(4)}</div>
                    </div>
                    <div>
                      <div className="text-muted-foreground">Tool Calls</div>
                      <div className="font-medium">{proc.metrics.tool_calls}</div>
                    </div>
                    {proc.metrics.last_active_at && (
                      <div className="col-span-2">
                        <div className="text-muted-foreground">Last Active</div>
                        <div className="font-medium">{new Date(proc.metrics.last_active_at).toLocaleTimeString()}</div>
                      </div>
                    )}
                  </div>
                )}

                {/* Team members */}
                {agent.team && agent.team.length > 0 && (
                  <div>
                    <span className="text-xs text-muted-foreground">Team: </span>
                    <div className="flex flex-wrap gap-1 mt-0.5">
                      {agent.team.map(member => (
                        <span key={member} className="text-xs px-2 py-0.5 rounded bg-green-900/30 text-green-400">
                          {member}
                        </span>
                      ))}
                    </div>
                  </div>
                )}

                {/* Tools — grouped by MCP server */}
                {toolCount > 0 && (
                  <div className="space-y-1.5">
                    <span className="text-xs text-muted-foreground">Tools:</span>
                    {Array.from(toolGroups.entries()).map(([prefix, tools]) => (
                      <div key={prefix || '_builtin'}>
                        {prefix && (
                          <div className="text-xs font-medium text-muted-foreground mt-1">{prefix} ({tools.length})</div>
                        )}
                        <div className="flex flex-wrap gap-1 mt-0.5">
                          {tools.map(tool => (
                            <span key={`${prefix}__${tool}`} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                              {tool}
                            </span>
                          ))}
                        </div>
                      </div>
                    ))}
                  </div>
                )}

                {/* PID */}
                {agent.process_id && (
                  <div className="text-xs text-muted-foreground">
                    PID: <span className="font-mono">{agent.process_id}</span>
                  </div>
                )}
              </div>
            )}
          </div>
          )
        })}
      </div>
    </div>
  )
}
