import { useState, useEffect, useMemo } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { CompanySwitcher } from './CompanySwitcher'
import { PeeringPill } from './PeeringPill'
import { AgentAvatar } from './chat/AgentAvatar'
import { api } from '../lib/api'
import type { AgentResponse, Channel, InboxItem, ProcessResponse, TenantConfigResponse } from '../lib/types'

// Fallback names used until the tenant-config endpoint resolves on first
// load. After that, the live orchestrator/builder names from the server
// drive the sidebar identity (so renamed agents like "Charlie" show up
// in the orchestrator slot instead of falling through to the specialists
// list).
const DEFAULT_ORCHESTRATOR = 'iris'
const BUILDER_NAME = 'hera'

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

const adminNav = [
  { to: '/overview', label: 'Overview' },
  { to: '/agents', label: 'Agents' },
  { to: '/population', label: 'Population' },
  { to: '/workflows', label: 'Workflows' },
  { to: '/schedules', label: 'Schedules' },
  { to: '/processes', label: 'Processes' },
  { to: '/events', label: 'Events' },
  { to: '/spawn-tree', label: 'Spawn Tree' },
  { to: '/visualize', label: 'Visualize' },
  { to: '/connections', label: 'Connections' },
  { to: '/costs', label: 'Costs' },
  { to: '/settings', label: 'Settings' },
]

