// MemoryGraph (govega#71) — force-directed graph view onto the wiki
// memory store. Renders to a 2D canvas. The d3-force simulation runs in
// a Web Worker so the main thread stays free even for big graphs.
//
// Controls:
//   - Scope: All / User / per-agent. Refetches the graph on change.
//   - Search: filters/highlights matching titles.
//   - Local-graph N hops: when a node is selected, restrict rendering to
//     its N-hop neighbourhood.
//   - Click a node: opens a side panel with the page content.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import * as d3 from 'd3'
import ReactMarkdown from 'react-markdown'
import { api, APIError } from '../lib/api'
import type {
  MemoryGraph,
  MemoryGraphNode,
  MemoryGraphParams,
  MemoryGraphScope,
  MemoryPage,
} from '../lib/types'
import GraphSimWorker from '../workers/graphSim?worker'

// Colors per cluster. Hex chosen to match the Tailwind accent palette
// the list view uses (bg-indigo-500 etc.) — the two views feel related.
const CLUSTER_COLOR: Record<string, string> = {
  index: '#6366f1',
  profile: '#f59e0b',
  decisions: '#f43f5e',
  topics: '#10b981',
  people: '#0ea5e9',
  notes: '#8b5cf6',
  legacy: '#71717a',
  ghost: '#52525b',
  other: '#a1a1aa',
}

interface Position { x: number; y: number }

interface TickMessage {
  type: 'tick'
  positions: { id: string; x: number; y: number }[]
  alpha: number
}

