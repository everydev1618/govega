import { useEffect, useState } from 'react'
import { Modal } from './Modal'
import { api } from '../lib/api'
import type { AuditDTO, GrantDTO, InviteDTO, PeerDTO, PeeringStatus } from '../lib/types'

type Tab = 'connections' | 'permissions' | 'active' | 'history'

interface Props {
  open: boolean
  onClose: () => void
  status: PeeringStatus
}

// PeeringModal is the operator surface for federation. The default tab is
// Connections; new users land on a hero-style empty state with two large
// affordances (Create invite / Paste invite) instead of a four-field form.
export function PeeringModal({ open, onClose, status }: Props) {
  const [tab, setTab] = useState<Tab>('connections')

  return (
    <Modal open={open} onClose={onClose} title="Federation" widthClass="w-[960px]">
      <div className="flex gap-1 border-b border-border mb-4 -mx-1 px-1">
        {([
          ['connections', 'Connections'],
          ['permissions', 'Permissions'],
          ['active', 'Active'],
          ['history', 'History'],
        ] as [Tab, string][]).map(([key, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={`px-4 py-2 text-sm rounded-t-md transition-colors ${
              tab === key
                ? 'bg-accent/40 text-foreground border-b-2 border-primary -mb-px font-medium'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent/20'
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'connections' && <ConnectionsTab status={status} />}
      {tab === 'permissions' && <PermissionsTab />}
      {tab === 'active' && <ActiveTab />}
      {tab === 'history' && <HistoryTab />}
    </Modal>
  )
}

// --- Connections ---

type ConnectionsView = 'list' | 'create-invite' | 'paste-invite' | 'manual'

function ConnectionsTab({ status }: { status: PeeringStatus }) {
  const [peers, setPeers] = useState<PeerDTO[]>([])
  const [view, setView] = useState<ConnectionsView>('list')
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = async () => {
    setLoading(true)
    try {
      setPeers(await api.listPeers())
      setErr(null)
    } catch (e: unknown) {
      setErr(formatError(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { refresh() }, [])

  const togglePause = async (p: PeerDTO) => {
    const next = p.trust_level === 'paused' ? 'scoped' : 'paused'
    try { await api.updatePeer(p.node_id, { trust_level: next }); refresh() } catch (e: unknown) {
      setErr(formatError(e))
    }
  }
  const remove = async (nodeID: string) => {
    if (!confirm(`Disconnect from ${nodeID}? All permissions for this peer are also removed.`)) return
    try { await api.deletePeer(nodeID); refresh() } catch (e: unknown) {
      setErr(formatError(e))
    }
  }

  if (view === 'create-invite') return <CreateInviteView status={status} onDone={() => { setView('list'); refresh() }} onBack={() => setView('list')} />
  if (view === 'paste-invite') return <PasteInviteView onDone={() => { setView('list'); refresh() }} onBack={() => setView('list')} />
  if (view === 'manual') return <ManualAddView onDone={() => { setView('list'); refresh() }} onBack={() => setView('list')} />

  return (
    <div className="space-y-3">
      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}

      {peers.length === 0 ? (
        <EmptyConnections onCreate={() => setView('create-invite')} onPaste={() => setView('paste-invite')} onManual={() => setView('manual')} />
      ) : (
        <>
          <div className="flex gap-2">
            <button onClick={() => setView('create-invite')} className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 font-medium">
              + Create invite
            </button>
            <button onClick={() => setView('paste-invite')} className="px-3 py-1.5 text-sm bg-accent/40 hover:bg-accent/60 rounded">
              Paste invite
            </button>
            <button onClick={() => setView('manual')} className="px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground">
              Manual setup…
            </button>
          </div>
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-muted-foreground border-b border-border">
                <th className="py-2 px-2">Name</th>
                <th className="py-2 px-2">Endpoint</th>
                <th className="py-2 px-2">Status</th>
                <th className="py-2 px-2">Last seen</th>
                <th className="py-2 px-2"></th>
              </tr>
            </thead>
            <tbody>
              {peers.map(p => (
                <tr key={p.node_id} className="border-b border-border/40 hover:bg-accent/10">
                  <td className="py-2 px-2">
                    <div className="font-medium">{p.handle || <span className="text-muted-foreground">(no name)</span>}</div>
                    <div className="text-xs text-muted-foreground font-mono truncate max-w-[260px]" title={p.node_id}>{p.node_id}</div>
                  </td>
                  <td className="py-2 px-2 font-mono text-xs">{p.endpoint}</td>
                  <td className="py-2 px-2">
                    <TrustBadge level={p.trust_level} />
                  </td>
                  <td className="py-2 px-2 text-xs text-muted-foreground">
                    {p.last_seen_at ? relativeTime(p.last_seen_at) : 'never'}
                  </td>
                  <td className="py-2 px-2 text-right space-x-3">
                    <button onClick={() => togglePause(p)} className="text-xs text-muted-foreground hover:text-foreground">
                      {p.trust_level === 'paused' ? 'Resume' : 'Pause'}
                    </button>
                    <button onClick={() => remove(p.node_id)} className="text-xs text-red-400 hover:text-red-300">
                      Disconnect
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {loading && peers.length === 0 && !err && (
        <p className="text-sm text-muted-foreground px-1">Loading…</p>
      )}
    </div>
  )
}

function EmptyConnections({ onCreate, onPaste, onManual }: { onCreate: () => void; onPaste: () => void; onManual: () => void }) {
  return (
    <div className="text-center py-8 px-4 space-y-4">
      <div className="text-5xl">🌐</div>
      <div>
        <h3 className="text-lg font-medium">Connect to another Vega</h3>
        <p className="text-sm text-muted-foreground mt-1 max-w-md mx-auto">
          Once connected, you can ask Iris to send tasks to agents on the other side — and they can ask theirs to talk to yours.
        </p>
      </div>
      <div className="flex justify-center gap-3 pt-2">
        <button onClick={onCreate} className="px-4 py-2 bg-primary text-primary-foreground rounded font-medium hover:opacity-90">
          Create an invite
        </button>
        <button onClick={onPaste} className="px-4 py-2 bg-accent/40 rounded hover:bg-accent/60">
          Paste an invite
        </button>
      </div>
      <button onClick={onManual} className="text-xs text-muted-foreground hover:text-foreground underline">
        Or set up manually
      </button>
    </div>
  )
}

function CreateInviteView({ status, onDone, onBack }: { status: PeeringStatus; onDone: () => void; onBack: () => void }) {
  const [invite, setInvite] = useState<InviteDTO | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [returnInvite, setReturnInvite] = useState('')
  const [adding, setAdding] = useState(false)
  const [handle, setHandle] = useState('')

  useEffect(() => {
    (async () => {
      try { setInvite(await api.createInvite()) }
      catch (e: unknown) { setErr(formatError(e)) }
    })()
  }, [])

  const copy = async () => {
    if (!invite) return
    await navigator.clipboard.writeText(JSON.stringify(invite, null, 2))
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const acceptReturn = async () => {
    setAdding(true)
    setErr(null)
    try {
      const parsed: InviteDTO = JSON.parse(returnInvite.trim())
      if (!parsed.node_id || !parsed.endpoint || !parsed.shared_secret) {
        throw new Error('Invite is missing required fields')
      }
      await api.addPeer({
        node_id: parsed.node_id,
        endpoint: parsed.endpoint,
        shared_secret: parsed.shared_secret,
        handle: handle.trim(),
      })
      onDone()
    } catch (e: unknown) {
      setErr(e instanceof SyntaxError ? 'That doesn\'t look like valid invite JSON.' : formatError(e))
    } finally {
      setAdding(false)
    }
  }

  return (
    <div className="space-y-4">
      <button onClick={onBack} className="text-xs text-muted-foreground hover:text-foreground">← Back</button>

      <div>
        <h3 className="font-medium">Step 1 — Share this with your friend</h3>
        <p className="text-sm text-muted-foreground mt-1">
          Copy the block below and send it over a secure channel (Signal, encrypted email — not Twitter). They'll paste it into their Vega's "Paste invite" field.
        </p>
      </div>

      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}

      {invite ? (
        <div className="relative">
          <pre className="bg-accent/20 border border-border rounded p-3 text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all">
{JSON.stringify(invite, null, 2)}
          </pre>
          <button
            onClick={copy}
            className="absolute top-2 right-2 px-2 py-1 text-xs bg-card border border-border rounded hover:bg-accent/40"
          >
            {copied ? '✓ Copied' : 'Copy'}
          </button>
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">Generating invite…</p>
      )}

      <p className="text-xs text-muted-foreground">
        Your local node ID: <code className="font-mono text-foreground">{status.node_id ?? '—'}</code>
      </p>

      <hr className="border-border" />

      <div>
        <h3 className="font-medium">Step 2 — Paste their reply</h3>
        <p className="text-sm text-muted-foreground mt-1">
          They'll send back a similar block. Paste it here to finish the connection.
        </p>
      </div>

      <input
        placeholder="Friendly name for them (optional, e.g. @alice@nous)"
        value={handle}
        onChange={e => setHandle(e.target.value)}
        className="w-full px-3 py-2 text-sm bg-background border border-border rounded"
      />

      <textarea
        placeholder='Paste their JSON here, e.g. {"node_id": "vega:...", "endpoint": "...", "shared_secret": "..."}'
        value={returnInvite}
        onChange={e => setReturnInvite(e.target.value)}
        className="w-full px-3 py-2 text-sm bg-background border border-border rounded font-mono h-32"
      />

      <button
        onClick={acceptReturn}
        disabled={!returnInvite.trim() || adding}
        className="px-4 py-2 bg-primary text-primary-foreground rounded font-medium hover:opacity-90 disabled:opacity-50"
      >
        {adding ? 'Connecting…' : 'Complete connection'}
      </button>
    </div>
  )
}

function PasteInviteView({ onDone, onBack }: { onDone: () => void; onBack: () => void }) {
  const [pasted, setPasted] = useState('')
  const [parsed, setParsed] = useState<InviteDTO | null>(null)
  const [reply, setReply] = useState<InviteDTO | null>(null)
  const [replyCopied, setReplyCopied] = useState(false)
  const [handle, setHandle] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [step, setStep] = useState<'paste' | 'reply'>('paste')

  // Parse as the user types so the preview updates live.
  useEffect(() => {
    if (!pasted.trim()) { setParsed(null); return }
    try {
      const p: InviteDTO = JSON.parse(pasted.trim())
      if (p.node_id && p.endpoint && p.shared_secret) setParsed(p)
      else setParsed(null)
    } catch { setParsed(null) }
  }, [pasted])

  const accept = async () => {
    if (!parsed) return
    setErr(null)
    try {
      await api.addPeer({
        node_id: parsed.node_id,
        endpoint: parsed.endpoint,
        shared_secret: parsed.shared_secret,
        handle: handle.trim(),
      })
      // Now mint our own return invite (reusing their secret).
      const r = await api.returnInvite(parsed)
      setReply(r)
      setStep('reply')
    } catch (e: unknown) {
      setErr(formatError(e))
    }
  }

  const copyReply = async () => {
    if (!reply) return
    await navigator.clipboard.writeText(JSON.stringify(reply, null, 2))
    setReplyCopied(true)
    setTimeout(() => setReplyCopied(false), 1500)
  }

  if (step === 'reply' && reply) {
    return (
      <div className="space-y-4">
        <button onClick={onBack} className="text-xs text-muted-foreground hover:text-foreground">← Back</button>

        <div className="bg-emerald-500/10 border border-emerald-500/30 rounded p-3">
          <p className="text-sm">
            ✓ Connected. Now send <strong>your</strong> reply back so they can complete the connection on their side.
          </p>
        </div>

        <div className="relative">
          <pre className="bg-accent/20 border border-border rounded p-3 text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all">
{JSON.stringify(reply, null, 2)}
          </pre>
          <button
            onClick={copyReply}
            className="absolute top-2 right-2 px-2 py-1 text-xs bg-card border border-border rounded hover:bg-accent/40"
          >
            {replyCopied ? '✓ Copied' : 'Copy'}
          </button>
        </div>

        <p className="text-xs text-muted-foreground">
          Send this back to your friend over the same secure channel. After they paste it on their end, you'll both be connected.
        </p>

        <button onClick={onDone} className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90">
          Done
        </button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <button onClick={onBack} className="text-xs text-muted-foreground hover:text-foreground">← Back</button>

      <div>
        <h3 className="font-medium">Paste invite</h3>
        <p className="text-sm text-muted-foreground mt-1">
          Paste the JSON block your friend sent you. We'll show you a preview before connecting.
        </p>
      </div>

      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}

      <textarea
        placeholder='{"node_id": "vega:...", "endpoint": "...", "shared_secret": "..."}'
        value={pasted}
        onChange={e => setPasted(e.target.value)}
        className="w-full px-3 py-2 text-sm bg-background border border-border rounded font-mono h-32"
        autoFocus
      />

      {parsed && (
        <div className="bg-accent/10 border border-border rounded p-3 space-y-1 text-sm">
          <div className="text-xs uppercase tracking-wide text-muted-foreground mb-1">Preview</div>
          <div><span className="text-muted-foreground">Node ID:</span> <code className="font-mono text-xs">{parsed.node_id}</code></div>
          <div><span className="text-muted-foreground">Endpoint:</span> <code className="font-mono text-xs">{parsed.endpoint}</code></div>
        </div>
      )}

      <input
        placeholder="Friendly name (optional, e.g. @alice@nous)"
        value={handle}
        onChange={e => setHandle(e.target.value)}
        className="w-full px-3 py-2 text-sm bg-background border border-border rounded"
      />

      <button
        onClick={accept}
        disabled={!parsed}
        className="px-4 py-2 bg-primary text-primary-foreground rounded font-medium hover:opacity-90 disabled:opacity-50"
      >
        Connect
      </button>
    </div>
  )
}

function ManualAddView({ onDone, onBack }: { onDone: () => void; onBack: () => void }) {
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
      onDone()
    } catch (ex: unknown) {
      setErr(formatError(ex))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <button type="button" onClick={onBack} className="text-xs text-muted-foreground hover:text-foreground">← Back</button>

      <div>
        <h3 className="font-medium">Manual setup</h3>
        <p className="text-sm text-muted-foreground mt-1">
          For when invite paste isn't an option. Both sides must end up with the same shared secret.
        </p>
      </div>

      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}

      <Field label="Their Node ID" hint="e.g. vega:01ABC...">
        <input value={nodeID} onChange={e => setNodeID(e.target.value)} required
          className="w-full px-3 py-2 text-sm bg-background border border-border rounded font-mono" />
      </Field>
      <Field label="Friendly name" hint="optional, e.g. @alice@nous">
        <input value={handle} onChange={e => setHandle(e.target.value)}
          className="w-full px-3 py-2 text-sm bg-background border border-border rounded" />
      </Field>
      <Field label="Their endpoint" hint="host:port for QUIC, e.g. alice.example.com:4433">
        <input value={endpoint} onChange={e => setEndpoint(e.target.value)} required
          className="w-full px-3 py-2 text-sm bg-background border border-border rounded font-mono" />
      </Field>
      <Field label="Shared secret" hint="32+ random bytes both sides agreed on">
        <input type="password" value={secret} onChange={e => setSecret(e.target.value)} required
          className="w-full px-3 py-2 text-sm bg-background border border-border rounded font-mono" />
      </Field>

      <button type="submit" disabled={submitting} className="px-4 py-2 bg-primary text-primary-foreground rounded font-medium hover:opacity-90 disabled:opacity-50">
        {submitting ? 'Adding…' : 'Add'}
      </button>
    </form>
  )
}

// --- Permissions ---

function PermissionsTab() {
  const [grants, setGrants] = useState<GrantDTO[]>([])
  const [peers, setPeers] = useState<PeerDTO[]>([])
  const [adding, setAdding] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const refresh = async () => {
    try {
      const [g, p] = await Promise.all([api.listGrants(), api.listPeers()])
      setGrants(g)
      setPeers(p)
      setErr(null)
    } catch (e: unknown) { setErr(formatError(e)) }
  }
  useEffect(() => { refresh() }, [])

  const nameOf = (id: string) => peers.find(p => p.node_id === id)?.handle || id
  const toggle = async (g: GrantDTO) => {
    try {
      await api.upsertGrant(g.peer_node_id, g.local_agent, {
        max_tokens_per_op: g.max_tokens_per_op,
        max_ops_per_hour: g.max_ops_per_hour,
        active: !g.active,
      })
      refresh()
    } catch (e: unknown) { setErr(formatError(e)) }
  }
  const revoke = async (g: GrantDTO) => {
    if (!confirm(`Revoke ${nameOf(g.peer_node_id)}'s access to ${g.local_agent}?`)) return
    try { await api.deleteGrant(g.peer_node_id, g.local_agent); refresh() }
    catch (e: unknown) { setErr(formatError(e)) }
  }

  return (
    <div className="space-y-3">
      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}

      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {peers.length === 0
            ? 'Connect to someone first, then grant them access to specific agents.'
            : 'Who can ask which of your agents to do work.'}
        </p>
        <button
          onClick={() => setAdding(true)}
          disabled={peers.length === 0}
          className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 disabled:opacity-50"
        >
          + Grant access
        </button>
      </div>

      {grants.length === 0 ? (
        <p className="text-sm text-muted-foreground px-1 italic">
          No permissions set. Connected peers can talk to your node but can't use any agent until you grant access.
        </p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-muted-foreground border-b border-border">
              <th className="py-2 px-2">Who</th>
              <th className="py-2 px-2">Agent</th>
              <th className="py-2 px-2">Tokens/op</th>
              <th className="py-2 px-2">Ops/hour</th>
              <th className="py-2 px-2">Status</th>
              <th className="py-2 px-2"></th>
            </tr>
          </thead>
          <tbody>
            {grants.map(g => (
              <tr key={`${g.peer_node_id}/${g.local_agent}`} className="border-b border-border/40">
                <td className="py-2 px-2">{nameOf(g.peer_node_id)}</td>
                <td className="py-2 px-2 font-mono">{g.local_agent}</td>
                <td className="py-2 px-2">{g.max_tokens_per_op.toLocaleString()}</td>
                <td className="py-2 px-2">{g.max_ops_per_hour}</td>
                <td className="py-2 px-2">
                  <button onClick={() => toggle(g)} className={`px-2 py-0.5 rounded text-xs ${g.active ? 'bg-emerald-500/20 text-emerald-300' : 'bg-yellow-500/20 text-yellow-300'}`}>
                    {g.active ? 'allowed' : 'paused'}
                  </button>
                </td>
                <td className="py-2 px-2 text-right">
                  <button onClick={() => revoke(g)} className="text-xs text-red-400 hover:text-red-300">Revoke</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {adding && <AddGrantForm peers={peers} onCancel={() => setAdding(false)} onDone={() => { setAdding(false); refresh() }} />}
    </div>
  )
}

function AddGrantForm({ peers, onCancel, onDone }: { peers: PeerDTO[]; onCancel: () => void; onDone: () => void }) {
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
      onDone()
    } catch (ex: unknown) { setErr(formatError(ex)) }
    finally { setSubmitting(false) }
  }

  return (
    <form onSubmit={submit} className="mt-3 p-4 bg-accent/10 border border-border rounded space-y-3">
      <h4 className="font-medium">Grant access</h4>
      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}

      <Field label="Who" hint="">
        <select value={peer} onChange={e => setPeer(e.target.value)} className="w-full px-3 py-2 text-sm bg-background border border-border rounded">
          {peers.map(p => <option key={p.node_id} value={p.node_id}>{p.handle || p.node_id}</option>)}
        </select>
      </Field>

      <Field label="Which agent" hint="local agent name, e.g. researcher">
        <input value={agent} onChange={e => setAgent(e.target.value)} required
          className="w-full px-3 py-2 text-sm bg-background border border-border rounded font-mono" />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="Max tokens per request" hint="default 8000">
          <input type="number" min={1} value={maxTokens} onChange={e => setMaxTokens(Number(e.target.value))}
            className="w-full px-3 py-2 text-sm bg-background border border-border rounded" />
        </Field>
        <Field label="Max requests per hour" hint="default 30">
          <input type="number" min={1} value={maxOps} onChange={e => setMaxOps(Number(e.target.value))}
            className="w-full px-3 py-2 text-sm bg-background border border-border rounded" />
        </Field>
      </div>

      <div className="flex gap-2 pt-1">
        <button type="submit" disabled={submitting} className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 disabled:opacity-50">
          {submitting ? 'Granting…' : 'Grant'}
        </button>
        <button type="button" onClick={onCancel} className="px-3 py-1.5 text-sm bg-accent/40 rounded hover:bg-accent/60">
          Cancel
        </button>
      </div>
    </form>
  )
}

// --- Active ---

function ActiveTab() {
  const [ops, setOps] = useState<AuditDTO[]>([])
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const refresh = async () => {
      try {
        const list = await api.listLiveOps()
        if (!cancelled) { setOps(list); setErr(null) }
      } catch (e: unknown) {
        if (!cancelled) setErr(formatError(e))
      }
    }
    refresh()
    const id = setInterval(refresh, 2_000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  if (err) return <ErrorBanner message={err} />
  if (ops.length === 0) return <p className="text-sm text-muted-foreground px-1 italic">Nothing happening right now.</p>

  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="text-left text-muted-foreground border-b border-border">
          <th className="py-2 px-2">Direction</th>
          <th className="py-2 px-2">Peer</th>
          <th className="py-2 px-2">Agent</th>
          <th className="py-2 px-2">Started</th>
        </tr>
      </thead>
      <tbody>
        {ops.map(op => (
          <tr key={op.id} className="border-b border-border/40">
            <td className="py-2 px-2 text-xs capitalize">{op.direction}</td>
            <td className="py-2 px-2 truncate max-w-[260px]" title={op.peer_node_id}>{op.peer_handle || op.peer_node_id}</td>
            <td className="py-2 px-2 font-mono">{op.agent}</td>
            <td className="py-2 px-2 text-xs text-muted-foreground">{relativeTime(op.timestamp)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// --- History ---

function HistoryTab() {
  const [rows, setRows] = useState<AuditDTO[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [filters, setFilters] = useState<{ peer?: string; agent?: string; direction?: string }>({})

  const refresh = async () => {
    try {
      setRows(await api.listAudit({ ...filters, limit: 200 }))
      setErr(null)
    } catch (e: unknown) { setErr(formatError(e)) }
  }
  useEffect(() => { refresh() }, [filters.peer, filters.agent, filters.direction])

  return (
    <div className="space-y-3">
      <div className="flex gap-2 flex-wrap">
        <input
          placeholder="Filter by node ID"
          value={filters.peer ?? ''}
          onChange={e => setFilters(f => ({ ...f, peer: e.target.value || undefined }))}
          className="px-2 py-1 text-xs bg-background border border-border rounded font-mono flex-1 min-w-[220px]"
        />
        <input
          placeholder="Filter by agent"
          value={filters.agent ?? ''}
          onChange={e => setFilters(f => ({ ...f, agent: e.target.value || undefined }))}
          className="px-2 py-1 text-xs bg-background border border-border rounded font-mono w-[160px]"
        />
        <select
          value={filters.direction ?? ''}
          onChange={e => setFilters(f => ({ ...f, direction: e.target.value || undefined }))}
          className="px-2 py-1 text-xs bg-background border border-border rounded"
        >
          <option value="">All directions</option>
          <option value="inbound">Inbound</option>
          <option value="outbound">Outbound</option>
        </select>
      </div>
      {err && <ErrorBanner message={err} onDismiss={() => setErr(null)} />}
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground px-1 italic">No history yet.</p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-muted-foreground border-b border-border">
              <th className="py-1.5 px-2">When</th>
              <th className="py-1.5 px-2">Dir</th>
              <th className="py-1.5 px-2">Peer</th>
              <th className="py-1.5 px-2">Agent</th>
              <th className="py-1.5 px-2">Result</th>
              <th className="py-1.5 px-2">Tokens</th>
              <th className="py-1.5 px-2">Cost</th>
              <th className="py-1.5 px-2">Δms</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(r => (
              <tr key={r.id} className={`border-b border-border/40 ${r.status === 'denied' ? 'bg-yellow-500/5' : r.status === 'error' ? 'bg-red-500/5' : ''}`}>
                <td className="py-1.5 px-2 text-xs text-muted-foreground whitespace-nowrap">{relativeTime(r.timestamp)}</td>
                <td className="py-1.5 px-2 text-xs">{r.direction === 'inbound' ? '←' : '→'}</td>
                <td className="py-1.5 px-2 text-xs truncate max-w-[180px]" title={r.peer_node_id}>{r.peer_handle || r.peer_node_id}</td>
                <td className="py-1.5 px-2 font-mono text-xs">{r.agent}</td>
                <td className="py-1.5 px-2 text-xs">
                  <span title={r.denial_reason} className={
                    r.status === 'ok' ? 'text-emerald-300'
                    : r.status === 'denied' ? 'text-yellow-300'
                    : r.status === 'error' ? 'text-red-300'
                    : r.status === 'started' ? 'text-cyan-300'
                    : 'text-muted-foreground'
                  }>{statusLabel(r.status)}</span>
                </td>
                <td className="py-1.5 px-2 text-xs">{r.tokens_in.toLocaleString()}/{r.tokens_out.toLocaleString()}</td>
                <td className="py-1.5 px-2 text-xs">{r.cost_usd ? `$${r.cost_usd.toFixed(4)}` : '—'}</td>
                <td className="py-1.5 px-2 text-xs">{r.duration_ms}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// --- shared bits ---

function TrustBadge({ level }: { level: PeerDTO['trust_level'] }) {
  const label = level === 'paused' ? 'paused' : level === 'trusted' ? 'connected' : 'connected'
  const cls = level === 'paused' ? 'bg-yellow-500/20 text-yellow-300' : 'bg-emerald-500/20 text-emerald-300'
  return <span className={`px-2 py-0.5 rounded text-xs ${cls}`}>{label}</span>
}

function Field({ label, hint, children }: { label: string; hint: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <div className="text-xs text-muted-foreground mb-1">{label}{hint && <span className="ml-1 text-muted-foreground/70">— {hint}</span>}</div>
      {children}
    </label>
  )
}

function ErrorBanner({ message, onDismiss }: { message: string; onDismiss?: () => void }) {
  return (
    <div className="flex items-start gap-2 text-sm text-red-300 px-3 py-2 bg-red-500/10 border border-red-500/20 rounded">
      <span className="flex-1">{message}</span>
      {onDismiss && (
        <button onClick={onDismiss} className="text-red-300/70 hover:text-red-300 text-xs">dismiss</button>
      )}
    </div>
  )
}

// formatError unwraps the various ways an error reaches us (network failure,
// API error, JSON parse, plain string) into a single friendly string.
function formatError(e: unknown): string {
  if (e instanceof TypeError && /fetch|NetworkError/i.test(e.message)) {
    return 'Could not reach the server. Is it running?'
  }
  if (e instanceof Error) return e.message
  return String(e)
}

// relativeTime renders an ISO timestamp as a human-friendly relative
// expression for recent events ("2m ago") and a calendar string for old
// ones ("May 14"). Cheap; called inline in table rows.
function relativeTime(iso: string): string {
  const t = new Date(iso).getTime()
  const delta = Date.now() - t
  if (delta < 0) return 'just now'
  const s = Math.floor(delta / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  if (d < 7) return `${d}d ago`
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function statusLabel(s: AuditDTO['status']): string {
  switch (s) {
    case 'ok': return 'success'
    case 'denied': return 'denied'
    case 'error': return 'failed'
    case 'started': return 'running'
    case 'cancelled': return 'cancelled'
    default: return s
  }
}
