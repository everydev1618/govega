import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { getAvatar } from '../lib/avatars'
import type { AgentResponse } from '../lib/types'

// --- Graph builder ---

interface TreeNode {
  agent: AgentResponse
  children: TreeNode[]
}

interface OrgGraph {
  trees: TreeNode[]
  standalone: AgentResponse[]
}

const META_AGENTS = new Set(['hermes', 'mother'])

function buildOrgGraph(agents: AgentResponse[]): OrgGraph {
  const filtered = agents.filter(a => !META_AGENTS.has(a.name.toLowerCase()))
  const byName = new Map(filtered.map(a => [a.name, a]))

  // Find which agents are subordinates (appear in someone's team[])
  const subordinates = new Set<string>()
  for (const a of filtered) {
    if (a.team) {
      for (const m of a.team) {
        if (byName.has(m)) subordinates.add(m)
      }
    }
  }

  const visited = new Set<string>()

  function buildTree(name: string): TreeNode | null {
    if (visited.has(name)) return null
    const agent = byName.get(name)
    if (!agent) return null
    visited.add(name)
    const children: TreeNode[] = []
    if (agent.team) {
      for (const m of agent.team) {
        const child = buildTree(m)
        if (child) children.push(child)
      }
    }
    return { agent, children }
  }

  // Roots = agents with teams who aren't subordinates themselves
  const trees: TreeNode[] = []
  for (const a of filtered) {
    if (a.team && a.team.length > 0 && !subordinates.has(a.name)) {
      const tree = buildTree(a.name)
      if (tree) trees.push(tree)
    }
  }

  // Standalone = not visited (no team and not subordinate)
  const standalone = filtered.filter(a => !visited.has(a.name))

  return { trees, standalone }
}

// --- Layout algorithm ---

interface LayoutNode {
  agent: AgentResponse
  x: number
  y: number
  r: number
  depth: number
  colorIdx: number
  children: LayoutNode[]
}

interface Layout {
  nodes: LayoutNode[]
  edges: { from: LayoutNode; to: LayoutNode; delay: number }[]
  hasMultipleRoots: boolean
}

function computeLayout(graph: OrgGraph, width: number, height: number): Layout {
  const cx = width / 2
  const cy = height / 2
  const maxR = Math.min(width, height) / 2

  const allNodes: LayoutNode[] = []
  const allEdges: { from: LayoutNode; to: LayoutNode; delay: number }[] = []
  let edgeIdx = 0
  let colorCounter = 0

  // Collect all top-level items (tree roots + standalone)
  const topLevel: { type: 'tree'; node: TreeNode }[] | { type: 'standalone'; agent: AgentResponse }[] = []
  const items: ({ type: 'tree'; node: TreeNode } | { type: 'standalone'; agent: AgentResponse })[] = []
  for (const t of graph.trees) items.push({ type: 'tree', node: t })
  for (const a of graph.standalone) items.push({ type: 'standalone', agent: a })

  if (items.length === 0) return { nodes: [], edges: [], hasMultipleRoots: false }

  // Single item: center it
  if (items.length === 1) {
    const item = items[0]
    if (item.type === 'standalone') {
      const node: LayoutNode = {
        agent: item.agent, x: cx, y: cy, r: 16, depth: 0,
        colorIdx: colorCounter++, children: [],
      }
      allNodes.push(node)
    } else {
      layoutSubtree(item.node, cx, cy, 0, Math.PI * 2, 0)
    }
    return { nodes: allNodes, edges: allEdges, hasMultipleRoots: false }
  }

  // Multiple items: distribute in inner ring, children in outer rings
  const innerR = maxR * 0.35
  const angleStep = (Math.PI * 2) / items.length
  const sectorWidth = angleStep

  items.forEach((item, i) => {
    const angle = -Math.PI / 2 + i * angleStep
    const x = cx + innerR * Math.cos(angle)
    const y = cy + innerR * Math.sin(angle)

    if (item.type === 'standalone') {
      const node: LayoutNode = {
        agent: item.agent, x, y, r: 14, depth: 0,
        colorIdx: colorCounter++, children: [],
      }
      allNodes.push(node)
    } else {
      const sectorStart = angle - sectorWidth / 2
      const sectorEnd = angle + sectorWidth / 2
      layoutSubtree(item.node, x, y, sectorStart, sectorEnd, 0)
    }
  })

  return { nodes: allNodes, edges: allEdges, hasMultipleRoots: items.length > 1 }

  function layoutSubtree(
    tree: TreeNode, px: number, py: number,
    sectorStart: number, sectorEnd: number, depth: number,
  ) {
    const isLeader = tree.children.length > 0
    const ci = colorCounter++
    const node: LayoutNode = {
      agent: tree.agent, x: px, y: py,
      r: isLeader ? 20 : 14, depth,
      colorIdx: ci, children: [],
    }
    allNodes.push(node)

    if (tree.children.length === 0) return

    const childR = depth === 0
      ? maxR * 0.65
      : maxR * 0.85
    const childCount = tree.children.length
    const sectorSpan = sectorEnd - sectorStart
    const childAngleStep = sectorSpan / childCount

    tree.children.forEach((child, i) => {
      const childAngle = sectorStart + childAngleStep * (i + 0.5)
      const childX = cx + childR * Math.cos(childAngle)
      const childY = cy + childR * Math.sin(childAngle)

      const childSectorStart = sectorStart + childAngleStep * i
      const childSectorEnd = childSectorStart + childAngleStep

      layoutSubtree(child, childX, childY, childSectorStart, childSectorEnd, depth + 1)

      // The child node was just pushed — grab it
      const childNode = allNodes[allNodes.length - 1]
      node.children.push(childNode)
      allEdges.push({ from: node, to: childNode, delay: edgeIdx++ })
    })
  }
}