export function MemoryGraphView() {
  const [graph, setGraph] = useState<MemoryGraph | null>(null)
  const [params, setParams] = useState<MemoryGraphParams>({ scope: 'all' })
  const [agentOptions, setAgentOptions] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedPage, setSelectedPage] = useState<MemoryPage | null>(null)
  const [hoverId, setHoverId] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [localMode, setLocalMode] = useState(false)
  const [hops, setHops] = useState(1)

  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const positionsRef = useRef<Map<string, Position>>(new Map())
  const transformRef = useRef(d3.zoomIdentity)
  const workerRef = useRef<Worker | null>(null)
  const dragRef = useRef<{ id: string; dx: number; dy: number } | null>(null)
  const sizeRef = useRef({ w: 0, h: 0 })

  // Refresh agent options whenever we receive a graph that includes
  // agent-scoped nodes (i.e. scope=all). This is the cheapest way to
  // populate the agent dropdown without a second endpoint.
  useEffect(() => {
    if (!graph) return
    const agents = new Set<string>()
    for (const n of graph.nodes) {
      if (n.scope.startsWith('agent:')) {
        agents.add(n.scope.slice('agent:'.length))
      }
    }
    setAgentOptions((prev) => {
      const merged = new Set([...prev, ...agents])
      return [...merged].sort()
    })
  }, [graph])

  // Fetch graph whenever the scope/agent params change.
  const fetchGraph = useCallback(async (next: MemoryGraphParams) => {
    setLoading(true)
    setError(null)
    try {
      const g = await api.getMemoryGraph(next)
      setGraph(g)
      // Selection from a previous scope rarely survives a refetch.
      setSelectedId(null)
      setSelectedPage(null)
    } catch (err) {
      if (err instanceof APIError) setError(err.message)
      else setError(err instanceof Error ? err.message : 'failed to load')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchGraph(params)
  }, [params, fetchGraph])

  // Indexes derived from the graph. Recomputed when graph changes.
  const { adjacency, nodeById, nodeIDs } = useMemo(() => {
    const adj = new Map<string, Set<string>>()
    const byId = new Map<string, MemoryGraphNode>()
    if (!graph) return { adjacency: adj, nodeById: byId, nodeIDs: [] as string[] }
    for (const n of graph.nodes) {
      byId.set(n.id, n)
      adj.set(n.id, new Set())
    }
    for (const e of graph.edges) {
      adj.get(e.from)?.add(e.to)
      adj.get(e.to)?.add(e.from)
    }
    return { adjacency: adj, nodeById: byId, nodeIDs: graph.nodes.map((n) => n.id) }
  }, [graph])

  // BFS to N hops from the selected node — fuels local-graph mode.
  const localSet = useMemo(() => {
    if (!localMode || !selectedId || !graph) return null
    const visited = new Set<string>([selectedId])
    let frontier = [selectedId]
    for (let h = 0; h < hops; h++) {
      const next: string[] = []
      for (const id of frontier) {
        for (const nb of adjacency.get(id) ?? []) {
          if (!visited.has(nb)) {
            visited.add(nb)
            next.push(nb)
          }
        }
      }
      frontier = next
      if (frontier.length === 0) break
    }
    return visited
  }, [localMode, selectedId, graph, hops, adjacency])

  // Lowercased search index. Empty string = no filter.
  const searchMatch = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q || !graph) return null
    const matches = new Set<string>()
    for (const n of graph.nodes) {
      if (n.title.toLowerCase().includes(q) || n.path.toLowerCase().includes(q)) {
        matches.add(n.id)
      }
    }
    return matches
  }, [search, graph])

  // Set up the simulation worker once. The 'init' message is sent each
  // time the graph payload changes.
  useEffect(() => {
    const w = new GraphSimWorker()
    workerRef.current = w
    w.onmessage = (e: MessageEvent<TickMessage | { type: 'end' }>) => {
      if (e.data.type === 'tick') {
        const positions = positionsRef.current
        for (const p of e.data.positions) {
          positions.set(p.id, { x: p.x, y: p.y })
        }
      }
    }
    w.onerror = (event) => {
      // Without this the previous bug was invisible: a d3 umbrella
      // import touched `document` in the worker and killed it silently.
      // Surface worker errors in the toolbar instead.
      const msg = event.message || 'worker error'
      // eslint-disable-next-line no-console
      console.error('[MemoryGraph worker]', event)
      setError(msg)
    }
    return () => {
      w.postMessage({ type: 'stop' })
      w.terminate()
      workerRef.current = null
    }
  }, [])

  // (Re-)initialize the simulation when the graph payload arrives.
  useEffect(() => {
    if (!graph || !workerRef.current) return
    positionsRef.current = new Map()
    const { w, h } = sizeRef.current
    workerRef.current.postMessage({
      type: 'init',
      nodes: graph.nodes.map((n) => ({ id: n.id })),
      edges: graph.edges.map((e) => ({ from: e.from, to: e.to })),
      width: w || 800,
      height: h || 600,
    })
  }, [graph])

  // Canvas sizing + render loop.
  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    if (!canvas || !container) return

    let raf = 0
    const ctx = canvas.getContext('2d')!

    const resize = () => {
      const dpr = window.devicePixelRatio || 1
      const w = container.clientWidth
      const h = container.clientHeight
      sizeRef.current = { w, h }
      canvas.width = Math.floor(w * dpr)
      canvas.height = Math.floor(h * dpr)
      canvas.style.width = `${w}px`
      canvas.style.height = `${h}px`
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    }
    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(container)

    // d3-zoom on the canvas. We let it drive a transform and apply it
    // manually each frame — that's the supported canvas pattern.
    const sel = d3.select(canvas)
    const zoom = d3
      .zoom<HTMLCanvasElement, unknown>()
      .scaleExtent([0.1, 8])
      .filter((event) => {
        // Don't pan when a node-drag is in flight; don't pan on right-click.
        if (event.type === 'mousedown' && dragRef.current) return false
        return !event.button
      })
      .on('zoom', (event) => {
        transformRef.current = event.transform
      })
    sel.call(zoom)
    ;(canvas as unknown as { __zoom: d3.ZoomBehavior<HTMLCanvasElement, unknown> }).__zoom = zoom

    const render = () => {
      raf = requestAnimationFrame(render)
      const { w, h } = sizeRef.current
      ctx.clearRect(0, 0, w, h)
      if (!graph) return

      const t = transformRef.current
      const positions = positionsRef.current

      // Hover neighbourhood — drives dimming.
      const hoverNeighbors = hoverId ? adjacency.get(hoverId) : null

      // Edges first.
      ctx.lineWidth = 1
      for (const e of graph.edges) {
        const a = positions.get(e.from)
        const b = positions.get(e.to)
        if (!a || !b) continue
        if (localSet && (!localSet.has(e.from) || !localSet.has(e.to))) continue

        const dimmed =
          (hoverId && !(e.from === hoverId || e.to === hoverId)) ||
          (searchMatch && !(searchMatch.has(e.from) || searchMatch.has(e.to)))
        ctx.globalAlpha = dimmed ? 0.05 : 0.35
        ctx.strokeStyle = '#71717a'
        const ax = t.applyX(a.x)
        const ay = t.applyY(a.y)
        const bx = t.applyX(b.x)
        const by = t.applyY(b.y)
        ctx.beginPath()
        ctx.moveTo(ax, ay)
        ctx.lineTo(bx, by)
        ctx.stroke()
      }
      ctx.globalAlpha = 1

      // Nodes.
      for (const n of graph.nodes) {
        const p = positions.get(n.id)
        if (!p) continue
        if (localSet && !localSet.has(n.id)) continue

        const deg = adjacency.get(n.id)?.size ?? 0
        const r = Math.max(2, Math.min(14, 2 + Math.sqrt(deg) * 1.8))
        const cx = t.applyX(p.x)
        const cy = t.applyY(p.y)

        const isHover = n.id === hoverId
        const isSelected = n.id === selectedId
        const isNeighbor = hoverNeighbors?.has(n.id) ?? false
        const dim =
          (hoverId && !isHover && !isNeighbor) ||
          (searchMatch && !searchMatch.has(n.id))
        ctx.globalAlpha = dim ? 0.15 : 1

        const color = CLUSTER_COLOR[n.cluster] ?? CLUSTER_COLOR.other
        ctx.beginPath()
        ctx.arc(cx, cy, r * t.k > 1 ? r : Math.max(1.5, r), 0, Math.PI * 2)
        if (n.ghost) {
          ctx.fillStyle = 'rgba(82,82,91,0.25)'
          ctx.fill()
          ctx.setLineDash([3, 2])
          ctx.strokeStyle = color
          ctx.stroke()
          ctx.setLineDash([])
        } else {
          ctx.fillStyle = color
          ctx.fill()
        }

        if (isSelected) {
          ctx.lineWidth = 2
          ctx.strokeStyle = '#fafafa'
          ctx.stroke()
        } else if (isHover) {
          ctx.lineWidth = 1.5
          ctx.strokeStyle = '#fafafa'
          ctx.stroke()
        }
      }
      ctx.globalAlpha = 1

      // Labels: only when zoomed in, or for hub/hover nodes.
      const minLabelDegree = t.k > 2.5 ? 0 : 6
      ctx.font = '11px ui-sans-serif, system-ui, sans-serif'
      ctx.textBaseline = 'middle'
      for (const n of graph.nodes) {
        const p = positions.get(n.id)
        if (!p) continue
        if (localSet && !localSet.has(n.id)) continue
        const deg = adjacency.get(n.id)?.size ?? 0
        const isHover = n.id === hoverId
        const isNeighbor = hoverNeighbors?.has(n.id) ?? false
        const isSelected = n.id === selectedId
        const show =
          isHover || isNeighbor || isSelected || deg >= minLabelDegree
        if (!show) continue
        ctx.fillStyle = isHover || isSelected ? '#fafafa' : '#a1a1aa'
        ctx.fillText(n.title, t.applyX(p.x) + 8, t.applyY(p.y))
      }
    }

    raf = requestAnimationFrame(render)
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      sel.on('.zoom', null)
    }
  }, [graph, hoverId, selectedId, localSet, searchMatch, adjacency])

  // Pointer handling: hover via quadtree (rebuilt cheap from positions),
  // drag for individual nodes, click to select.
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !graph) return
    const positions = positionsRef.current

    const screenToWorld = (sx: number, sy: number) => {
      const t = transformRef.current
      return { x: t.invertX(sx), y: t.invertY(sy) }
    }

    const hit = (sx: number, sy: number): MemoryGraphNode | null => {
      const t = transformRef.current
      let best: { node: MemoryGraphNode; d2: number } | null = null
      // Plain O(N) linear scan — fine up to ~10k nodes, and avoids
      // rebuilding a quadtree every pointer move.
      for (const n of graph.nodes) {
        const p = positions.get(n.id)
        if (!p) continue
        const dx = t.applyX(p.x) - sx
        const dy = t.applyY(p.y) - sy
        const d2 = dx * dx + dy * dy
        if (d2 < 144 && (!best || d2 < best.d2)) {
          best = { node: n, d2 }
        }
      }
      return best?.node ?? null
    }

    const localXY = (e: PointerEvent) => {
      const rect = canvas.getBoundingClientRect()
      return { sx: e.clientX - rect.left, sy: e.clientY - rect.top }
    }

    const onPointerMove = (e: PointerEvent) => {
      if (dragRef.current) {
        const { sx, sy } = localXY(e)
        const { x, y } = screenToWorld(sx, sy)
        workerRef.current?.postMessage({
          type: 'drag',
          id: dragRef.current.id,
          x: x + dragRef.current.dx,
          y: y + dragRef.current.dy,
        })
        return
      }
      const { sx, sy } = localXY(e)
      const node = hit(sx, sy)
      setHoverId(node?.id ?? null)
    }

    const onPointerDown = (e: PointerEvent) => {
      if (e.button !== 0) return
      const { sx, sy } = localXY(e)
      const node = hit(sx, sy)
      if (!node) return
      const p = positions.get(node.id)
      if (!p) return
      const { x, y } = screenToWorld(sx, sy)
      dragRef.current = { id: node.id, dx: p.x - x, dy: p.y - y }
      canvas.setPointerCapture(e.pointerId)
      // Prevent d3-zoom from interpreting this as a pan.
      e.stopPropagation()
    }

    const onPointerUp = (e: PointerEvent) => {
      if (dragRef.current) {
        const id = dragRef.current.id
        // If pointer didn't really move, treat as click.
        const { sx, sy } = localXY(e)
        const node = hit(sx, sy)
        workerRef.current?.postMessage({ type: 'release', id })
        if (node && node.id === id) {
          setSelectedId(id)
        }
        dragRef.current = null
        canvas.releasePointerCapture(e.pointerId)
        return
      }
      const { sx, sy } = localXY(e)
      const node = hit(sx, sy)
      if (node) {
        setSelectedId(node.id)
      }
    }

    canvas.addEventListener('pointermove', onPointerMove)
    canvas.addEventListener('pointerdown', onPointerDown)
    canvas.addEventListener('pointerup', onPointerUp)
    return () => {
      canvas.removeEventListener('pointermove', onPointerMove)
      canvas.removeEventListener('pointerdown', onPointerDown)
      canvas.removeEventListener('pointerup', onPointerUp)
    }
  }, [graph])

  // Fetch the markdown for the selected node. Ghost nodes don't have
  // content — skip the fetch entirely.
  useEffect(() => {
    if (!selectedId) {
      setSelectedPage(null)
      return
    }
    const node = nodeById.get(selectedId)
    if (!node || node.ghost) {
      setSelectedPage(null)
      return
    }
    let cancelled = false
    api.getMemoryPage(node.path)
      .then((p) => { if (!cancelled) setSelectedPage(p) })
      .catch(() => { if (!cancelled) setSelectedPage(null) })
    return () => { cancelled = true }
  }, [selectedId, nodeById])

  const selectedNode = selectedId ? nodeById.get(selectedId) : undefined

  const resetZoom = () => {
    const canvas = canvasRef.current
    if (!canvas) return
    const zoom = (canvas as unknown as { __zoom?: d3.ZoomBehavior<HTMLCanvasElement, unknown> }).__zoom
    if (!zoom) return
    d3.select(canvas).transition().duration(250).call(zoom.transform, d3.zoomIdentity)
  }

  const reheat = () => workerRef.current?.postMessage({ type: 'reheat' })

  return (
    <div className="flex h-full min-h-0 flex-1">
      <div ref={containerRef} className="relative flex-1 bg-zinc-950">
        <canvas ref={canvasRef} className="block h-full w-full cursor-grab active:cursor-grabbing" />

        <div className="pointer-events-none absolute inset-x-0 top-0 flex items-start justify-between gap-2 p-3">
          <div className="pointer-events-auto flex flex-wrap items-center gap-2 rounded-md bg-zinc-900/80 px-3 py-2 text-[12px] text-zinc-200 backdrop-blur">
            <label className="flex items-center gap-1.5">
              <span className="text-zinc-400">Scope</span>
              <select
                value={params.scope ?? 'user'}
                onChange={(e) => setParams({ scope: e.target.value as MemoryGraphScope extends never ? never : 'user' | 'agent' | 'all', agent: undefined })}
                className="rounded bg-zinc-800 px-1.5 py-0.5"
              >
                <option value="all">All</option>
                <option value="user">User wiki</option>
                <option value="agent">Agent…</option>
              </select>
            </label>
            {params.scope === 'agent' && (
              <select
                value={params.agent ?? ''}
                onChange={(e) => setParams({ scope: 'agent', agent: e.target.value })}
                className="rounded bg-zinc-800 px-1.5 py-0.5"
              >
                <option value="">— pick agent —</option>
                {agentOptions.map((a) => <option key={a} value={a}>{a}</option>)}
              </select>
            )}
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search title or path"
              className="w-44 rounded bg-zinc-800 px-2 py-0.5 placeholder:text-zinc-500"
            />
            <label className="flex items-center gap-1.5">
              <input
                type="checkbox"
                checked={localMode}
                onChange={(e) => setLocalMode(e.target.checked)}
                disabled={!selectedId}
              />
              <span className={selectedId ? '' : 'text-zinc-500'}>Local</span>
            </label>
            {localMode && (
              <label className="flex items-center gap-1.5">
                <span className="text-zinc-400">{hops} hop{hops > 1 ? 's' : ''}</span>
                <input
                  type="range"
                  min={1}
                  max={4}
                  value={hops}
                  onChange={(e) => setHops(Number(e.target.value))}
                  className="w-20"
                />
              </label>
            )}
            <button onClick={resetZoom} className="rounded bg-zinc-800 px-2 py-0.5 hover:bg-zinc-700">Reset zoom</button>
            <button onClick={reheat} className="rounded bg-zinc-800 px-2 py-0.5 hover:bg-zinc-700">Reheat</button>
            <button onClick={() => fetchGraph(params)} className="rounded bg-zinc-800 px-2 py-0.5 hover:bg-zinc-700">↻</button>
          </div>
          <div className="pointer-events-none rounded-md bg-zinc-900/80 px-3 py-2 text-[11px] text-zinc-400 backdrop-blur">
            {graph ? `${graph.nodes.length} nodes · ${graph.edges.length} edges` : loading ? 'loading…' : ''}
            {error && <div className="text-rose-400">{error}</div>}
          </div>
        </div>

        {graph && graph.nodes.length === 0 && !loading && (
          <div className="absolute inset-0 flex items-center justify-center text-[12px] text-zinc-500">
            No pages in this wiki yet.
          </div>
        )}
      </div>

      {selectedNode && (
        <aside className="flex w-96 shrink-0 flex-col overflow-y-auto border-l border-border bg-background">
          <div className="flex items-start justify-between border-b border-border px-4 py-3">
            <div className="min-w-0">
              <div className="truncate text-[13px] font-semibold">{selectedNode.title}</div>
              <div className="truncate text-[10px] text-muted-foreground font-mono">{selectedNode.path}</div>
              <div className="mt-0.5 text-[10px] text-muted-foreground/70">{selectedNode.scope}</div>
            </div>
            <button onClick={() => setSelectedId(null)} className="ml-2 rounded-md px-1.5 py-0.5 text-muted-foreground hover:bg-accent/40">✕</button>
          </div>
          <div className="flex-1 overflow-y-auto px-4 py-3">
            {selectedNode.ghost ? (
              <p className="text-[12px] text-muted-foreground">
                This page is referenced by other pages but doesn't exist yet.
              </p>
            ) : selectedPage ? (
              <article className="prose prose-sm prose-apex max-w-none">
                <ReactMarkdown>{selectedPage.content}</ReactMarkdown>
              </article>
            ) : (
              <div className="text-[12px] text-muted-foreground">Loading…</div>
            )}
          </div>
        </aside>
      )}
    </div>
  )
}
