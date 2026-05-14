import { useEffect, useState } from 'react'
import { api, APIError } from '../lib/api'
import type { PeeringStatus } from '../lib/types'
import { PeeringModal } from './PeeringModal'

// PeeringPill is the always-visible federation indicator in the sidebar
// header. Shows the connected-peer count when peering is enabled; renders
// nothing when peering is off so non-federated installs don't see a stub.
// Click → opens the Federation modal.
export function PeeringPill() {
  const [status, setStatus] = useState<PeeringStatus | null>(null)
  const [open, setOpen] = useState(false)

  // Poll the status endpoint every 15s so the peer count stays current
  // without flooding the server. The pill itself doesn't need real-time
  // updates — the modal's Live Ops tab handles that with its own poll.
  useEffect(() => {
    let cancelled = false
    const fetchStatus = async () => {
      try {
        const s = await api.getPeeringStatus()
        if (!cancelled) setStatus(s)
      } catch (e) {
        if (e instanceof APIError && e.status === 404) {
          if (!cancelled) setStatus({ enabled: false })
        }
        // Network failures: keep the previous state rather than flicker.
      }
    }
    fetchStatus()
    const id = setInterval(fetchStatus, 15_000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  if (!status || !status.enabled) return null

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        title={`Federation — ${status.peer_count ?? 0} peer${status.peer_count === 1 ? '' : 's'} configured`}
        className="flex items-center gap-1 px-2 py-1 rounded-md text-xs font-mono bg-accent/30 hover:bg-accent/60 text-muted-foreground hover:text-foreground transition-colors"
        aria-label="Open federation panel"
      >
        <span aria-hidden>🌐</span>
        <span>{status.peer_count ?? 0}</span>
      </button>
      <PeeringModal open={open} onClose={() => setOpen(false)} status={status} />
    </>
  )
}
