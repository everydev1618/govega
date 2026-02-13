import { NavLink, Outlet } from 'react-router-dom'

const nav = [
  { to: '/', label: 'Overview' },
  { to: '/chat', label: 'Chat' },
  { to: '/population', label: 'Population' },
  { to: '/agents', label: 'Agents' },
  { to: '/workflows', label: 'Workflows' },
  { to: '/processes', label: 'Processes' },
  { to: '/events', label: 'Events' },
  { to: '/spawn-tree', label: 'Spawn Tree' },
  { to: '/mcp', label: 'MCP' },
  { to: '/costs', label: 'Costs' },
]

export function Layout() {
  return (
    <div className="flex min-h-screen">
      {/* Sidebar */}
      <aside className="w-56 border-r border-border bg-card flex flex-col">
        <div className="p-4 border-b border-border">
          <h1 className="text-lg font-bold tracking-tight bg-gradient-to-r from-indigo-400 to-purple-400 bg-clip-text text-transparent">
            Vega
          </h1>
          <p className="text-xs text-muted-foreground">Agent Dashboard</p>
        </div>
        <nav className="flex-1 p-2 space-y-0.5">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) =>
                `block px-3 py-2 rounded-md text-sm transition-colors ${
                  isActive
                    ? 'bg-accent text-accent-foreground font-medium'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
                }`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>

      {/* Main content */}
      <main className="flex-1 p-6 overflow-auto">
        <Outlet />
      </main>
    </div>
  )
}
