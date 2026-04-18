import { useState, useEffect, useRef } from 'react'
import { AgentAvatar } from './chat/AgentAvatar'
import { api } from '../lib/api'
import type { AgentResponse } from '../lib/types'

interface ActivityItem {
  agent: string
  displayName: string
  avatar?: string
  task?: string
  startedAt: number
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  const secs = Math.round((now - since) / 1000)
  const str = secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m ${secs % 60}s`
  return <span>{str}</span>
}

export function ActivityBar({ agents }: { agents: AgentResponse[] }) {
  const [items, setItems] = useState<ActivityItem[]>([])
  const agentsRef = useRef(agents)
  agentsRef.current = agents

  // Poll for running processes with tasks or dispatched agents
  useEffect(() => {
    const refresh = () => {
      api.getProcesses().then(procs => {
        if (!procs) return
        const running = procs.filter(p => p.status === 'running' && p.task)
        const agentsList = agentsRef.current
        setItems(running.map(p => {
          const agentBase = p.agent.includes(':') ? p.agent.substring(0, p.agent.indexOf(':')) : p.agent
          const agentData = agentsList.find(a => a.name === agentBase || a.name === p.agent)
          return {
            agent: p.agent,
            displayName: agentData?.display_name || capitalize(agentBase),
            avatar: agentData?.avatar,
            task: p.task,
            startedAt: new Date(p.started_at).getTime(),
          }
        }))
      }).catch(() => {})
    }
    refresh()
    const id = setInterval(refresh, 2000)
    return () => clearInterval(id)
  }, [])

  // Also show agents that are streaming (busy) but may not have a "task"
  const streamingAgents = agents.filter(a =>
    a.streaming && !items.some(item => item.agent === a.name || item.agent.startsWith(a.name + ':'))
  )

  const allItems: ActivityItem[] = [
    ...items,
    ...streamingAgents.map(a => ({
      agent: a.name,
      displayName: a.display_name || capitalize(a.name),
      avatar: a.avatar,
      task: undefined,
      startedAt: Date.now(),
    })),
  ]

  if (allItems.length === 0) return null

  return (
    <div className="flex items-center gap-3 px-4 py-1.5 bg-amber-500/5 border-b border-amber-500/10 overflow-x-auto flex-shrink-0 activity-bar-animate-in">
      <div className="flex items-center gap-1.5 flex-shrink-0">
        <span className="relative flex h-2 w-2">
          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75" />
          <span className="relative inline-flex rounded-full h-2 w-2 bg-amber-400" />
        </span>
        <span className="text-[11px] font-medium text-amber-400/90 uppercase tracking-wider">Working</span>
      </div>
      <div className="h-3 w-px bg-border/50 flex-shrink-0" />
      <div className="flex items-center gap-3 overflow-x-auto">
        {allItems.map(item => (
          <div key={item.agent} className="flex items-center gap-2 flex-shrink-0 activity-item-animate-in">
            <div className="relative flex-shrink-0" style={{ width: 20, height: 20 }}>
              <AgentAvatar name={item.agent} displayName={item.displayName} avatar={item.avatar} size={4} />
              <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
                <span className="agent-orbit-dot absolute w-1 h-1 rounded-full bg-amber-400 shadow-[0_0_3px_rgba(251,191,36,0.6)]" />
              </div>
            </div>
            <div className="flex items-center gap-1.5 min-w-0">
              <span className="text-xs font-medium text-foreground whitespace-nowrap">{item.displayName}</span>
              {item.task && (
                <span className="text-[11px] text-muted-foreground/60 truncate max-w-[200px]">{item.task}</span>
              )}
              <span className="text-[10px] text-muted-foreground/40 whitespace-nowrap tabular-nums">
                <Elapsed since={item.startedAt} />
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
