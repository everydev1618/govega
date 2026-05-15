// Force-directed memory-graph simulation. Runs in a Web Worker so the
// main thread stays free to render even with thousands of nodes.
//
// Protocol:
//   main → worker: { type: 'init', nodes, edges, width, height }
//   main → worker: { type: 'reheat' }                       // bump alpha
//   main → worker: { type: 'drag', id, x, y }               // pin a node
//   main → worker: { type: 'release', id }                  // unpin
//   main → worker: { type: 'stop' }
//   worker → main: { type: 'tick', positions: [{id,x,y}], alpha }
//   worker → main: { type: 'end' }

// Import only from d3-force — pulling from the 'd3' umbrella drags in
// d3-selection / d3-zoom, which touch `document` and `window` at module
// init time. That throws inside a Web Worker (no DOM globals), causing
// silent worker death and an empty canvas. Direct submodule import has
// no DOM dependencies.
import {
  forceSimulation,
  forceManyBody,
  forceLink,
  forceCenter,
  forceCollide,
  type Simulation,
  type SimulationNodeDatum,
  type SimulationLinkDatum,
} from 'd3-force'

interface SimNode extends SimulationNodeDatum {
  id: string
  degree: number
}

type SimLink = SimulationLinkDatum<SimNode>

interface InitMessage {
  type: 'init'
  nodes: { id: string }[]
  edges: { from: string; to: string }[]
  width: number
  height: number
}

interface DragMessage { type: 'drag'; id: string; x: number; y: number }
interface ReleaseMessage { type: 'release'; id: string }
interface ReheatMessage { type: 'reheat' }
interface StopMessage { type: 'stop' }

type IncomingMessage = InitMessage | DragMessage | ReleaseMessage | ReheatMessage | StopMessage

let simulation: Simulation<SimNode, SimLink> | null = null
let nodesById = new Map<string, SimNode>()

function postTick(alpha: number) {
  // Pack only what the renderer needs. Avoid sending velocities — they
  // double the payload and the renderer doesn't use them.
  const positions: { id: string; x: number; y: number }[] = []
  for (const n of nodesById.values()) {
    if (n.x === undefined || n.y === undefined) continue
    positions.push({ id: n.id, x: n.x, y: n.y })
  }
  ;(self as unknown as Worker).postMessage({ type: 'tick', positions, alpha })
}

self.onmessage = (e: MessageEvent<IncomingMessage>) => {
  const msg = e.data
  switch (msg.type) {
    case 'init': {
      simulation?.stop()
      // Count degree per node so the renderer can size them and so we can
      // bump up repulsion for hub nodes.
      const degree = new Map<string, number>()
      for (const ed of msg.edges) {
        degree.set(ed.from, (degree.get(ed.from) ?? 0) + 1)
        degree.set(ed.to, (degree.get(ed.to) ?? 0) + 1)
      }

      // Seed positions on a deterministic ring so the first tick doesn't
      // explode. d3-force only seeds .x/.y if both are undefined, so this
      // also serves as our "initial layout."
      const n = msg.nodes.length
      const radius = Math.max(60, Math.min(msg.width, msg.height) / 3)
      const cx = msg.width / 2
      const cy = msg.height / 2
      const simNodes: SimNode[] = msg.nodes.map((node, i) => {
        const angle = (i / Math.max(1, n)) * Math.PI * 2
        return {
          id: node.id,
          degree: degree.get(node.id) ?? 0,
          x: cx + Math.cos(angle) * radius,
          y: cy + Math.sin(angle) * radius,
        }
      })
      nodesById = new Map(simNodes.map((s) => [s.id, s]))

      const simLinks: SimLink[] = msg.edges
        .filter((e) => nodesById.has(e.from) && nodesById.has(e.to))
        .map((e) => ({ source: e.from, target: e.to }))

      simulation = forceSimulation<SimNode, SimLink>(simNodes)
        .force(
          'link',
          forceLink<SimNode, SimLink>(simLinks)
            .id((d) => d.id)
            .distance(40)
            .strength(0.4),
        )
        .force('charge', forceManyBody<SimNode>().strength((d) => -30 - d.degree * 4))
        .force('center', forceCenter(cx, cy).strength(0.05))
        .force(
          'collide',
          forceCollide<SimNode>().radius((d) => 4 + Math.sqrt(d.degree) * 1.5),
        )
        .alphaDecay(0.04)
        .on('tick', () => postTick(simulation?.alpha() ?? 0))
        .on('end', () => {
          postTick(0)
          ;(self as unknown as Worker).postMessage({ type: 'end' })
        })
      break
    }
    case 'drag': {
      const node = nodesById.get(msg.id)
      if (!node || !simulation) break
      node.fx = msg.x
      node.fy = msg.y
      simulation.alphaTarget(0.3).restart()
      break
    }
    case 'release': {
      const node = nodesById.get(msg.id)
      if (!node || !simulation) break
      node.fx = null
      node.fy = null
      simulation.alphaTarget(0)
      break
    }
    case 'reheat': {
      simulation?.alpha(0.6).restart()
      break
    }
    case 'stop': {
      simulation?.stop()
      simulation = null
      nodesById.clear()
      break
    }
  }
}
