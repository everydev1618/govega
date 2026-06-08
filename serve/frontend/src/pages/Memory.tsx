// Memory viewer (govega#71). Read-only view onto the shared user
// wiki. Auto-refreshes every 3 seconds so you can watch Mira
// populate pages while you chat with your agents.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import { api } from '../lib/api'
import type {
  MemoryCluster,
  MemoryGraph,
  MemoryPage,
  MemoryPageMetadata,
} from '../lib/types'
import { MemoryGraphView } from '../components/MemoryGraph'

type MemoryTab = 'list' | 'graph'

const REFRESH_MS = 3000

const CLUSTER_ORDER: { key: MemoryCluster; label: string; accent: string }[] = [
  { key: 'index', label: 'Index', accent: 'bg-indigo-500' },
  { key: 'profile', label: 'Profile', accent: 'bg-amber-500' },
  { key: 'decisions', label: 'Decisions', accent: 'bg-rose-500' },
  { key: 'topics', label: 'Topics', accent: 'bg-emerald-500' },
  { key: 'people', label: 'People', accent: 'bg-sky-500' },
  { key: 'notes', label: 'Notes', accent: 'bg-violet-500' },
  { key: 'legacy', label: 'Legacy', accent: 'bg-zinc-500' },
  { key: 'other', label: 'Other', accent: 'bg-zinc-400' },
]

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

