import { NavLink, Outlet } from 'react-router-dom'

const primaryNav = [
  { to: '/', label: 'Chat' },
  { to: '/agents', label: 'Agents' },
]

const secondaryNav = [
  { to: '/overview', label: 'Overview' },
  { to: '/population', label: 'Population' },
  { to: '/workflows', label: 'Workflows' },
  { to: '/tasks', label: 'Tasks' },
  { to: '/files', label: 'Files' },
  { to: '/events', label: 'Events' },
  { to: '/spawn-tree', label: 'Spawn Tree' },
  { to: '/connections', label: 'Connections' },
  { to: '/costs', label: 'Costs' },
]

function NavItem({ to, label }: { to: string; label: string }) {
  return (
    <NavLink
      to={to}
      end={to === '/'}
      className={({ isActive }) =>
        `block px-3 py-2 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-accent text-accent-foreground font-medium'
            : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
        }`
      }
    >
      {label}
    </NavLink>
  )
}

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
        <nav className="flex-1 p-2">
          <div className="space-y-0.5">
            {primaryNav.map((item) => (
              <NavItem key={item.to} {...item} />
            ))}
          </div>
          <div className="my-2 border-t border-border" />
          <div className="space-y-0.5">
            {secondaryNav.map((item) => (
              <NavItem key={item.to} {...item} />
            ))}
          </div>
        </nav>
      </aside>

      {/* Main content */}
      <main className="flex-1 p-6 overflow-auto">
        <Outlet />
      </main>
    </div>
  )
}