function SectionHeader({ children, action, collapsed, onToggle }: { children: React.ReactNode; action?: React.ReactNode; collapsed?: boolean; onToggle?: () => void }) {
  return (
    <div className="flex items-center justify-between px-3 pt-4 pb-1.5">
      <button
        onClick={onToggle}
        className="section-label flex items-center gap-1 hover:text-ink-soft transition-colors"
      >
        {onToggle && (
          <svg className={`w-2 h-2 transition-transform ${collapsed ? '' : 'rotate-90'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        )}
        {children}
      </button>
      {action}
    </div>
  )
}

export function Layout() {
  const location = useLocation()
  const [agents, setAgents] = useState<AgentResponse[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [inboxItems, setInboxItems] = useState<InboxItem[]>([])
  const [chatUnread, setChatUnread] = useState<Record<string, number>>({})
  const [runningTasks, setRunningTasks] = useState<ProcessResponse[]>([])
  // Set of base agent names that currently have a turn-of-work in progress.
  // 'status === running' on the agent record means alive (registered), not
  // actively inferring; processes endpoint is the truthful source.
  const [activeAgentNames, setActiveAgentNames] = useState<Set<string>>(new Set())
  const [moreOpen, setMoreOpen] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [dmCollapsed, setDmCollapsed] = useState(false)
  const [channelsCollapsed, setChannelsCollapsed] = useState(false)
  const [tenant, setTenant] = useState<TenantConfigResponse | null>(null)

  const fetchRunningTasks = () => {
    api.getProcesses().then(procs => {
      const running = (procs ?? []).filter(p => p.status === 'running')
      setRunningTasks(running.filter(p => p.task))
      // Strip per-user clone suffix ("charlie:42" → "charlie") so the
      // sidebar lights up for the base agent regardless of which user clone
      // is doing the work. "status===running" alone just means the process
      // is *registered* — use metrics.last_active_at within a 30s window as
      // the truthful "actively working right now" signal.
      const now = Date.now()
      const ACTIVE_WINDOW_MS = 30_000
      const active = new Set<string>()
      for (const p of running) {
        const lastActive = p.metrics?.last_active_at
          ? new Date(p.metrics.last_active_at).getTime()
          : 0
        if (!lastActive || now - lastActive > ACTIVE_WINDOW_MS) continue
        const colon = p.agent.indexOf(':')
        active.add(colon >= 0 ? p.agent.substring(0, colon) : p.agent)
      }
      setActiveAgentNames(active)
    }).catch(() => {})
  }

  useEffect(() => {
    api.getAgents().then(list => setAgents(list ?? [])).catch(() => {})
    api.getChannels().then(list => setChannels(list ?? [])).catch(() => {})
    api.getInbox('pending').then(list => setInboxItems(list ?? [])).catch(() => {})
    api.chatUnreadCounts().then(counts => setChatUnread(counts ?? {})).catch(() => {})
    api.getTenantConfig().then(cfg => setTenant(cfg)).catch(() => {})
    fetchRunningTasks()
  }, [])

  // Refresh agents, channels, inbox, unread counts, and running tasks periodically
  useEffect(() => {
    const id = setInterval(() => {
      api.getAgents().then(list => setAgents(list ?? [])).catch(() => {})
      api.getChannels().then(list => setChannels(list ?? [])).catch(() => {})
      api.getInbox('pending').then(list => setInboxItems(list ?? [])).catch(() => {})
      api.chatUnreadCounts().then(counts => setChatUnread(counts ?? {})).catch(() => {})
      fetchRunningTasks()
    }, 5000)
    return () => clearInterval(id)
  }, [])

  const orchestratorName = tenant?.orchestrator_name || DEFAULT_ORCHESTRATOR
  const orchestratorDisplay = tenant?.orchestrator_display || 'Iris'
  const metaAgentNames = useMemo(
    () => new Set([orchestratorName.toLowerCase(), BUILDER_NAME]),
    [orchestratorName]
  )
  const orchestratorAgent = agents.find(a => a.name.toLowerCase() === orchestratorName.toLowerCase())
  const specialists = useMemo(() =>
    agents
      .filter(a => !metaAgentNames.has(a.name.toLowerCase()))
      .sort((a, b) => (a.display_name || a.name).localeCompare(b.display_name || b.name)),
    [agents, metaAgentNames]
  )

  const inboxCount = inboxItems.length

  // Check if current path is an admin page
  const isAdminPage = adminNav.some(item => location.pathname.startsWith(item.to))
  // Auto-expand More when on an admin page
  const showMore = moreOpen || isAdminPage

  // Close sidebar on navigation (mobile)
  useEffect(() => {
    setSidebarOpen(false)
  }, [location.pathname])

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Mobile header */}
      <div className="fixed top-0 left-0 right-0 z-40 flex items-center gap-3 px-3 py-2 border-b border-rule bg-paper-deep md:hidden">
        <button
          onClick={() => setSidebarOpen(true)}
          className="p-1.5 rounded-sm text-ink-soft hover:text-ink hover:bg-paper transition-colors"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
          </svg>
        </button>
        <CompanySwitcher />
      </div>

      {/* Sidebar backdrop (mobile) */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-ink/30 md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside className={`fixed inset-y-0 left-0 z-50 w-56 border-r border-rule bg-paper-deep flex flex-col transform transition-transform duration-200 ease-out md:static md:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="p-3 border-b border-rule flex items-center justify-between gap-2">
          <CompanySwitcher />
          <PeeringPill />
          <button
            onClick={() => setSidebarOpen(false)}
            className="p-1 rounded-sm text-ink-soft hover:text-ink md:hidden"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <nav className="flex-1 overflow-y-auto p-1">
          {/* Direct Messages */}
          <SectionHeader collapsed={dmCollapsed} onToggle={() => setDmCollapsed(v => !v)}>Direct Messages</SectionHeader>
          {!dmCollapsed && (
            <div className="space-y-0.5">
              {/* Orchestrator (default: Iris; may be renamed via tenant config) always first */}
              {orchestratorAgent && (
                <AgentNavItem
                  agent={orchestratorAgent}
                  to={`/chat/${orchestratorAgent.name}`}
                  displayName={orchestratorAgent.display_name || orchestratorDisplay}
                  avatar={orchestratorAgent.avatar || 'n2'}
                  unreadCount={chatUnread[orchestratorAgent.name] || 0}
                  busy={Boolean(orchestratorAgent.streaming) || activeAgentNames.has(orchestratorAgent.name)}
                />
              )}
              {specialists.length > 0 && orchestratorAgent && (
                <div className="mx-3 my-1.5 border-t border-rule/60" />
              )}
              {specialists.map(a => (
                <AgentNavItem
                  key={a.name}
                  agent={a}
                  to={`/chat/${a.name}`}
                  displayName={a.display_name || capitalize(a.name)}
                  avatar={a.avatar}
                  unreadCount={chatUnread[a.name] || 0}
                  busy={a.streaming || activeAgentNames.has(a.name)}
                />
              ))}
            </div>
          )}

          {/* Channels */}
          <SectionHeader
            collapsed={channelsCollapsed}
            onToggle={() => setChannelsCollapsed(v => !v)}
            action={
              <NavLink
                to="/channels/new"
                className="text-muted-foreground/70 hover:text-foreground transition-colors"
                title="Create channel"
              >
                <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
                </svg>
              </NavLink>
            }
          >
            Channels
          </SectionHeader>
          {!channelsCollapsed && (
            <div className="space-y-0.5">
              {channels.map(ch => (
                <NavLink
                  key={ch.name}
                  to={`/channels/${ch.name}`}
                  className={({ isActive }) =>
                    `nav-item justify-between ${isActive ? 'nav-item-active' : ''}`
                  }
                >
                  <span className="truncate"><span className="text-ink-faint">#</span> {ch.name}</span>
                  {ch.unread_count > 0 && (
                    <span className="text-2xs font-mono font-medium text-brand tnum flex-shrink-0 ml-1">
                      {ch.unread_count}
                    </span>
                  )}
                </NavLink>
              ))}
              {channels.length === 0 && (
                <p className="px-3 py-1 text-xs text-ink-faint">No channels yet</p>
              )}
            </div>
          )}

          {/* Activity — running tasks */}
          {runningTasks.length > 0 && (
            <div className="mt-2">
              <SectionHeader>
                Activity
              </SectionHeader>
              <div className="space-y-0.5">
                {runningTasks.map(proc => {
                  const agentBase = proc.agent.indexOf(':') >= 0 ? proc.agent.substring(0, proc.agent.indexOf(':')) : proc.agent
                  const agentData = agents.find(a => a.name === agentBase || a.name === proc.agent)
                  const displayName = agentData?.display_name || capitalize(agentBase)
                  const task = proc.task && proc.task.length > 60 ? proc.task.substring(0, 60) + '...' : proc.task
                  const elapsed = Math.round((Date.now() - new Date(proc.started_at).getTime()) / 1000)
                  const elapsedStr = elapsed < 60 ? `${elapsed}s` : `${Math.floor(elapsed / 60)}m`
                  return (
                    <div key={proc.id} className="px-3 py-1.5">
                      <div className="flex items-center gap-1.5">
                        <span className="status-dot status-dot-running live-pulse flex-shrink-0" />
                        <span className="text-xs font-medium text-ink truncate">{displayName}</span>
                        <span className="text-2xs font-mono text-ink-faint tnum flex-shrink-0">{elapsedStr}</span>
                      </div>
                      {task && (
                        <p className="text-2xs text-ink-soft truncate mt-0.5 ml-3.5">{task}</p>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          )}

          {/* Inbox */}
          <div className="mt-2">
            <NavLink
              to="/inbox"
              className={({ isActive }) =>
                `nav-item justify-between ${isActive ? 'nav-item-active' : ''}`
              }
            >
              <div className="flex items-center gap-2">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859M12 3v8.25m0 0l-3-3m3 3l3-3" />
                </svg>
                <span>Inbox</span>
              </div>
              {inboxCount > 0 && (
                <span className="text-2xs font-mono font-medium text-brand tnum">
                  {inboxCount}
                </span>
              )}
            </NavLink>
          </div>

          {/* Tasks */}
          <div>
            <NavLink
              to="/tasks"
              className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4" />
              </svg>
              <span>Tasks</span>
            </NavLink>
          </div>

          {/* Memory */}
          <div>
            <NavLink
              to="/memory"
              className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
              <span>Memory</span>
            </NavLink>
          </div>

          {/* Files */}
          <div>
            <NavLink
              to="/files"
              className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z" />
              </svg>
              <span>Files</span>
            </NavLink>
          </div>

          {/* More (Admin) */}
          <div className="mt-3 border-t border-rule/60 pt-2">
            <button
              onClick={() => setMoreOpen(v => !v)}
              className="section-label flex items-center gap-1 px-3 py-1.5 w-full text-left hover:text-ink-soft transition-colors"
            >
              <svg className={`w-2 h-2 transition-transform ${showMore ? 'rotate-90' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
              </svg>
              <span>More</span>
            </button>
            {showMore && (
              <div className="space-y-0.5 mt-0.5">
                {adminNav.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    className={({ isActive }) => `nav-item ${isActive ? 'nav-item-active' : ''}`}
                  >
                    {item.label}
                  </NavLink>
                ))}
              </div>
            )}
          </div>
        </nav>
      </aside>

      {/* Main content */}
      <div className="flex-1 flex flex-col min-h-0 overflow-hidden relative">
        <main className="flex-1 p-3 pt-14 md:p-6 md:pt-6 overflow-auto flex flex-col min-h-0">
          <Outlet />
        </main>
        {location.pathname !== '/visualize' && (
          <VisualizeButton running={runningTasks.length} />
        )}
      </div>
    </div>
  )
}

function VisualizeButton({ running }: { running: number }) {
  const navigate = useNavigate()
  return (
    <button
      onClick={() => navigate('/visualize')}
      className="absolute bottom-5 right-5 z-30 group flex items-center gap-2 pl-2.5 pr-3 py-2 rounded-sm border border-rule bg-paper text-ink-soft hover:text-ink hover:border-brand transition-colors"
      title="Supervision tree"
    >
      {/* Tree icon */}
      <svg className="w-4 h-4" width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
        <circle cx="9" cy="4" r="2" />
        <circle cx="4" cy="13" r="2" />
        <circle cx="14" cy="13" r="2" />
        <line x1="7.5" y1="5.5" x2="5.5" y2="11.5" />
        <line x1="10.5" y1="5.5" x2="12.5" y2="11.5" />
        <line x1="6" y1="13" x2="12" y2="13" />
      </svg>
      <span className="text-xs font-medium">tree</span>
      {running > 0 && (
        <span className="status-dot status-dot-running live-pulse" />
      )}
    </button>
  )
}

function AgentNavItem({
  agent,
  to,
  displayName,
  avatar,
  unreadCount = 0,
  busy = false,
}: {
  agent: AgentResponse
  to: string
  displayName: string
  avatar?: string
  unreadCount?: number
  busy?: boolean
}) {
  return (
    <NavLink
      to={to}
      end={to === '/chat'}
      className={({ isActive }) =>
        `nav-item ${isActive ? 'nav-item-active' : ''}`
      }
    >
      <div className="relative flex-shrink-0 flex items-center justify-center" style={{ width: 24, height: 24 }}>
        <AgentAvatar name={agent.name} displayName={displayName} avatar={avatar} size={5} />
        <span
          className={`absolute -bottom-0.5 -right-0.5 w-2 h-2 rounded-full border border-paper-deep ${
            busy
              ? 'bg-running live-pulse'
              : agent.status === 'running'
                ? 'bg-running'
                : 'bg-ink-faint opacity-40'
          }`}
          aria-label={busy ? 'working' : agent.status === 'running' ? 'live' : 'idle'}
        />
      </div>
      <div className="min-w-0 flex-1">
        <span className="block truncate">{displayName}</span>
        {agent.title && <span className="block truncate text-2xs text-ink-faint leading-tight">{agent.title}</span>}
      </div>
      {unreadCount > 0 && (
        <span className="text-2xs font-mono font-medium text-brand tnum flex-shrink-0">
          {unreadCount}
        </span>
      )}
    </NavLink>
  )
}
