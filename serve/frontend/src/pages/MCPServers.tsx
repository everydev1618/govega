import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'

export function MCPServers() {
  const { data: servers, loading } = useAPI(() => api.getMCPServers())

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">MCP Servers</h2>

      {!servers || servers.length === 0 ? (
        <p className="text-muted-foreground text-sm">No MCP servers configured.</p>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {servers.map(server => (
            <div key={server.name} className="p-4 rounded-lg bg-card border border-border space-y-3">
              <div className="flex items-center justify-between">
                <h3 className="font-semibold">{server.name}</h3>
                <span className={`text-xs px-2 py-0.5 rounded ${
                  server.connected ? 'bg-green-900/50 text-green-400' : 'bg-red-900/50 text-red-400'
                }`}>
                  {server.connected ? 'Connected' : 'Disconnected'}
                </span>
              </div>

              <div className="text-sm space-y-1">
                {server.transport && (
                  <div><span className="text-muted-foreground">Transport: </span>{server.transport || 'stdio'}</div>
                )}
                {server.url && (
                  <div><span className="text-muted-foreground">URL: </span><span className="font-mono text-xs">{server.url}</span></div>
                )}
                {server.command && (
                  <div><span className="text-muted-foreground">Command: </span><span className="font-mono text-xs">{server.command}</span></div>
                )}
              </div>

              {server.tools && server.tools.length > 0 && (
                <div>
                  <p className="text-xs text-muted-foreground mb-1">Tools ({server.tools.length})</p>
                  <div className="flex flex-wrap gap-1">
                    {server.tools.map(tool => (
                      <span key={tool} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                        {tool}
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
