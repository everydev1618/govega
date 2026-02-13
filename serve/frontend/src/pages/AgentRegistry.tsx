import { useState, useEffect } from 'react'
import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'
import type { PopulationInstalledItem, CreateAgentRequest } from '../lib/types'

export function AgentRegistry() {
  const { data: agents, loading, refetch } = useAPI(() => api.getAgents())
  const [showComposer, setShowComposer] = useState(false)
  const [personas, setPersonas] = useState<PopulationInstalledItem[]>([])
  const [skills, setSkills] = useState<PopulationInstalledItem[]>([])
  const [form, setForm] = useState<CreateAgentRequest>({ name: '', model: 'claude-sonnet-4-20250514' })
  const [systemPreview, setSystemPreview] = useState('')
  const [selectedSkills, setSelectedSkills] = useState<string[]>([])
  const [selectedTeam, setSelectedTeam] = useState<string[]>([])
  const [creating, setCreating] = useState(false)

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
        {agents?.map(agent => (
          <div key={agent.name} className="p-4 rounded-lg bg-card border border-border space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <h3 className="font-semibold">{agent.name}</h3>
                {agent.source === 'composed' && (
                  <span className="text-xs px-1.5 py-0.5 rounded bg-purple-900/50 text-purple-400">composed</span>
                )}
              </div>
              <div className="flex items-center gap-1.5">
                {agent.process_status && (
                  <span className={`text-xs px-2 py-0.5 rounded ${
                    agent.process_status === 'running' ? 'bg-blue-900/50 text-blue-400' :
                    agent.process_status === 'completed' ? 'bg-green-900/50 text-green-400' :
                    'bg-muted text-muted-foreground'
                  }`}>
                    {agent.process_status}
                  </span>
                )}
                {agent.source === 'composed' && (
                  <button
                    onClick={() => deleteAgent(agent.name)}
                    className="text-xs px-1.5 py-0.5 rounded bg-red-900/30 text-red-400 hover:bg-red-900/50 transition-colors"
                    title="Delete agent"
                  >
                    x
                  </button>
                )}
              </div>
            </div>

            {agent.model && (
              <div className="text-sm">
                <span className="text-muted-foreground">Model: </span>
                <span className="font-mono text-xs">{agent.model}</span>
              </div>
            )}

            {agent.system && (
              <p className="text-xs text-muted-foreground line-clamp-3">{agent.system}</p>
            )}

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

            {agent.tools && agent.tools.length > 0 && (
              <div className="flex flex-wrap gap-1">
                {agent.tools.filter(t => t !== 'delegate').map(tool => (
                  <span key={tool} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                    {tool}
                  </span>
                ))}
              </div>
            )}

            {agent.process_id && (
              <div className="text-xs text-muted-foreground">
                PID: <span className="font-mono">{agent.process_id}</span>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
