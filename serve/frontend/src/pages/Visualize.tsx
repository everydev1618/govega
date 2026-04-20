import { useEffect, useRef, useState, useCallback } from 'react'
import * as d3 from 'd3'
import { useAPI } from '../hooks/useAPI'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import type { SpawnTreeNode, ProcessResponse, AgentResponse } from '../lib/types'

// --- Types ---

interface GraphNode extends d3.SimulationNodeDatum {
  id: string
  agent: string
  task?: string
  status: string
  depth: number
  reason?: string
  startedAt: string
  metrics?: ProcessResponse['metrics']
  // animation state
  birthTime: number
  pulsePhase: number
}

interface GraphLink extends d3.SimulationLinkDatum<GraphNode> {
  sourceId: string
  targetId: string
  type: 'spawn' | 'team'
}

// --- Color palette matching the dark theme ---

const STATUS_COLORS: Record<string, { fill: string; glow: string; ring: string }> = {
  running: { fill: '#3b82f6', glow: 'rgba(59,130,246,0.4)', ring: 'rgba(59,130,246,0.2)' },
  pending: { fill: '#eab308', glow: 'rgba(234,179,8,0.4)', ring: 'rgba(234,179,8,0.2)' },
  completed: { fill: '#22c55e', glow: 'rgba(34,197,94,0.3)', ring: 'rgba(34,197,94,0.15)' },
  failed: { fill: '#ef4444', glow: 'rgba(239,68,68,0.4)', ring: 'rgba(239,68,68,0.2)' },
}

const DEFAULT_COLOR = { fill: '#6b7280', glow: 'rgba(107,114,128,0.3)', ring: 'rgba(107,114,128,0.15)' }

function getColor(status: string) {
  return STATUS_COLORS[status] || DEFAULT_COLOR
}

// --- Flatten spawn tree into nodes and links ---

function flattenTree(
  trees: SpawnTreeNode[],
  processes: ProcessResponse[],
  agents: AgentResponse[],
): { nodes: GraphNode[]; links: GraphLink[] } {
  const nodes: GraphNode[] = []
  const links: GraphLink[] = []
  const processMap = new Map(processes.map(p => [p.id, p]))
  const seen = new Set<string>()

  function walk(node: SpawnTreeNode, parentId?: string) {
    if (seen.has(node.process_id)) return
    seen.add(node.process_id)

    const proc = processMap.get(node.process_id)
    nodes.push({
      id: node.process_id,
      agent: node.agent_name,
      task: node.task,
      status: node.status,
      depth: node.spawn_depth,
      reason: node.spawn_reason,
      startedAt: node.started_at,
      metrics: proc?.metrics,
      birthTime: Date.now(),
      pulsePhase: Math.random() * Math.PI * 2,
    })

    if (parentId) {
      links.push({ sourceId: parentId, targetId: node.process_id, source: parentId, target: node.process_id, type: 'spawn' })
    }

    for (const child of node.children || []) {
      walk(child, node.process_id)
    }
  }

  for (const root of trees) {
    walk(root)
  }

  // Build team links: connect nodes whose agents share a team relationship.
  // An agent's team[] lists the agents it can delegate to / work with.
  const agentMap = new Map(agents.map(a => [a.name, a]))
  const nodesByAgent = new Map<string, GraphNode>()
  for (const node of nodes) {
    // Use base agent name (strip :suffix)
    const base = node.agent.includes(':') ? node.agent.substring(0, node.agent.indexOf(':')) : node.agent
    if (!nodesByAgent.has(base)) {
      nodesByAgent.set(base, node)
    }
  }

  const teamLinkSet = new Set<string>()
  for (const agent of agents) {
    if (!agent.team || agent.team.length === 0) continue
    const sourceNode = nodesByAgent.get(agent.name)
    if (!sourceNode) continue
    for (const teammate of agent.team) {
      const targetNode = nodesByAgent.get(teammate)
      if (!targetNode || targetNode.id === sourceNode.id) continue
      // Deduplicate: only one link per pair
      const key = [sourceNode.id, targetNode.id].sort().join('|')
      if (teamLinkSet.has(key)) continue
      teamLinkSet.add(key)
      links.push({ sourceId: sourceNode.id, targetId: targetNode.id, source: sourceNode.id, target: targetNode.id, type: 'team' })
    }
  }

  return { nodes, links }
}

// --- Main component ---

