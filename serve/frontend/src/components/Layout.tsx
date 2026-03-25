import { useState, useEffect, useMemo } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { CompanySwitcher } from './CompanySwitcher'
import { AgentAvatar } from './chat/AgentAvatar'
import { UserIdentityPrompt, getUserName } from './UserIdentityPrompt'
import { api } from '../lib/api'
import type { AgentResponse, Channel, InboxItem, ProcessResponse } from '../lib/types'

const HERMES = 'hermes'
const META_AGENTS = new Set(['hermes', 'mother'])

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

const adminNav = [
  { to: '/overview', label: 'Overview' },
  { to: '/agents', label: 'Agents' },
  { to: '/population', label: 'Population' },
  { to: '/workflows', label: 'Workflows' },
  { to: '/schedules', label: 'Schedules' },
  { to: '/tasks', label: 'Tasks' },
  { to: '/processes', label: 'Processes' },
  { to: '/events', label: 'Events' },
  { to: '/spawn-tree', label: 'Spawn Tree' },
  { to: '/connections', label: 'Connections' },
  { to: '/costs', label: 'Costs' },
  { to: '/settings', label: 'Settings' },
]

function SectionHeader({ children, action, collapsed, onToggle }: { children: React.ReactNode; action?: React.ReactNode; collapsed?: boolean; onToggle?: () => void }) {
  return (
    <div className="flex items-center justify-between px-3 pt-3 pb-1">
      <button
        onClick={onToggle}
        className="flex items-center gap-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70 hover:text-muted-foreground transition-colors"
      >
        {onToggle && (
          <svg className={`w-2.5 h-2.5 transition-transform ${collapsed ? '' : 'rotate-90'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
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
  const [userName, setUserNameState] = useState<string | null>(getUserName())
  const [agents, setAgents] = useState<AgentResponse[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [inboxItems, setInboxItems] = useState<InboxItem[]>([])
  const [chatUnread, setChatUnread] = useState<Record<string, number>>({})
  const [runningTasks, setRunningTasks] = useState<ProcessResponse[]>([])
  const [busyAgents, setBusyAgents] = useState<Set<string>>(new Set())
  const [moreOpen, setMoreOpen] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [dmCollapsed, setDmCollapsed] = useState(false)
  const [channelsCollapsed, setChannelsCollapsed] = useState(false)

  const fetchRunningTasks = () => {
    api.getProcesses().then(procs => {
      const running = (procs ?? []).filter(p => p.status === 'running')
      setRunningTasks(running.filter(p => p.task))
      // Build busy set from ALL running processes, not just ones with task descriptions.
      const busy = new Set<string>()
      for (const p of running) {
        const base = p.agent.indexOf(':') >= 0 ? p.agent.substring(0, p.agent.indexOf(':')) : p.agent
        busy.add(base)
      }
      setBusyAgents(busy)
    }).catch(() => {})
  }

  useEffect(() => {
    api.getAgents().then(list => setAgents(list ?? [])).catch(() => {})
    api.getChannels().then(list => setChannels(list ?? [])).catch(() => {})
    api.getInbox('pending').then(list => setInboxItems(list ?? [])).catch(() => {})
    api.chatUnreadCounts().then(counts => setChatUnread(counts ?? {})).catch(() => {})
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

  const hermesAgent = agents.find(a => a.name === HERMES)
  const specialists = useMemo(() =>
    agents
      .filter(a => !META_AGENTS.has(a.name))
      .sort((a, b) => (a.display_name || a.name).localeCompare(b.display_name || b.name)),
    [agents]
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

  if (!userName) {
    return <UserIdentityPrompt onComplete={setUserNameState} />
  }

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Mobile header */}
      <div className="fixed top-0 left-0 right-0 z-40 flex items-center gap-3 px-3 py-2 border-b border-border bg-card md:hidden">
        <button
          onClick={() => setSidebarOpen(true)}
          className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
          </svg>
        </button>
        <CompanySwitcher />
      </div>

      {/* Sidebar backdrop (mobile) */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/50 md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside className={`fixed inset-y-0 left-0 z-50 w-56 border-r border-border bg-card flex flex-col transform transition-transform duration-200 ease-in-out md:static md:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="p-3 border-b border-border flex items-center justify-between">
          <CompanySwitcher />
          <button
            onClick={() => setSidebarOpen(false)}
            className="p-1 rounded-md text-muted-foreground hover:text-foreground md:hidden"
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
              {/* Hermes always first */}
              {hermesAgent && (
                <AgentNavItem
                  agent={hermesAgent}
                  to={'/chat'}
                  displayName="Hermes"
                  avatar="n2"
                  unreadCount={chatUnread[hermesAgent.name] || 0}
                  busy={busyAgents.has('hermes')}
                />
              )}
              {specialists.length > 0 && hermesAgent && (
                <div className="mx-3 my-1 border-t border-border/50" />
              )}
              {specialists.map(a => (
                <AgentNavItem
                  key={a.name}
                  agent={a}
                  to={`/chat/${a.name}`}
                  displayName={a.display_name || capitalize(a.name)}
                  avatar={a.avatar}
                  unreadCount={chatUnread[a.name] || 0}
                  busy={busyAgents.has(a.name)}
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
                    `flex items-center justify-between px-3 py-1.5 rounded-md text-sm transition-colors ${
                      isActive
                        ? 'bg-accent text-accent-foreground font-medium'
                        : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
                    }`
                  }
                >
                  <span className="truncate"># {ch.name}</span>
                  {ch.unread_count > 0 && (
                    <span className="text-[10px] bg-primary/20 text-primary rounded-full px-1.5 py-0.5 font-medium leading-none flex-shrink-0 ml-1">
                      {ch.unread_count}
                    </span>
                  )}
                </NavLink>
              ))}
              {channels.length === 0 && (
                <p className="px-3 py-1 text-xs text-muted-foreground/50">No channels yet</p>
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
                    <div key={proc.id} className="px-3 py-1.5 rounded-md">
                      <div className="flex items-center gap-1.5">
                        <span className="relative flex h-2 w-2 flex-shrink-0">
                          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75" />
                          <span className="relative inline-flex rounded-full h-2 w-2 bg-amber-400" />
                        </span>
                        <span className="text-xs font-medium text-foreground truncate">{displayName}</span>
                        <span className="text-[10px] text-muted-foreground/60 flex-shrink-0">{elapsedStr}</span>
                      </div>
                      {task && (
                        <p className="text-[11px] text-muted-foreground/70 truncate mt-0.5 ml-3.5">{task}</p>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          )}

          {/* Inbox */}
          <div className="mt-1">
            <NavLink
              to="/inbox"
              className={({ isActive }) =>
                `flex items-center justify-between px-3 py-2 rounded-md text-sm transition-colors ${
                  isActive
                    ? 'bg-accent text-accent-foreground font-medium'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
                }`
              }
            >
              <div className="flex items-center gap-2">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859M12 3v8.25m0 0l-3-3m3 3l3-3" />
                </svg>
                <span>Inbox</span>
              </div>
              {inboxCount > 0 && (
                <span className="text-[10px] bg-red-500/20 text-red-400 rounded-full px-1.5 py-0.5 font-medium leading-none">
                  {inboxCount}
                </span>
              )}
            </NavLink>
          </div>

          {/* Files */}
          <div className="mt-1">
            <NavLink
              to="/files"
              className={({ isActive }) =>
                `flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors ${
                  isActive
                    ? 'bg-accent text-accent-foreground font-medium'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
                }`
              }
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z" />
              </svg>
              <span>Files</span>
            </NavLink>
          </div>

          {/* More (Admin) */}
          <div className="mt-2">
            <button
              onClick={() => setMoreOpen(v => !v)}
              className="flex items-center gap-1.5 px-3 py-1.5 w-full text-left text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              <svg className={`w-3 h-3 transition-transform ${showMore ? 'rotate-90' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
              </svg>
              <span className="text-[11px] font-semibold uppercase tracking-wider">More</span>
            </button>
            {showMore && (
              <div className="space-y-0.5 mt-0.5">
                {adminNav.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    className={({ isActive }) =>
                      `block px-3 py-1.5 rounded-md text-sm transition-colors ${
                        isActive
                          ? 'bg-accent text-accent-foreground font-medium'
                          : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
                      }`
                    }
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
      <main className="flex-1 p-3 pt-14 md:p-6 md:pt-6 overflow-auto flex flex-col min-h-0">
        <Outlet />
      </main>
    </div>
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
        `flex items-center gap-2 px-3 py-1.5 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-accent text-accent-foreground font-medium'
            : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
        }`
      }
    >
      <div className="relative flex-shrink-0 flex items-center justify-center" style={{ width: 24, height: 24 }}>
        <AgentAvatar name={agent.name} displayName={displayName} avatar={avatar} size={5} />
        {busy ? (
          <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
            <span className="agent-orbit-dot absolute w-1.5 h-1.5 rounded-full bg-amber-400 shadow-[0_0_4px_rgba(251,191,36,0.6)]" />
          </div>
        ) : (
          <span className={`absolute -bottom-0.5 -right-0.5 w-2 h-2 rounded-full border border-card ${
            agent.process_status === 'running' ? 'bg-green-400' : 'bg-muted-foreground/30'
          }`} />
        )}
      </div>
      <div className="min-w-0 flex-1">
        <span className="block truncate">{displayName}</span>
        {agent.title && <span className="block truncate text-[11px] text-muted-foreground/60 leading-tight">{agent.title}</span>}
      </div>
      {unreadCount > 0 && (
        <span className="text-[10px] bg-primary/20 text-primary rounded-full px-1.5 py-0.5 font-medium leading-none flex-shrink-0">
          {unreadCount}
        </span>
      )}
    </NavLink>
  )
}