// --- Colors ---

const PALETTE = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24']

// --- Star field ---

function starField(width: number, height: number, count: number) {
  const stars: { x: number; y: number; r: number; opacity: number }[] = []
  // Deterministic pseudo-random via simple seed
  let seed = 42
  const rand = () => { seed = (seed * 16807 + 0) % 2147483647; return seed / 2147483647 }
  for (let i = 0; i < count; i++) {
    stars.push({
      x: rand() * width,
      y: rand() * height,
      r: 0.3 + rand() * 0.8,
      opacity: 0.15 + rand() * 0.35,
    })
  }
  return stars
}

// --- Component ---

interface Props {
  agents: AgentResponse[]
}

export function AgentOrgChart({ agents }: Props) {
  const navigate = useNavigate()
  const [tooltip, setTooltip] = useState<{ agent: AgentResponse; x: number; y: number } | null>(null)

  const graph = useMemo(() => buildOrgGraph(agents), [agents])
  const isEmpty = graph.trees.length === 0 && graph.standalone.length === 0
  if (isEmpty) return null

  const WIDTH = 600
  const HEIGHT = 400
  const layout = useMemo(() => computeLayout(graph, WIDTH, HEIGHT), [graph])
  const stars = useMemo(() => starField(WIDTH, HEIGHT, 60), [])

  return (
    <div>
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-lg font-semibold">Agents</h3>
        <a href="/agents" className="text-xs text-primary hover:underline"
           onClick={e => { e.preventDefault(); navigate('/agents') }}>
          View all
        </a>
      </div>
      <div className="org-chart-container rounded-lg border border-border bg-card overflow-hidden">
        <svg
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          className="w-full"
          style={{ maxHeight: 420 }}
          onMouseLeave={() => setTooltip(null)}
        >
          {/* Layer 1: Star field */}
          {stars.map((s, i) => (
            <circle key={`s${i}`} cx={s.x} cy={s.y} r={s.r}
              fill="#94a3b8" opacity={s.opacity} />
          ))}

          {/* Layer 2: Edges */}
          {layout.edges.map((e, i) => (
            <line key={`e${i}`}
              x1={e.from.x} y1={e.from.y} x2={e.to.x} y2={e.to.y}
              stroke={PALETTE[e.from.colorIdx % PALETTE.length]}
              strokeWidth={1} opacity={0.35}
              className="constellation-line"
              style={{ animationDelay: `${e.delay * 100}ms` }}
            />
          ))}

          {/* Layer 3: Nodes */}
          {layout.nodes.map(n => {
            const color = PALETTE[n.colorIdx % PALETTE.length]
            const isRunning = n.agent.process_status === 'running'
            const initial = n.agent.name.charAt(0).toUpperCase()
            const toolCount = n.agent.tools?.length ?? 0
            const AvatarSvg = getAvatar(n.agent.avatar)

            return (
              <g key={n.agent.name}
                className="cursor-pointer constellation-node-interactive"
                onClick={(e) => { e.stopPropagation(); navigate(`/chat/${n.agent.name}`) }}
                onMouseEnter={(e) => {
                  const svg = (e.currentTarget.ownerSVGElement as SVGSVGElement)
                  const pt = svg.createSVGPoint()
                  pt.x = n.x; pt.y = n.y
                  const ctm = svg.getScreenCTM()
                  if (ctm) {
                    const screen = pt.matrixTransform(ctm)
                    setTooltip({ agent: n.agent, x: screen.x, y: screen.y })
                  }
                }}
                onMouseLeave={() => setTooltip(null)}
              >
                {/* Invisible hit area for easier clicking */}
                <circle cx={n.x} cy={n.y} r={n.r + 14}
                  fill="transparent" />

                {/* Glow halo */}
                <circle cx={n.x} cy={n.y} r={n.r + 6}
                  fill={color} opacity={0.08} />

                {/* Pulsing ring for running agents */}
                {isRunning && (
                  <circle cx={n.x} cy={n.y} r={n.r + 4}
                    fill="none" stroke={color} strokeWidth={1.5}
                    opacity={0.5} className="constellation-core" />
                )}

                {/* Avatar or colored circle with initial */}
                {AvatarSvg ? (
                  <>
                    <clipPath id={`clip-${n.agent.name}`}>
                      <circle cx={n.x} cy={n.y} r={n.r} />
                    </clipPath>
                    <foreignObject
                      x={n.x - n.r} y={n.y - n.r}
                      width={n.r * 2} height={n.r * 2}
                      clipPath={`url(#clip-${n.agent.name})`}
                      style={{ pointerEvents: 'none' }}
                    >
                      <AvatarSvg className="w-full h-full" />
                    </foreignObject>
                  </>
                ) : (
                  <>
                    <circle cx={n.x} cy={n.y} r={n.r}
                      fill={color} opacity={0.85}
                      className="constellation-node-done"
                      style={{ animationDelay: `${n.colorIdx * 80}ms` }}
                    />
                    <text x={n.x} y={n.y} textAnchor="middle" dominantBaseline="central"
                      fill="#0f172a" fontSize={n.r > 16 ? 13 : 10} fontWeight="bold"
                      style={{ pointerEvents: 'none' }}>
                      {initial}
                    </text>
                  </>
                )}

                {/* Name label */}
                <text x={n.x} y={n.y + n.r + 12} textAnchor="middle"
                  fill="#94a3b8" fontSize={9}
                  style={{ pointerEvents: 'none' }}>
                  {n.agent.display_name || n.agent.name}
                </text>

                {/* Tool count badge for leaders */}
                {toolCount > 0 && n.r >= 20 && (
                  <text x={n.x} y={n.y + n.r + 22} textAnchor="middle"
                    fill="#64748b" fontSize={7}
                    style={{ pointerEvents: 'none' }}>
                    {toolCount} tools
                  </text>
                )}
              </g>
            )
          })}

          {/* Layer 4: Center beacon when multiple roots */}
          {layout.hasMultipleRoots && (
            <>
              <circle cx={WIDTH / 2} cy={HEIGHT / 2} r={3}
                fill="#60a5fa" className="constellation-core" />
              <circle cx={WIDTH / 2} cy={HEIGHT / 2} r={6}
                fill="#60a5fa" opacity={0.1} className="constellation-core" />
            </>
          )}
        </svg>

        {/* Tooltip overlay */}
        {tooltip && (
          <div
            className="fixed z-50 px-3 py-2 rounded-lg bg-popover border border-border shadow-lg text-xs pointer-events-none"
            style={{
              left: tooltip.x,
              top: tooltip.y - 60,
              transform: 'translateX(-50%)',
            }}
          >
            <p className="font-semibold text-foreground">{tooltip.agent.display_name || tooltip.agent.name}</p>
            {tooltip.agent.model && (
              <p className="text-muted-foreground font-mono">{tooltip.agent.model}</p>
            )}
            {(tooltip.agent.tools?.length ?? 0) > 0 && (
              <p className="text-muted-foreground">{tooltip.agent.tools!.length} tools</p>
            )}
            {tooltip.agent.process_status && (
              <p className={
                tooltip.agent.process_status === 'running' ? 'text-blue-400' :
                tooltip.agent.process_status === 'completed' ? 'text-green-400' :
                'text-muted-foreground'
              }>{tooltip.agent.process_status}</p>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