export function Visualize() {
  const svgRef = useRef<SVGSVGElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const simulationRef = useRef<d3.Simulation<GraphNode, GraphLink> | null>(null)
  const animFrameRef = useRef<number>(0)
  const [selectedNode, setSelectedNode] = useState<GraphNode | null>(null)
  const [dimensions, setDimensions] = useState({ width: 800, height: 600 })

  const { data: tree, refetch: refetchTree } = useAPI(() => api.getSpawnTree())
  const { data: processes, refetch: refetchProcesses } = useAPI(() => api.getProcesses())
  const { data: agents } = useAPI(() => api.getAgents())
  const { events } = useSSE()

  // Auto-refresh on process events
  const lastEventRef = useRef(0)
  useEffect(() => {
    if (events.length === 0) return
    const latest = events[0]
    const ts = new Date(latest.timestamp).getTime()
    if (ts > lastEventRef.current && (latest.type.startsWith('process.') || latest.type.startsWith('agent.'))) {
      lastEventRef.current = ts
      refetchTree()
      refetchProcesses()
    }
  }, [events, refetchTree, refetchProcesses])

  // Track container size
  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    const observer = new ResizeObserver((entries) => {
      const { width, height } = entries[0].contentRect
      setDimensions({ width, height })
    })
    observer.observe(container)
    return () => observer.disconnect()
  }, [])

  // Build and animate the graph
  useEffect(() => {
    if (!tree || !svgRef.current) return

    const { nodes, links } = flattenTree(tree, processes || [], agents || [])
    if (nodes.length === 0) return
    const spawnLinks = links.filter(l => l.type === 'spawn')
    const teamLinks = links.filter(l => l.type === 'team')

    const svg = d3.select(svgRef.current)
    const { width, height } = dimensions

    // Clear previous
    svg.selectAll('*').remove()

    // Defs for gradients and filters
    const defs = svg.append('defs')

    // Glow filter
    const glow = defs.append('filter').attr('id', 'glow').attr('x', '-50%').attr('y', '-50%').attr('width', '200%').attr('height', '200%')
    glow.append('feGaussianBlur').attr('stdDeviation', '4').attr('result', 'blur')
    glow.append('feMerge').selectAll('feMergeNode')
      .data(['blur', 'SourceGraphic'])
      .join('feMergeNode')
      .attr('in', d => d)

    // Pulse glow filter (stronger)
    const pulseGlow = defs.append('filter').attr('id', 'pulse-glow').attr('x', '-100%').attr('y', '-100%').attr('width', '300%').attr('height', '300%')
    pulseGlow.append('feGaussianBlur').attr('stdDeviation', '8').attr('result', 'blur')
    pulseGlow.append('feMerge').selectAll('feMergeNode')
      .data(['blur', 'SourceGraphic'])
      .join('feMergeNode')
      .attr('in', d => d)

    // Arrow marker for links
    defs.append('marker')
      .attr('id', 'arrowhead')
      .attr('viewBox', '0 -5 10 10')
      .attr('refX', 28)
      .attr('refY', 0)
      .attr('markerWidth', 6)
      .attr('markerHeight', 6)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,-4L10,0L0,4')
      .attr('fill', 'rgba(100,116,139,0.4)')

    // Main group with zoom
    const g = svg.append('g')

    const zoom = d3.zoom<SVGSVGElement, unknown>()
      .scaleExtent([0.1, 4])
      .on('zoom', (event) => {
        g.attr('transform', event.transform)
      })

    svg.call(zoom)

    // Center initially
    svg.call(zoom.transform, d3.zoomIdentity.translate(width / 2, height / 2))

    // Team links (dotted, behind spawn links)
    const teamLinkGroup = g.append('g').attr('class', 'team-links')
    const teamLinkElements = teamLinkGroup.selectAll<SVGLineElement, GraphLink>('line')
      .data(teamLinks)
      .join('line')
      .attr('stroke', 'rgba(139,92,246,0.2)')
      .attr('stroke-width', 1)
      .attr('stroke-dasharray', '4 4')

    // Spawn links (solid, with arrows)
    const linkGroup = g.append('g').attr('class', 'links')
    const linkElements = linkGroup.selectAll<SVGLineElement, GraphLink>('line')
      .data(spawnLinks)
      .join('line')
      .attr('stroke', 'rgba(100,116,139,0.25)')
      .attr('stroke-width', 1.5)
      .attr('marker-end', 'url(#arrowhead)')

    // Particle group for animated data flow
    const particleGroup = g.append('g').attr('class', 'particles')

    // Node group
    const nodeGroup = g.append('g').attr('class', 'nodes')

    const nodeElements = nodeGroup.selectAll<SVGGElement, GraphNode>('g')
      .data(nodes, d => d.id)
      .join('g')
      .attr('cursor', 'pointer')
      .on('click', (_, d) => {
        setSelectedNode(prev => prev?.id === d.id ? null : d)
      })
      .call(d3.drag<SVGGElement, GraphNode>()
        .on('start', (event, d) => {
          if (!event.active) simulationRef.current?.alphaTarget(0.3).restart()
          d.fx = d.x
          d.fy = d.y
        })
        .on('drag', (event, d) => {
          d.fx = event.x
          d.fy = event.y
        })
        .on('end', (event, d) => {
          if (!event.active) simulationRef.current?.alphaTarget(0)
          d.fx = null
          d.fy = null
        })
      )

    // Outer ring (status glow)
    nodeElements.append('circle')
      .attr('class', 'outer-ring')
      .attr('r', d => d.depth === 0 ? 28 : 22)
      .attr('fill', d => getColor(d.status).ring)
      .attr('stroke', 'none')

    // Main circle
    nodeElements.append('circle')
      .attr('class', 'main-circle')
      .attr('r', d => d.depth === 0 ? 20 : 16)
      .attr('fill', d => getColor(d.status).fill)
      .attr('stroke', d => getColor(d.status).glow)
      .attr('stroke-width', 2)
      .attr('filter', 'url(#glow)')

    // Inner icon/initial
    nodeElements.append('text')
      .attr('text-anchor', 'middle')
      .attr('dominant-baseline', 'central')
      .attr('fill', 'white')
      .attr('font-size', d => d.depth === 0 ? '12px' : '10px')
      .attr('font-weight', '600')
      .attr('font-family', 'ui-monospace, monospace')
      .attr('pointer-events', 'none')
      .text(d => d.agent.substring(0, 2).toUpperCase())

    // Agent name label
    nodeElements.append('text')
      .attr('class', 'label')
      .attr('y', d => (d.depth === 0 ? 20 : 16) + 14)
      .attr('text-anchor', 'middle')
      .attr('fill', 'rgba(248,250,252,0.8)')
      .attr('font-size', '11px')
      .attr('font-weight', '500')
      .attr('pointer-events', 'none')
      .text(d => d.agent)

    // Status indicator dot
    nodeElements.append('circle')
      .attr('class', 'status-dot')
      .attr('cx', d => (d.depth === 0 ? 20 : 16) * 0.7)
      .attr('cy', d => -(d.depth === 0 ? 20 : 16) * 0.7)
      .attr('r', 4)
      .attr('fill', d => getColor(d.status).fill)
      .attr('stroke', 'hsl(240,10%,3.9%)')
      .attr('stroke-width', 2)

    // Force simulation
    const simulation = d3.forceSimulation<GraphNode>(nodes)
      .force('link', d3.forceLink<GraphNode, GraphLink>(links).id(d => d.id).distance(120).strength(0.5))
      .force('charge', d3.forceManyBody().strength(-400))
      .force('center', d3.forceCenter(0, 0).strength(0.05))
      .force('collision', d3.forceCollide().radius(50))
      .force('y', d3.forceY<GraphNode>().y(d => d.depth * 100).strength(0.1))
      .alphaDecay(0.02)

    simulationRef.current = simulation

    simulation.on('tick', () => {
      linkElements
        .attr('x1', d => (d.source as GraphNode).x!)
        .attr('y1', d => (d.source as GraphNode).y!)
        .attr('x2', d => (d.target as GraphNode).x!)
        .attr('y2', d => (d.target as GraphNode).y!)

      teamLinkElements
        .attr('x1', d => (d.source as GraphNode).x!)
        .attr('y1', d => (d.source as GraphNode).y!)
        .attr('x2', d => (d.target as GraphNode).x!)
        .attr('y2', d => (d.target as GraphNode).y!)

      nodeElements.attr('transform', d => `translate(${d.x},${d.y})`)
    })

    // Animation loop for pulses and particles
    let frameCount = 0
    function animate() {
      frameCount++
      const t = Date.now() / 1000

      // Pulse running nodes
      nodeElements.select('.outer-ring')
        .attr('r', (d) => {
          const base = d.depth === 0 ? 28 : 22
          if (d.status === 'running') {
            return base + Math.sin(t * 2 + d.pulsePhase) * 4
          }
          return base
        })
        .attr('opacity', (d) => {
          if (d.status === 'running') {
            return 0.4 + Math.sin(t * 2 + d.pulsePhase) * 0.3
          }
          return 0.6
        })

      // Animate particles along links for running processes
      if (frameCount % 3 === 0) {
        for (const link of spawnLinks) {
          const targetNode = nodes.find(n => n.id === link.targetId)
          if (targetNode && targetNode.status === 'running') {
            const src = link.source as GraphNode
            const tgt = link.target as GraphNode
            if (src.x == null || tgt.x == null) continue

            const progress = (t * 0.5 + Math.random() * 0.1) % 1
            const px = src.x! + (tgt.x! - src.x!) * progress
            const py = src.y! + (tgt.y! - src.y!) * progress

            particleGroup.append('circle')
              .attr('cx', px)
              .attr('cy', py)
              .attr('r', 2)
              .attr('fill', getColor(targetNode.status).fill)
              .attr('opacity', 0.8)
              .transition()
              .duration(800)
              .attr('opacity', 0)
              .attr('r', 0.5)
              .remove()
          }
        }
      }

      animFrameRef.current = requestAnimationFrame(animate)
    }

    animFrameRef.current = requestAnimationFrame(animate)

    return () => {
      cancelAnimationFrame(animFrameRef.current)
      simulation.stop()
    }
  }, [tree, processes, agents, dimensions])

  const handleRecenter = useCallback(() => {
    if (!svgRef.current) return
    const svg = d3.select(svgRef.current)
    const { width, height } = dimensions
    const zoom = d3.zoom<SVGSVGElement, unknown>()
    svg.transition().duration(500).call(
      zoom.transform as any,
      d3.zoomIdentity.translate(width / 2, height / 2)
    )
  }, [dimensions])

  const totalProcesses = processes?.length ?? 0
  const running = processes?.filter(p => p.status === 'running').length ?? 0
  const completed = processes?.filter(p => p.status === 'completed').length ?? 0
  const failed = processes?.filter(p => p.status === 'failed').length ?? 0

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Header */}
      <div className="flex items-center justify-between mb-4 flex-shrink-0">
        <div>
          <h2 className="text-2xl font-bold">Supervision Tree</h2>
          <p className="text-sm text-muted-foreground mt-0.5">Live process topology</p>
        </div>
        <div className="flex items-center gap-3">
          {/* Stats badges */}
          <div className="flex items-center gap-2 text-xs">
            <span className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-card border border-border">
              <span className="w-2 h-2 rounded-full bg-blue-500" />
              {running} running
            </span>
            <span className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-card border border-border">
              <span className="w-2 h-2 rounded-full bg-green-500" />
              {completed} done
            </span>
            {failed > 0 && (
              <span className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-card border border-border">
                <span className="w-2 h-2 rounded-full bg-red-500" />
                {failed} failed
              </span>
            )}
            <span className="px-2 py-1 rounded-md bg-card border border-border text-muted-foreground">
              {totalProcesses} total
            </span>
          </div>
          <button
            onClick={handleRecenter}
            className="text-sm text-muted-foreground hover:text-foreground px-3 py-1 rounded border border-border transition-colors"
          >
            Re-center
          </button>
          <button
            onClick={() => { refetchTree(); refetchProcesses() }}
            className="text-sm text-muted-foreground hover:text-foreground px-3 py-1 rounded border border-border transition-colors"
          >
            Refresh
          </button>
        </div>
      </div>

      {/* Visualization area */}
      <div className="flex-1 min-h-0 flex gap-4">
        <div
          ref={containerRef}
          className="flex-1 min-h-0 rounded-lg border border-border bg-card overflow-hidden relative"
        >
          {(!tree || tree.length === 0) ? (
            <div className="absolute inset-0 flex items-center justify-center">
              <div className="text-center">
                <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-muted/50 flex items-center justify-center">
                  <svg className="w-8 h-8 text-muted-foreground/50" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 21a9.004 9.004 0 008.716-6.747M12 21a9.004 9.004 0 01-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 017.843 4.582M12 3a8.997 8.997 0 00-7.843 4.582m15.686 0A11.953 11.953 0 0112 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0121 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0112 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 013 12c0-1.605.42-3.113 1.157-4.418" />
                  </svg>
                </div>
                <p className="text-muted-foreground text-sm">No active processes</p>
                <p className="text-muted-foreground/60 text-xs mt-1">Processes will appear here when agents are running</p>
              </div>
            </div>
          ) : (
            <svg
              ref={svgRef}
              width={dimensions.width}
              height={dimensions.height}
              className="w-full h-full"
            />
          )}

          {/* Legend */}
          <div className="absolute bottom-3 left-3 flex items-center gap-3 px-3 py-2 rounded-md bg-background/80 backdrop-blur-sm border border-border text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <span className="w-2.5 h-2.5 rounded-full bg-blue-500" /> Running
            </span>
            <span className="flex items-center gap-1.5">
              <span className="w-2.5 h-2.5 rounded-full bg-yellow-500" /> Pending
            </span>
            <span className="flex items-center gap-1.5">
              <span className="w-2.5 h-2.5 rounded-full bg-green-500" /> Completed
            </span>
            <span className="flex items-center gap-1.5">
              <span className="w-2.5 h-2.5 rounded-full bg-red-500" /> Failed
            </span>
            <span className="w-px h-3 bg-border" />
            <span className="flex items-center gap-1.5">
              <span className="w-4 border-t border-slate-500" /> Spawn
            </span>
            <span className="flex items-center gap-1.5">
              <span className="w-4 border-t border-dashed border-violet-400/50" /> Team
            </span>
          </div>

          {/* Zoom hint */}
          <div className="absolute bottom-3 right-3 text-[10px] text-muted-foreground/40">
            Scroll to zoom · Drag to pan
          </div>
        </div>

        {/* Detail panel */}
        {selectedNode && (
          <div className="w-72 flex-shrink-0 rounded-lg border border-border bg-card p-4 overflow-y-auto">
            <div className="flex items-center justify-between mb-3">
              <h3 className="font-semibold text-sm">Process Detail</h3>
              <button
                onClick={() => setSelectedNode(null)}
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            <div className="space-y-3">
              {/* Agent name */}
              <div>
                <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Agent</label>
                <p className="text-sm font-medium mt-0.5">{selectedNode.agent}</p>
              </div>

              {/* Status */}
              <div>
                <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Status</label>
                <div className="flex items-center gap-2 mt-0.5">
                  <span
                    className="w-2.5 h-2.5 rounded-full"
                    style={{ backgroundColor: getColor(selectedNode.status).fill }}
                  />
                  <span className="text-sm capitalize">{selectedNode.status}</span>
                </div>
              </div>

              {/* Process ID */}
              <div>
                <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Process ID</label>
                <p className="text-xs font-mono text-muted-foreground mt-0.5 break-all">{selectedNode.id}</p>
              </div>

              {/* Task */}
              {selectedNode.task && (
                <div>
                  <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Task</label>
                  <p className="text-sm text-muted-foreground mt-0.5">{selectedNode.task}</p>
                </div>
              )}

              {/* Spawn reason */}
              {selectedNode.reason && (
                <div>
                  <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Spawn Reason</label>
                  <p className="text-sm text-muted-foreground mt-0.5 italic">{selectedNode.reason}</p>
                </div>
              )}

              {/* Depth */}
              <div>
                <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Depth</label>
                <p className="text-sm text-muted-foreground mt-0.5">{selectedNode.depth}</p>
              </div>

              {/* Started */}
              <div>
                <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70">Started</label>
                <p className="text-sm text-muted-foreground mt-0.5">
                  {new Date(selectedNode.startedAt).toLocaleTimeString()}
                </p>
              </div>

              {/* Metrics */}
              {selectedNode.metrics && (
                <div>
                  <label className="text-[10px] uppercase tracking-wider text-muted-foreground/70 block mb-1.5">Metrics</label>
                  <div className="grid grid-cols-2 gap-2">
                    <MetricCard label="Iterations" value={selectedNode.metrics.iterations} />
                    <MetricCard label="Tool Calls" value={selectedNode.metrics.tool_calls} />
                    <MetricCard label="Input Tokens" value={formatNumber(selectedNode.metrics.input_tokens)} />
                    <MetricCard label="Output Tokens" value={formatNumber(selectedNode.metrics.output_tokens)} />
                    <MetricCard label="Cost" value={`$${selectedNode.metrics.cost_usd.toFixed(4)}`} />
                    <MetricCard label="Errors" value={selectedNode.metrics.errors} highlight={selectedNode.metrics.errors > 0} />
                  </div>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function MetricCard({ label, value, highlight }: { label: string; value: string | number; highlight?: boolean }) {
  return (
    <div className={`px-2 py-1.5 rounded border ${highlight ? 'border-red-500/30 bg-red-500/5' : 'border-border bg-background/50'}`}>
      <p className="text-[10px] text-muted-foreground/70">{label}</p>
      <p className={`text-sm font-mono ${highlight ? 'text-red-400' : 'text-foreground'}`}>{value}</p>
    </div>
  )
}

function formatNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}
