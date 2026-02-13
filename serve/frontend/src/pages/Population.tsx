import { useState, useCallback, useEffect } from 'react'
import { api } from '../lib/api'
import type { PopulationSearchResult, PopulationInfoResponse, PopulationInstalledItem } from '../lib/types'

const kindTabs = [
  { value: '', label: 'All' },
  { value: 'persona', label: 'Personas' },
  { value: 'skill', label: 'Skills' },
  { value: 'profile', label: 'Profiles' },
]

const kindColors: Record<string, string> = {
  persona: 'bg-purple-900/50 text-purple-400',
  skill: 'bg-blue-900/50 text-blue-400',
  profile: 'bg-green-900/50 text-green-400',
}

type ViewMode = 'browse' | 'installed'

export function Population() {
  const [viewMode, setViewMode] = useState<ViewMode>('browse')
  const [query, setQuery] = useState('')
  const [kind, setKind] = useState('')
  const [results, setResults] = useState<PopulationSearchResult[]>([])
  const [installed, setInstalled] = useState<PopulationInstalledItem[]>([])
  const [selected, setSelected] = useState<PopulationInfoResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [installing, setInstalling] = useState(false)
  const [installResult, setInstallResult] = useState<'success' | 'error' | null>(null)

  const search = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.populationSearch(query, kind || undefined)
      setResults(data)
      setSelected(null)
    } catch {
      setResults([])
    } finally {
      setLoading(false)
    }
  }, [query, kind])

  const loadInstalled = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.populationInstalled(kind || undefined)
      setInstalled(data)
      setSelected(null)
    } catch {
      setInstalled([])
    } finally {
      setLoading(false)
    }
  }, [kind])

  // Fetch on mount and when kind/viewMode changes.
  useEffect(() => {
    if (viewMode === 'browse') {
      search()
    } else {
      loadInstalled()
    }
  }, [viewMode, search, loadInstalled])

  const selectSearchItem = async (item: PopulationSearchResult) => {
    setInstallResult(null)
    try {
      const info = await api.populationInfo(item.kind, item.name)
      setSelected(info)
    } catch {
      setSelected(null)
    }
  }

  const selectInstalledItem = async (item: PopulationInstalledItem) => {
    setInstallResult(null)
    try {
      const info = await api.populationInfo(item.kind, item.name)
      setSelected(info)
    } catch {
      setSelected(null)
    }
  }

  const install = async () => {
    if (!selected) return
    setInstalling(true)
    setInstallResult(null)
    try {
      const prefix = selected.kind === 'persona' ? '@' : selected.kind === 'profile' ? '+' : ''
      await api.populationInstall(prefix + selected.name)
      setSelected({ ...selected, installed: true })
      setInstallResult('success')
      // Refresh installed list if we're on that tab.
      if (viewMode === 'installed') loadInstalled()
    } catch {
      setInstallResult('error')
    } finally {
      setInstalling(false)
    }
  }

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">Population</h2>

      {/* View mode toggle */}
      <div className="flex gap-1 border-b border-border pb-2">
        <button
          onClick={() => setViewMode('browse')}
          className={`px-4 py-1.5 rounded-t text-sm font-medium transition-colors ${
            viewMode === 'browse'
              ? 'bg-accent text-accent-foreground'
              : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          Browse
        </button>
        <button
          onClick={() => setViewMode('installed')}
          className={`px-4 py-1.5 rounded-t text-sm font-medium transition-colors ${
            viewMode === 'installed'
              ? 'bg-accent text-accent-foreground'
              : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          Installed
        </button>
      </div>

      {/* Search bar (browse mode only) */}
      {viewMode === 'browse' && (
        <div className="flex gap-3 items-center">
          <input
            type="text"
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && search()}
            placeholder="Search personas, skills, profiles..."
            className="flex-1 px-3 py-2 rounded bg-background border border-border text-sm focus:outline-none focus:border-primary"
          />
          <button
            onClick={search}
            className="px-4 py-2 rounded bg-primary text-primary-foreground text-sm font-medium"
          >
            Search
          </button>
        </div>
      )}

      {/* Kind filter tabs */}
      <div className="flex gap-1">
        {kindTabs.map(tab => (
          <button
            key={tab.value}
            onClick={() => setKind(tab.value)}
            className={`px-3 py-1.5 rounded text-sm transition-colors ${
              kind === tab.value
                ? 'bg-accent text-accent-foreground font-medium'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {/* Two-column layout */}
      <div className="flex gap-6">
        {/* Left: item list */}
        <div className="w-80 space-y-2 flex-shrink-0">
          {loading ? (
            <div className="space-y-2">
              {[1, 2, 3].map(i => (
                <div key={i} className="h-20 bg-muted rounded animate-pulse" />
              ))}
            </div>
          ) : viewMode === 'browse' ? (
            results.length === 0 ? (
              <p className="text-sm text-muted-foreground">No results found</p>
            ) : (
              results.map(item => (
                <button
                  key={`${item.kind}-${item.name}`}
                  onClick={() => selectSearchItem(item)}
                  className={`w-full text-left p-3 rounded-lg border transition-colors ${
                    selected?.name === item.name && selected?.kind === item.kind
                      ? 'border-primary bg-accent'
                      : 'border-border bg-card hover:border-primary/50'
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <span className={`text-xs px-1.5 py-0.5 rounded ${kindColors[item.kind] || 'bg-muted text-muted-foreground'}`}>
                      {item.kind}
                    </span>
                    <span className="font-medium text-sm">{item.name}</span>
                  </div>
                  {item.description && (
                    <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{item.description}</p>
                  )}
                  {item.tags && item.tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-1.5">
                      {item.tags.slice(0, 4).map(tag => (
                        <span key={tag} className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}
                </button>
              ))
            )
          ) : (
            installed.length === 0 ? (
              <p className="text-sm text-muted-foreground">Nothing installed yet</p>
            ) : (
              installed.map(item => (
                <button
                  key={`${item.kind}-${item.name}`}
                  onClick={() => selectInstalledItem(item)}
                  className={`w-full text-left p-3 rounded-lg border transition-colors ${
                    selected?.name === item.name && selected?.kind === item.kind
                      ? 'border-primary bg-accent'
                      : 'border-border bg-card hover:border-primary/50'
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <span className={`text-xs px-1.5 py-0.5 rounded ${kindColors[item.kind] || 'bg-muted text-muted-foreground'}`}>
                      {item.kind}
                    </span>
                    <span className="font-medium text-sm">{item.name}</span>
                    {item.version && (
                      <span className="text-xs text-muted-foreground">v{item.version}</span>
                    )}
                  </div>
                  {item.path && (
                    <p className="text-xs text-muted-foreground mt-1 font-mono truncate">{item.path}</p>
                  )}
                </button>
              ))
            )
          )}
        </div>

        {/* Right: detail panel */}
        <div className="flex-1">
          {selected ? (
            <div className="p-4 rounded-lg bg-card border border-border space-y-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <span className={`text-xs px-1.5 py-0.5 rounded ${kindColors[selected.kind] || 'bg-muted text-muted-foreground'}`}>
                    {selected.kind}
                  </span>
                  <h3 className="text-lg font-semibold">{selected.name}</h3>
                  {selected.version && (
                    <span className="text-xs text-muted-foreground">v{selected.version}</span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  {selected.installed && (
                    <span className="text-xs px-2 py-0.5 rounded bg-green-900/50 text-green-400">Installed</span>
                  )}
                  <button
                    onClick={install}
                    disabled={installing}
                    className="px-3 py-1.5 rounded bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
                  >
                    {installing ? 'Installing...' : selected.installed ? 'Reinstall' : 'Install'}
                  </button>
                </div>
              </div>

              {installResult === 'success' && (
                <div className="px-3 py-2 rounded bg-green-900/20 border border-green-900/30 text-green-400 text-sm">
                  Installed successfully
                </div>
              )}
              {installResult === 'error' && (
                <div className="px-3 py-2 rounded bg-red-900/20 border border-red-900/30 text-red-400 text-sm">
                  Installation failed
                </div>
              )}

              {selected.description && (
                <p className="text-sm text-muted-foreground">{selected.description}</p>
              )}

              {selected.author && (
                <div className="text-sm">
                  <span className="text-muted-foreground">Author: </span>
                  <span>{selected.author}</span>
                </div>
              )}

              {selected.tags && selected.tags.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {selected.tags.map(tag => (
                    <span key={tag} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                      {tag}
                    </span>
                  ))}
                </div>
              )}

              {/* Profile details */}
              {selected.persona && (
                <div className="text-sm">
                  <span className="text-muted-foreground">Persona: </span>
                  <span className="font-mono text-xs">@{selected.persona}</span>
                </div>
              )}
              {selected.skills && selected.skills.length > 0 && (
                <div>
                  <span className="text-sm text-muted-foreground">Skills: </span>
                  <div className="flex flex-wrap gap-1 mt-1">
                    {selected.skills.map(s => (
                      <span key={s} className="text-xs px-2 py-0.5 rounded bg-blue-900/50 text-blue-400">{s}</span>
                    ))}
                  </div>
                </div>
              )}

              {/* Persona details */}
              {selected.recommended_skills && selected.recommended_skills.length > 0 && (
                <div>
                  <span className="text-sm text-muted-foreground">Recommended Skills: </span>
                  <div className="flex flex-wrap gap-1 mt-1">
                    {selected.recommended_skills.map(s => (
                      <span key={s} className="text-xs px-2 py-0.5 rounded bg-blue-900/50 text-blue-400">{s}</span>
                    ))}
                  </div>
                </div>
              )}

              {/* System prompt preview */}
              {selected.system_prompt && (
                <div>
                  <span className="text-sm text-muted-foreground">System Prompt:</span>
                  <pre className="mt-1 p-3 rounded bg-background border border-border text-xs overflow-auto max-h-64 whitespace-pre-wrap">
                    {selected.system_prompt}
                  </pre>
                </div>
              )}
            </div>
          ) : (
            <div className="flex items-center justify-center h-48 text-sm text-muted-foreground">
              Select an item to view details
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