function formatRelative(iso: string): string {
  const delta = Math.max(0, Date.now() - new Date(iso).getTime())
  const sec = Math.floor(delta / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  return new Date(iso).toLocaleDateString()
}

export function Memory() {
  const [tab, setTab] = useState<MemoryTab>('list')
  const [pages, setPages] = useState<MemoryPageMetadata[] | null>(null)
  const [graph, setGraph] = useState<MemoryGraph | null>(null)
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [selectedPage, setSelectedPage] = useState<MemoryPage | null>(null)
  const [lastFetch, setLastFetch] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const selectedPathRef = useRef<string | null>(null)
  selectedPathRef.current = selectedPath

  const refresh = useCallback(async () => {
    try {
      const [list, g] = await Promise.all([api.listMemoryPages(), api.getMemoryGraph()])
      setPages(list)
      setGraph(g)
      setLastFetch(new Date().toISOString())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load memory')
    }
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, REFRESH_MS)
    return () => clearInterval(id)
  }, [refresh])

  useEffect(() => {
    if (!selectedPath) {
      setSelectedPage(null)
      return
    }
    let cancelled = false
    api.getMemoryPage(selectedPath)
      .then((p) => {
        if (!cancelled && selectedPathRef.current === selectedPath) {
          setSelectedPage(p)
        }
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [selectedPath, lastFetch])

  useEffect(() => {
    if (selectedPath || !pages || pages.length === 0) return
    const memoryIndex = pages.find((p) => p.path === 'MEMORY.md')
    setSelectedPath((memoryIndex ?? pages[0]).path)
  }, [pages, selectedPath])

  const grouped = useMemo(() => {
    const buckets = new Map<MemoryCluster, MemoryPageMetadata[]>()
    for (const p of pages ?? []) {
      const list = buckets.get(p.cluster) ?? []
      list.push(p)
      buckets.set(p.cluster, list)
    }
    return buckets
  }, [pages])

  const linksOut = useMemo(() => {
    if (!graph || !selectedPath) return []
    return graph.edges.filter((e) => e.from === selectedPath).map((e) => e.to)
  }, [graph, selectedPath])
  const linksIn = useMemo(() => {
    if (!graph || !selectedPath) return []
    return graph.edges.filter((e) => e.to === selectedPath).map((e) => e.from)
  }, [graph, selectedPath])

  const totalBytes = (pages ?? []).reduce((sum, p) => sum + p.bytes, 0)

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-6 py-4">
        <div>
          <h1 className="text-base font-semibold">Memory</h1>
          <p className="text-[12px] text-muted-foreground">
            The shared wiki Mira maintains for you across every agent.
          </p>
        </div>
        <div className="flex items-center gap-3 text-[12px] text-muted-foreground">
          <div className="flex items-center gap-1 rounded-md bg-muted/40 p-0.5">
            <button
              type="button"
              onClick={() => setTab('list')}
              className={`rounded px-2 py-0.5 ${tab === 'list' ? 'bg-background shadow-sm text-foreground' : 'hover:bg-accent/40'}`}
            >
              List
            </button>
            <button
              type="button"
              onClick={() => setTab('graph')}
              className={`rounded px-2 py-0.5 ${tab === 'graph' ? 'bg-background shadow-sm text-foreground' : 'hover:bg-accent/40'}`}
            >
              Graph
            </button>
          </div>
          <span>·</span>
          <span>{pages?.length ?? 0} pages</span>
          <span>·</span>
          <span>{formatBytes(totalBytes)}</span>
          {lastFetch && (
            <>
              <span>·</span>
              <span title={lastFetch}>updated {formatRelative(lastFetch)}</span>
            </>
          )}
          <button
            onClick={refresh}
            className="rounded-md px-2 py-1 hover:bg-accent/40"
            aria-label="Refresh"
            title="Refresh"
          >
            ↻
          </button>
        </div>
      </div>

      {error && (
        <div className="border-b border-destructive/40 bg-destructive/10 px-6 py-2 text-[12px] text-destructive">
          {error}
        </div>
      )}

      {tab === 'graph' ? (
        <MemoryGraphView />
      ) : (
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-72 shrink-0 flex-col overflow-y-auto border-r border-border bg-muted/20">
          {pages === null ? (
            <div className="space-y-2 p-4 text-[12px] text-muted-foreground">Loading…</div>
          ) : pages.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center px-6 text-center text-muted-foreground">
              <div className="text-2xl mb-2">🧠</div>
              <p className="text-[13px] font-medium">Memory is empty</p>
              <p className="mt-1 text-[11px] leading-relaxed">
                Chat with your agents. Mira will populate this wiki in the
                background and pages will appear here.
              </p>
            </div>
          ) : (
            <div className="space-y-4 p-3">
              {CLUSTER_ORDER.map(({ key, label, accent }) => {
                const inCluster = grouped.get(key)
                if (!inCluster || inCluster.length === 0) return null
                return (
                  <div key={key}>
                    <div className="mb-1.5 flex items-center gap-2 px-2">
                      <span className={`size-1.5 rounded-full ${accent}`} />
                      <h3 className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                        {label}
                      </h3>
                      <span className="text-[10px] text-muted-foreground/70">
                        {inCluster.length}
                      </span>
                    </div>
                    <ul className="space-y-0.5">
                      {inCluster.map((p) => (
                        <li key={p.path}>
                          <button
                            type="button"
                            onClick={() => setSelectedPath(p.path)}
                            className={`w-full rounded-md px-2 py-1.5 text-left text-[13px] transition-colors ${
                              selectedPath === p.path
                                ? 'bg-accent text-accent-foreground'
                                : 'hover:bg-accent/40'
                            }`}
                            title={p.path}
                          >
                            <div className="flex items-center justify-between gap-2">
                              <span className="truncate">{p.title}</span>
                              <span className="shrink-0 text-[10px] text-muted-foreground/70">
                                {formatBytes(p.bytes)}
                              </span>
                            </div>
                          </button>
                        </li>
                      ))}
                    </ul>
                  </div>
                )
              })}
            </div>
          )}
        </aside>

        <main className="min-w-0 flex-1 overflow-y-auto">
          {selectedPath && !selectedPage ? (
            <div className="space-y-3 p-8 text-[12px] text-muted-foreground">Loading…</div>
          ) : selectedPage ? (
            <PageView
              page={selectedPage}
              linksIn={linksIn}
              linksOut={linksOut}
              onJump={setSelectedPath}
            />
          ) : (
            <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
              Pick a page on the left.
            </div>
          )}
        </main>
      </div>
      )}
    </div>
  )
}

function PageView({
  page,
  linksIn,
  linksOut,
  onJump,
}: {
  page: MemoryPage
  linksIn: string[]
  linksOut: string[]
  onJump: (path: string) => void
}) {
  return (
    <div className="mx-auto max-w-3xl px-8 py-6">
      <div className="mb-4">
        <div className="text-[12px] text-muted-foreground font-mono">{page.path}</div>
        <div className="mt-1 text-[11px] text-muted-foreground/70">
          updated {formatRelative(page.updated_at)} · {page.content.length} bytes
        </div>
      </div>

      {page.frontmatter && (
        <pre className="mb-4 overflow-x-auto rounded-md border border-border bg-muted/30 px-3 py-2 text-[11px] text-muted-foreground">
          {page.frontmatter}
        </pre>
      )}

      <article className="prose prose-sm prose-apex max-w-none">
        <ReactMarkdown>{page.content}</ReactMarkdown>
      </article>

      {(linksIn.length > 0 || linksOut.length > 0) && (
        <div className="mt-8 grid grid-cols-1 gap-4 border-t border-border pt-4 sm:grid-cols-2">
          <LinkColumn label="Links from this page" paths={linksOut} onJump={onJump} />
          <LinkColumn label="Pages linking here" paths={linksIn} onJump={onJump} />
        </div>
      )}
    </div>
  )
}

function LinkColumn({
  label,
  paths,
  onJump,
}: {
  label: string
  paths: string[]
  onJump: (path: string) => void
}) {
  return (
    <div>
      <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      {paths.length === 0 ? (
        <p className="text-[12px] text-muted-foreground/60">none</p>
      ) : (
        <ul className="space-y-1 text-[12px]">
          {paths.map((p) => (
            <li key={p}>
              <button
                type="button"
                onClick={() => onJump(p)}
                className="font-mono text-primary hover:underline"
              >
                {p}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
