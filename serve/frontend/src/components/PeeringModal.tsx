import { useEffect, useState } from 'react'
import { Modal } from './Modal'
import { api } from '../lib/api'
import type { AuditDTO, GrantDTO, PeerDTO, PeeringStatus } from '../lib/types'

type Tab = 'peers' | 'grants' | 'live' | 'audit'

interface Props {
  open: boolean
  onClose: () => void
  status: PeeringStatus
}

// PeeringModal is the operator surface for federation: who can talk to me,
// what they can touch, what's happening now, what happened before. Tabs
// stay simple — one fetch per tab, refresh on switch.
export function PeeringModal({ open, onClose, status }: Props) {
  const [tab, setTab] = useState<Tab>('peers')

  return (
    <Modal open={open} onClose={onClose} title="Federation" widthClass="w-[900px]">
      {/* Local NodeID strip — operator copies this when establishing trust */}
      <div className="mb-3 px-3 py-2 bg-accent/20 rounded text-xs font-mono flex items-center gap-2">
        <span className="text-muted-foreground">My NodeID:</span>
        <code className="text-foreground break-all">{status.node_id ?? 'unknown'}</code>
        {status.node_id && (
          <button
            onClick={() => navigator.clipboard?.writeText(status.node_id!)}
            className="ml-auto text-muted-foreground hover:text-foreground"
            title="Copy"
          >
            ⧉
          </button>
        )}
      </div>

      <div className="flex gap-1 border-b border-border mb-3 -mx-1 px-1">
        {([
          ['peers', 'Peers'],
          ['grants', 'Grants'],
          ['live', 'Live ops'],
          ['audit', 'Audit'],
        ] as [Tab, string][]).map(([key, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={`px-3 py-1.5 text-sm rounded-t-md transition-colors ${
              tab === key
                ? 'bg-accent/40 text-foreground border-b-2 border-primary -mb-px'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent/20'
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'peers' && <PeersTab />}
      {tab === 'grants' && <GrantsTab />}
      {tab === 'live' && <LiveOpsTab />}
      {tab === 'audit' && <AuditTab />}
    </Modal>
  )
}

// --- Peers ---

function PeersTab() {
  const [peers, setPeers] = useState<PeerDTO[]>([])
  const [adding, setAdding] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const refresh = async () => {
    try {
      const list = await api.listPeers()
      setPeers(list)
      setErr(null)
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  useEffect(() => { refresh() }, [])

  const remove = async (nodeID: string) => {
    if (!confirm(`Remove peer ${nodeID}? All grants for this peer will also be deleted.`)) return
    try { await api.deletePeer(nodeID); await refresh() } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  const togglePause = async (p: PeerDTO) => {
    const next = p.trust_level === 'paused' ? 'scoped' : 'paused'
    try { await api.updatePeer(p.node_id, { trust_level: next }); await refresh() } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="space-y-3">
      {err && <div className="text-sm text-red-400 px-2 py-1 bg-red-500/10 rounded">{err}</div>}
      <button
        onClick={() => setAdding(true)}
        className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90"
      >
        + Add peer
      </button>
      {peers.length === 0 ? (
        <p className="text-sm text-muted-foreground px-1">No peers configured. Add one to start federating.</p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-muted-foreground border-b border-border">
              <th className="py-1.5 px-2">Handle</th>
              <th className="py-1.5 px-2">NodeID</th>
              <th className="py-1.5 px-2">Endpoint</th>
              <th className="py-1.5 px-2">Trust</th>
              <th className="py-1.5 px-2">Last seen</th>
              <th className="py-1.5 px-2"></th>
            </tr>
          </thead>
          <tbody>
            {peers.map(p => (
              <tr key={p.node_id} className="border-b border-border/40 hover:bg-accent/10">
                <td className="py-1.5 px-2">{p.handle || <span className="text-muted-foreground">—</span>}</td>
                <td className="py-1.5 px-2 font-mono text-xs truncate max-w-[200px]" title={p.node_id}>{p.node_id}</td>
                <td className="py-1.5 px-2 font-mono text-xs">{p.endpoint}</td>
                <td className="py-1.5 px-2">
                  <span className={`px-1.5 py-0.5 rounded text-xs ${
                    p.trust_level === 'paused' ? 'bg-yellow-500/20 text-yellow-300'
                    : p.trust_level === 'trusted' ? 'bg-emerald-500/20 text-emerald-300'
                    : 'bg-accent/30 text-foreground'
                  }`}>
                    {p.trust_level}
                  </span>
                </td>
                <td className="py-1.5 px-2 text-xs text-muted-foreground">
                  {p.last_seen_at ? new Date(p.last_seen_at).toLocaleString() : 'never'}
                </td>
                <td className="py-1.5 px-2 text-right space-x-1">
                  <button onClick={() => togglePause(p)} className="text-xs text-muted-foreground hover:text-foreground">
                    {p.trust_level === 'paused' ? 'Resume' : 'Pause'}
                  </button>
                  <button onClick={() => remove(p.node_id)} className="text-xs text-red-400 hover:text-red-300">
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && <AddPeerForm onCancel={() => setAdding(false)} onAdded={() => { setAdding(false); refresh() }} />}
    </div>
  )
}

function AddPeerForm({ onCancel, onAdded }: { onCancel: () => void; onAdded: () => void }) {
  const [nodeID, setNodeID] = useState('')
  const [handle, setHandle] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [secret, setSecret] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setErr(null)
    try {
      await api.addPeer({ node_id: nodeID, handle, endpoint, shared_secret: secret })
      onAdded()
    } catch (ex: unknown) {
      setErr(ex instanceof Error ? ex.message : String(ex))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={submit} className="mt-3 p-3 bg-accent/10 border border-border rounded space-y-2">
      <h4 className="font-semibold text-sm">Add peer</h4>
      <p className="text-xs text-muted-foreground">
        Exchange these four fields with your peer out-of-band (Signal, encrypted email). Use the same shared secret on both sides.
      </p>
      {err && <div className="text-sm text-red-400 px-2 py-1 bg-red-500/10 rounded">{err}</div>}
      <input
        placeholder="NodeID (e.g. vega:01ABC...)"
        value={nodeID}
        onChange={e => setNodeID(e.target.value)}
        className="w-full px-2 py-1.5 text-sm bg-background border border-border rounded font-mono"
        required
      />
      <input
        placeholder="Handle (optional, e.g. @alice@nous)"
        value={handle}
        onChange={e => setHandle(e.target.value)}
        className="w-full px-2 py-1.5 text-sm bg-background border border-border rounded"
      />
      <input
        placeholder="Endpoint (e.g. alice.example.com:4433)"
        value={endpoint}
        onChange={e => setEndpoint(e.target.value)}
        className="w-full px-2 py-1.5 text-sm bg-background border border-border rounded font-mono"
        required
      />
      <input
        placeholder="Shared secret (32+ bytes recommended)"
        value={secret}
        onChange={e => setSecret(e.target.value)}
        className="w-full px-2 py-1.5 text-sm bg-background border border-border rounded font-mono"
        type="password"
        required
      />
      <div className="flex gap-2 pt-1">
        <button type="submit" disabled={submitting} className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 disabled:opacity-50">
          {submitting ? 'Adding…' : 'Add'}
        </button>
        <button type="button" onClick={onCancel} className="px-3 py-1.5 text-sm bg-accent/30 rounded hover:bg-accent/50">
          Cancel
        </button>
      </div>
    </form>
  )
}

// --- Grants ---

function GrantsTab() {
  const [grants, setGrants] = useState<GrantDTO[]>([])
  const [peers, setPeers] = useState<PeerDTO[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)

  const refresh = async () => {
    try {
      const [g, p] = await Promise.all([api.listGrants(), api.listPeers()])
      setGrants(g)
      setPeers(p)
      setErr(null)
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  useEffect(() => { refresh() }, [])

  const handleFor = (peerNodeID: string) => peers.find(p => p.node_id === peerNodeID)?.handle || peerNodeID

  const revoke = async (g: GrantDTO) => {
    if (!confirm(`Revoke ${handleFor(g.peer_node_id)} → ${g.local_agent}?`)) return
    try { await api.deleteGrant(g.peer_node_id, g.local_agent); await refresh() } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  const toggleActive = async (g: GrantDTO) => {
    try {
      await api.upsertGrant(g.peer_node_id, g.local_agent, {
        max_tokens_per_op: g.max_tokens_per_op,
        max_ops_per_hour: g.max_ops_per_hour,
        active: !g.active,
      })
      await refresh()
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="space-y-3">
      {err && <div className="text-sm text-red-400 px-2 py-1 bg-red-500/10 rounded">{err}</div>}
      <button
        onClick={() => setAdding(true)}
        disabled={peers.length === 0}
        className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 disabled:opacity-50"
        title={peers.length === 0 ? 'Add a peer first' : ''}
      >
        + Grant access
      </button>
      {grants.length === 0 ? (
        <p className="text-sm text-muted-foreground px-1">
          No grants. Without grants, every peer invoke is denied (default-deny).
        </p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-muted-foreground border-b border-border">
              <th className="py-1.5 px-2">Peer</th>
              <th className="py-1.5 px-2">Agent</th>
              <th className="py-1.5 px-2">Tokens/op</th>
              <th className="py-1.5 px-2">Ops/hour</th>
              <th className="py-1.5 px-2">Active</th>
              <th className="py-1.5 px-2"></th>
            </tr>
          </thead>
          <tbody>
            {grants.map(g => (
              <tr key={`${g.peer_node_id}/${g.local_agent}`} className="border-b border-border/40 hover:bg-accent/10">
                <td className="py-1.5 px-2 truncate max-w-[200px]" title={g.peer_node_id}>{handleFor(g.peer_node_id)}</td>
                <td className="py-1.5 px-2 font-mono">{g.local_agent}</td>
                <td className="py-1.5 px-2">{g.max_tokens_per_op}</td>
                <td className="py-1.5 px-2">{g.max_ops_per_hour}</td>
                <td className="py-1.5 px-2">
                  <button onClick={() => toggleActive(g)} className={`px-1.5 py-0.5 rounded text-xs ${g.active ? 'bg-emerald-500/20 text-emerald-300' : 'bg-yellow-500/20 text-yellow-300'}`}>
                    {g.active ? 'on' : 'off'}
                  </button>
                </td>
                <td className="py-1.5 px-2 text-right">
                  <button onClick={() => revoke(g)} className="text-xs text-red-400 hover:text-red-300">Revoke</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && <AddGrantForm peers={peers} onCancel={() => setAdding(false)} onAdded={() => { setAdding(false); refresh() }} />}
    </div>
  )
}

function AddGrantForm({ peers, onCancel, onAdded }: { peers: PeerDTO[]; onCancel: () => void; onAdded: () => void }) {
  const [peer, setPeer] = useState(peers[0]?.node_id ?? '')
  const [agent, setAgent] = useState('')
  const [maxTokens, setMaxTokens] = useState(8000)
  const [maxOps, setMaxOps] = useState(30)
  const [err, setErr] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setErr(null)
    try {
      await api.upsertGrant(peer, agent, { max_tokens_per_op: maxTokens, max_ops_per_hour: maxOps, active: true })
      onAdded()
    } catch (ex: unknown) {
      setErr(ex instanceof Error ? ex.message : String(ex))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={submit} className="mt-3 p-3 bg-accent/10 border border-border rounded space-y-2">
      <h4 className="font-semibold text-sm">Grant access</h4>
      {err && <div className="text-sm text-red-400 px-2 py-1 bg-red-500/10 rounded">{err}</div>}
      <select
        value={peer}
        onChange={e => setPeer(e.target.value)}
        className="w-full px-2 py-1.5 text-sm bg-background border border-border rounded"
      >
        {peers.map(p => (
          <option key={p.node_id} value={p.node_id}>{p.handle || p.node_id}</option>
        ))}
      </select>
      <input
        placeholder="Local agent name (e.g. researcher)"
        value={agent}
        onChange={e => setAgent(e.target.value)}
        className="w-full px-2 py-1.5 text-sm bg-background border border-border rounded font-mono"
        required
      />
      <div className="grid grid-cols-2 gap-2">
        <label className="text-xs text-muted-foreground">
          Max tokens / op
          <input type="number" min={1} value={maxTokens} onChange={e => setMaxTokens(Number(e.target.value))} className="block w-full mt-1 px-2 py-1.5 text-sm bg-background border border-border rounded" />
        </label>
        <label className="text-xs text-muted-foreground">
          Max ops / hour
          <input type="number" min={1} value={maxOps} onChange={e => setMaxOps(Number(e.target.value))} className="block w-full mt-1 px-2 py-1.5 text-sm bg-background border border-border rounded" />
        </label>
      </div>
      <div className="flex gap-2 pt-1">
        <button type="submit" disabled={submitting} className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 disabled:opacity-50">
          {submitting ? 'Granting…' : 'Grant'}
        </button>
        <button type="button" onClick={onCancel} className="px-3 py-1.5 text-sm bg-accent/30 rounded hover:bg-accent/50">
          Cancel
        </button>
      </div>
    </form>
  )
}

// --- Live ops ---

function LiveOpsTab() {
  const [ops, setOps] = useState<AuditDTO[]>([])
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const refresh = async () => {
      try {
        const list = await api.listLiveOps()
        if (!cancelled) { setOps(list); setErr(null) }
      } catch (e: unknown) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e))
      }
    }
    refresh()
    const id = setInterval(refresh, 2_000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  if (err) return <div className="text-sm text-red-400 px-2 py-1 bg-red-500/10 rounded">{err}</div>
  if (ops.length === 0) return <p className="text-sm text-muted-foreground px-1">No active operations.</p>

  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="text-left text-muted-foreground border-b border-border">
          <th className="py-1.5 px-2">Dir</th>
          <th className="py-1.5 px-2">Peer</th>
          <th className="py-1.5 px-2">Agent</th>
          <th className="py-1.5 px-2">Started</th>
          <th className="py-1.5 px-2">Tokens (in/out)</th>
        </tr>
      </thead>
      <tbody>
        {ops.map(op => (
          <tr key={op.id} className="border-b border-border/40">
            <td className="py-1.5 px-2 text-xs">{op.direction}</td>
            <td className="py-1.5 px-2 truncate max-w-[200px]" title={op.peer_node_id}>{op.peer_handle || op.peer_node_id}</td>
            <td className="py-1.5 px-2 font-mono">{op.agent}</td>
            <td className="py-1.5 px-2 text-xs text-muted-foreground">{new Date(op.timestamp).toLocaleTimeString()}</td>
            <td className="py-1.5 px-2 text-xs">{op.tokens_in}/{op.tokens_out}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// --- Audit ---

function AuditTab() {
  const [rows, setRows] = useState<AuditDTO[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [filters, setFilters] = useState<{ peer?: string; agent?: string; direction?: string }>({})

  const refresh = async () => {
    try {
      const list = await api.listAudit({ ...filters, limit: 200 })
      setRows(list)
      setErr(null)
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  useEffect(() => { refresh() }, [filters.peer, filters.agent, filters.direction])

  return (
    <div className="space-y-2">
      <div className="flex gap-2 flex-wrap">
        <input
          placeholder="Filter by peer NodeID"
          value={filters.peer ?? ''}
          onChange={e => setFilters(f => ({ ...f, peer: e.target.value || undefined }))}
          className="px-2 py-1 text-xs bg-background border border-border rounded font-mono flex-1 min-w-[200px]"
        />
        <input
          placeholder="Filter by agent"
          value={filters.agent ?? ''}
          onChange={e => setFilters(f => ({ ...f, agent: e.target.value || undefined }))}
          className="px-2 py-1 text-xs bg-background border border-border rounded font-mono w-[150px]"
        />
        <select
          value={filters.direction ?? ''}
          onChange={e => setFilters(f => ({ ...f, direction: e.target.value || undefined }))}
          className="px-2 py-1 text-xs bg-background border border-border rounded"
        >
          <option value="">all directions</option>
          <option value="inbound">inbound</option>
          <option value="outbound">outbound</option>
        </select>
      </div>
      {err && <div className="text-sm text-red-400 px-2 py-1 bg-red-500/10 rounded">{err}</div>}
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground px-1">No audit rows.</p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-muted-foreground border-b border-border">
              <th className="py-1 px-2">Time</th>
              <th className="py-1 px-2">Dir</th>
              <th className="py-1 px-2">Peer</th>
              <th className="py-1 px-2">Agent</th>
              <th className="py-1 px-2">Status</th>
              <th className="py-1 px-2">Tokens</th>
              <th className="py-1 px-2">Cost</th>
              <th className="py-1 px-2">Δms</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(r => (
              <tr key={r.id} className={`border-b border-border/40 ${r.status === 'denied' ? 'bg-yellow-500/5' : r.status === 'error' ? 'bg-red-500/5' : ''}`}>
                <td className="py-1 px-2 text-xs text-muted-foreground whitespace-nowrap">{new Date(r.timestamp).toLocaleString()}</td>
                <td className="py-1 px-2 text-xs">{r.direction[0]}</td>
                <td className="py-1 px-2 text-xs truncate max-w-[150px]" title={r.peer_node_id}>{r.peer_handle || r.peer_node_id}</td>
                <td className="py-1 px-2 font-mono text-xs">{r.agent}</td>
                <td className="py-1 px-2 text-xs">
                  <span title={r.denial_reason} className={
                    r.status === 'ok' ? 'text-emerald-300'
                    : r.status === 'denied' ? 'text-yellow-300'
                    : r.status === 'error' ? 'text-red-300'
                    : r.status === 'started' ? 'text-cyan-300'
                    : 'text-muted-foreground'
                  }>{r.status}</span>
                </td>
                <td className="py-1 px-2 text-xs">{r.tokens_in}/{r.tokens_out}</td>
                <td className="py-1 px-2 text-xs">{r.cost_usd ? `$${r.cost_usd.toFixed(4)}` : '—'}</td>
                <td className="py-1 px-2 text-xs">{r.duration_ms}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
