import { useState, useCallback } from 'react'
import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'
import type { MCPRegistryEntry, MCPServerResponse, ConnectMCPRequest, Setting } from '../lib/types'

type Section = 'connected' | 'catalog' | 'custom' | 'settings'

export function Connections() {
  const { data: servers, refetch: refetchServers } = useAPI(() => api.getMCPServers())
  const { data: registry, refetch: refetchRegistry } = useAPI(() => api.getMCPRegistry())
  const { data: settings, refetch: refetchSettings } = useAPI(() => api.getSettings())

  const [expandedSections, setExpandedSections] = useState<Set<Section>>(
    new Set(['connected', 'catalog'])
  )
  const [connectingServer, setConnectingServer] = useState<string | null>(null)
  const [setupEntry, setSetupEntry] = useState<MCPRegistryEntry | null>(null)
  const [envValues, setEnvValues] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [disconnecting, setDisconnecting] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState<string | null>(null)

  // Expanded tool lists per server.
  const [expandedTools, setExpandedTools] = useState<Set<string>>(new Set())

  const toggleSection = (s: Section) => {
    setExpandedSections(prev => {
      const next = new Set(prev)
      if (next.has(s)) next.delete(s)
      else next.add(s)
      return next
    })
  }

  const toggleTools = (name: string) => {
    setExpandedTools(prev => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const refetchAll = useCallback(() => {
    refetchServers()
    refetchRegistry()
  }, [refetchServers, refetchRegistry])

  const handleConnect = async (entry: MCPRegistryEntry) => {
    const allEnv = [...(entry.required_env || []), ...(entry.optional_env || [])]
    if (allEnv.length > 0) {
      setSetupEntry(entry)
      setEnvValues({})
      setError(null)
      return
    }
    // No env needed — connect directly.
    await doConnect({ name: entry.name })
  }

  const doConnect = async (req: ConnectMCPRequest) => {
    setConnectingServer(req.name)
    setError(null)
    try {
      const res = await api.connectMCPServer(req)
      if (res.error) {
        setError(res.error)
      } else {
        setSetupEntry(null)
        setEnvValues({})
        refetchAll()
      }
    } catch (err: any) {
      setError(err.message || 'Failed to connect')
    } finally {
      setConnectingServer(null)
    }
  }

  const handleDisconnect = async (name: string) => {
    setDisconnecting(name)
    try {
      await api.disconnectMCPServer(name)
      refetchAll()
    } catch (err: any) {
      setError(err.message || 'Failed to disconnect')
    } finally {
      setDisconnecting(null)
    }
  }

  const handleRefresh = async (name: string) => {
    setRefreshing(name)
    setError(null)
    try {
      const res = await api.refreshMCPServer(name)
      if (res.error) {
        setError(res.error)
      } else {
        refetchAll()
      }
    } catch (err: any) {
      setError(err.message || 'Failed to refresh')
    } finally {
      setRefreshing(null)
    }
  }

  const handleSetupSubmit = () => {
    if (!setupEntry) return
    const env: Record<string, string> = {}
    for (const [k, v] of Object.entries(envValues)) {
      if (v.trim()) env[k] = v.trim()
    }
    doConnect({ name: setupEntry.name, env })
  }

  // --- Custom server state ---
  const [customName, setCustomName] = useState('')
  const [customTransport, setCustomTransport] = useState('stdio')
  const [customCommand, setCustomCommand] = useState('')
  const [customArgs, setCustomArgs] = useState('')
  const [customURL, setCustomURL] = useState('')

  const handleCustomConnect = () => {
    if (!customName.trim()) return
    const req: ConnectMCPRequest = { name: customName.trim(), transport: customTransport }
    if (customTransport === 'stdio') {
      req.command = customCommand
      req.args = customArgs.split(/\s+/).filter(Boolean)
    } else {
      req.url = customURL
    }
    doConnect(req)
  }

  // --- Settings state ---
  const [settingKey, setSettingKey] = useState('')
  const [settingValue, setSettingValue] = useState('')
  const [settingSensitive, setSettingSensitive] = useState(false)
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const handleSaveSetting = async () => {
    if (!settingKey.trim()) return
    setSaving(true)
    try {
      await api.upsertSetting(settingKey.trim(), settingValue, settingSensitive)
      setSettingKey('')
      setSettingValue('')
      setSettingSensitive(false)
      setEditingKey(null)
      refetchSettings()
      refetchRegistry()
    } catch (err: any) {
      setError(err.message || 'Failed to save setting')
    } finally {
      setSaving(false)
    }
  }

  const handleDeleteSetting = async (key: string) => {
    try {
      await api.deleteSetting(key)
      refetchSettings()
      refetchRegistry()
    } catch (err: any) {
      setError(err.message || 'Failed to delete setting')
    }
  }

  const handleEditSetting = (s: Setting) => {
    setSettingKey(s.key)
    setSettingValue(s.sensitive ? '' : s.value)
    setSettingSensitive(s.sensitive)
    setEditingKey(s.key)
    if (!expandedSections.has('settings')) {
      setExpandedSections(prev => new Set([...prev, 'settings']))
    }
  }

  const connectedNames = new Set(servers?.filter(s => s.connected).map(s => s.name) || [])

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-bold">Connections</h2>
        <p className="text-sm text-muted-foreground">
          Connect MCP servers to give your agents access to external tools and services.
        </p>
      </div>

      {error && (
        <div className="p-3 rounded-lg bg-red-900/30 text-red-400 text-sm flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="text-red-400 hover:text-red-300 ml-4">
            &times;
          </button>
        </div>
      )}

      {/* === Connected Servers === */}
      <SectionHeader
        title="Connected Servers"
        count={servers?.filter(s => s.connected).length || 0}
        expanded={expandedSections.has('connected')}
        onToggle={() => toggleSection('connected')}
      />
      {expandedSections.has('connected') && (
        <div>
          {!servers || servers.filter(s => s.connected).length === 0 ? (
            <p className="text-muted-foreground text-sm pl-1">No servers connected. Browse the catalog below to get started.</p>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {servers.filter(s => s.connected).map(server => (
                <ServerCard
                  key={server.name}
                  server={server}
                  expanded={expandedTools.has(server.name)}
                  onToggleTools={() => toggleTools(server.name)}
                  onDisconnect={() => handleDisconnect(server.name)}
                  disconnecting={disconnecting === server.name}
                  onRefresh={() => handleRefresh(server.name)}
                  refreshing={refreshing === server.name}
                />
              ))}
            </div>
          )}
        </div>
      )}

      {/* === Server Catalog === */}
      <SectionHeader
        title="Server Catalog"
        count={registry?.length || 0}
        expanded={expandedSections.has('catalog')}
        onToggle={() => toggleSection('catalog')}
      />
      {expandedSections.has('catalog') && (
        <div>
          {/* Setup form for a registry entry */}
          {setupEntry && (
            <div className="mb-4 p-4 rounded-lg bg-card border border-indigo-500/30 space-y-3">
              <div className="flex items-center justify-between">
                <h4 className="font-semibold">
                  Connect <span className="text-indigo-400">{setupEntry.name}</span>
                </h4>
                <button
                  onClick={() => { setSetupEntry(null); setEnvValues({}) }}
                  className="text-muted-foreground hover:text-foreground text-sm"
                >
                  Cancel
                </button>
              </div>
              <p className="text-sm text-muted-foreground">{setupEntry.description}</p>

              {[...(setupEntry.required_env || []), ...(setupEntry.optional_env || [])].map(key => {
                const isRequired = setupEntry.required_env?.includes(key)
                const hasExisting = setupEntry.existing_settings?.[key]
                return (
                  <div key={key} className="space-y-1">
                    <label className="text-xs font-medium text-muted-foreground flex items-center gap-2">
                      {key}
                      {isRequired && <span className="text-red-400">*</span>}
                      {hasExisting && (
                        <span className="text-xs px-1.5 py-0.5 rounded bg-green-900/40 text-green-400">saved</span>
                      )}
                    </label>
                    <input
                      type="password"
                      placeholder={hasExisting ? '(using saved value)' : `Enter ${key}`}
                      value={envValues[key] || ''}
                      onChange={(e) => setEnvValues(prev => ({ ...prev, [key]: e.target.value }))}
                      className="w-full px-3 py-2 rounded bg-background border border-border text-sm font-mono"
                    />
                  </div>
                )
              })}

              <div className="flex gap-2 pt-1">
                <button
                  onClick={handleSetupSubmit}
                  disabled={connectingServer === setupEntry.name}
                  className="px-4 py-2 rounded bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium disabled:opacity-50 transition-colors"
                >
                  {connectingServer === setupEntry.name ? 'Connecting...' : 'Connect'}
                </button>
              </div>
            </div>
          )}

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {registry?.map(entry => {
              const isConnected = connectedNames.has(entry.name)
              return (
                <div
                  key={entry.name}
                  className={`p-4 rounded-lg border space-y-2 transition-colors ${
                    isConnected
                      ? 'bg-card/50 border-border opacity-60'
                      : 'bg-card border-border hover:border-muted-foreground/30'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <h4 className="font-semibold text-sm">{entry.name}</h4>
                    <div className="flex items-center gap-2">
                      {entry.builtin_go && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-emerald-900/40 text-emerald-400 font-medium">
                          native
                        </span>
                      )}
                      {isConnected ? (
                        <span className="text-xs px-2 py-0.5 rounded bg-green-900/50 text-green-400">connected</span>
                      ) : (
                        <button
                          onClick={() => handleConnect(entry)}
                          disabled={connectingServer === entry.name}
                          className="text-xs px-3 py-1 rounded bg-indigo-600 hover:bg-indigo-500 text-white font-medium disabled:opacity-50 transition-colors"
                        >
                          {connectingServer === entry.name ? 'Connecting...' : 'Connect'}
                        </button>
                      )}
                    </div>
                  </div>
                  <p className="text-xs text-muted-foreground">{entry.description}</p>
                  {(entry.required_env?.length || 0) > 0 && (
                    <div className="flex flex-wrap gap-1">
                      {entry.required_env!.map(key => (
                        <span key={key} className={`text-[10px] px-1.5 py-0.5 rounded font-mono ${
                          entry.existing_settings?.[key]
                            ? 'bg-green-900/30 text-green-400'
                            : 'bg-yellow-900/30 text-yellow-400'
                        }`}>
                          {key}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* === Custom Server === */}
      <SectionHeader
        title="Custom Server"
        expanded={expandedSections.has('custom')}
        onToggle={() => toggleSection('custom')}
      />
      {expandedSections.has('custom') && (
        <div className="p-4 rounded-lg bg-card border border-border space-y-3">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs font-medium text-muted-foreground">Name</label>
              <input
                type="text"
                placeholder="my-server"
                value={customName}
                onChange={e => setCustomName(e.target.value)}
                className="w-full px-3 py-2 rounded bg-background border border-border text-sm font-mono"
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs font-medium text-muted-foreground">Transport</label>
              <select
                value={customTransport}
                onChange={e => setCustomTransport(e.target.value)}
                className="w-full px-3 py-2 rounded bg-background border border-border text-sm"
              >
                <option value="stdio">stdio</option>
                <option value="http">http</option>
                <option value="sse">sse</option>
              </select>
            </div>
          </div>

          {customTransport === 'stdio' ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="text-xs font-medium text-muted-foreground">Command</label>
                <input
                  type="text"
                  placeholder="npx"
                  value={customCommand}
                  onChange={e => setCustomCommand(e.target.value)}
                  className="w-full px-3 py-2 rounded bg-background border border-border text-sm font-mono"
                />
              </div>
              <div className="space-y-1">
                <label className="text-xs font-medium text-muted-foreground">Args (space-separated)</label>
                <input
                  type="text"
                  placeholder="-y @modelcontextprotocol/server-example"
                  value={customArgs}
                  onChange={e => setCustomArgs(e.target.value)}
                  className="w-full px-3 py-2 rounded bg-background border border-border text-sm font-mono"
                />
              </div>
            </div>
          ) : (
            <div className="space-y-1">
              <label className="text-xs font-medium text-muted-foreground">URL</label>
              <input
                type="text"
                placeholder="http://localhost:3000/mcp"
                value={customURL}
                onChange={e => setCustomURL(e.target.value)}
                className="w-full px-3 py-2 rounded bg-background border border-border text-sm font-mono"
              />
            </div>
          )}

          <button
            onClick={handleCustomConnect}
            disabled={!customName.trim() || !!connectingServer}
            className="px-4 py-2 rounded bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium disabled:opacity-50 transition-colors"
          >
            {connectingServer === customName ? 'Connecting...' : 'Connect'}
          </button>
        </div>
      )}

      {/* === Advanced Settings === */}
      <SectionHeader
        title="Advanced Settings"
        count={settings?.length || 0}
        expanded={expandedSections.has('settings')}
        onToggle={() => toggleSection('settings')}
      />
      {expandedSections.has('settings') && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground pl-1">
            Key-value settings injected into dynamic tool templates via {'{{.KEY}}'}.
          </p>

          {/* Add / Edit form */}
          <div className="p-4 rounded-lg bg-card border border-border space-y-3">
            <h4 className="font-semibold text-sm">{editingKey ? 'Edit Setting' : 'Add Setting'}</h4>
            <div className="flex flex-col sm:flex-row gap-3">
              <input
                type="text"
                placeholder="KEY"
                value={settingKey}
                onChange={e => setSettingKey(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, ''))}
                disabled={editingKey !== null}
                className="flex-1 px-3 py-2 rounded bg-background border border-border text-sm font-mono disabled:opacity-50"
              />
              <input
                type={settingSensitive ? 'password' : 'text'}
                placeholder="Value"
                value={settingValue}
                onChange={e => setSettingValue(e.target.value)}
                className="flex-[2] px-3 py-2 rounded bg-background border border-border text-sm font-mono"
              />
              <label className="flex items-center gap-2 text-sm text-muted-foreground whitespace-nowrap">
                <input
                  type="checkbox"
                  checked={settingSensitive}
                  onChange={e => setSettingSensitive(e.target.checked)}
                  className="rounded"
                />
                Sensitive
              </label>
              <div className="flex gap-2">
                <button
                  onClick={handleSaveSetting}
                  disabled={saving || !settingKey.trim()}
                  className="px-4 py-2 rounded bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium disabled:opacity-50 transition-colors"
                >
                  {saving ? 'Saving...' : 'Save'}
                </button>
                {editingKey && (
                  <button
                    onClick={() => { setSettingKey(''); setSettingValue(''); setSettingSensitive(false); setEditingKey(null) }}
                    className="px-4 py-2 rounded bg-muted hover:bg-muted/80 text-sm transition-colors"
                  >
                    Cancel
                  </button>
                )}
              </div>
            </div>
          </div>

          {/* Settings table */}
          {settings && settings.length > 0 && (
            <div className="rounded-lg border border-border overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="bg-muted/50 border-b border-border">
                    <th className="text-left px-4 py-2 font-medium">Key</th>
                    <th className="text-left px-4 py-2 font-medium">Value</th>
                    <th className="text-left px-4 py-2 font-medium">Sensitive</th>
                    <th className="text-right px-4 py-2 font-medium">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {settings.map(s => (
                    <tr key={s.key} className="border-b border-border last:border-0 hover:bg-muted/30">
                      <td className="px-4 py-2 font-mono">{s.key}</td>
                      <td className="px-4 py-2 font-mono text-muted-foreground">
                        {s.sensitive ? '********' : s.value}
                      </td>
                      <td className="px-4 py-2">
                        {s.sensitive && (
                          <span className="text-xs px-2 py-0.5 rounded bg-yellow-900/40 text-yellow-400">yes</span>
                        )}
                      </td>
                      <td className="px-4 py-2 text-right space-x-2">
                        <button
                          onClick={() => handleEditSetting(s)}
                          className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
                        >
                          Edit
                        </button>
                        <button
                          onClick={() => handleDeleteSetting(s.key)}
                          className="text-xs text-red-400 hover:text-red-300 transition-colors"
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// --- Sub-components ---

function SectionHeader({
  title,
  count,
  expanded,
  onToggle,
}: {
  title: string
  count?: number
  expanded: boolean
  onToggle: () => void
}) {
  return (
    <button
      onClick={onToggle}
      className="w-full flex items-center gap-2 group text-left"
    >
      <span className={`text-muted-foreground transition-transform ${expanded ? 'rotate-90' : ''}`}>
        &#9654;
      </span>
      <h3 className="text-lg font-semibold group-hover:text-foreground transition-colors">{title}</h3>
      {count !== undefined && (
        <span className="text-xs text-muted-foreground px-2 py-0.5 rounded-full bg-muted">{count}</span>
      )}
    </button>
  )
}

function ServerCard({
  server,
  expanded,
  onToggleTools,
  onDisconnect,
  disconnecting,
  onRefresh,
  refreshing,
}: {
  server: MCPServerResponse
  expanded: boolean
  onToggleTools: () => void
  onDisconnect: () => void
  disconnecting: boolean
  onRefresh: () => void
  refreshing: boolean
}) {
  return (
    <div className="p-4 rounded-lg bg-card border border-border space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <h4 className="font-semibold">{server.name}</h4>
          <span className="text-xs px-2 py-0.5 rounded bg-green-900/50 text-green-400">connected</span>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={onRefresh}
            disabled={refreshing || disconnecting}
            className="text-xs px-3 py-1 rounded bg-indigo-900/40 hover:bg-indigo-900/60 text-indigo-400 font-medium disabled:opacity-50 transition-colors"
            title="Reconnect server and re-discover tools"
          >
            {refreshing ? 'Refreshing...' : 'Refresh'}
          </button>
          <button
            onClick={onDisconnect}
            disabled={disconnecting || refreshing}
            className="text-xs px-3 py-1 rounded bg-red-900/40 hover:bg-red-900/60 text-red-400 font-medium disabled:opacity-50 transition-colors"
          >
            {disconnecting ? 'Disconnecting...' : 'Disconnect'}
          </button>
        </div>
      </div>

      <div className="text-sm text-muted-foreground space-y-0.5">
        {server.transport && <div>Transport: {server.transport}</div>}
        {server.url && <div className="font-mono text-xs truncate">{server.url}</div>}
        {server.command && <div className="font-mono text-xs">{server.command}</div>}
      </div>

      {server.tools && server.tools.length > 0 && (
        <div>
          <button
            onClick={onToggleTools}
            className="text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            {expanded ? '▾' : '▸'} Tools ({server.tools.length})
          </button>
          {expanded && (
            <div className="flex flex-wrap gap-1 mt-1.5">
              {server.tools.map(tool => (
                <span key={tool} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                  {tool}
                </span>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
