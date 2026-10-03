import { useEffect, useState } from 'react'
import { Modal } from './Modal'
import { api, APIError } from '../lib/api'
import { shouldPromptForPublicURL, suggestedPublicURL } from '../lib/onboarding'
import type { OnboardingStatus } from '../lib/types'

// First-run setup. Asks only what the server could not deduce.
//
// A Vega bound to 0.0.0.0 has no way to know its own public name — everything
// it can see locally says "localhost", so that is what agents put in the
// links they hand you, and those links are dead everywhere but the server's
// own machine. Reaching the dashboard on a real hostname is itself the
// answer, and the server learns it silently; this modal appears only when
// the instance is exposed and nobody has yet reached it by anything but
// localhost.
export function OnboardingGate() {
  const [status, setStatus] = useState<OnboardingStatus | null>(null)
  const [url, setUrl] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    api.getOnboarding()
      .then(s => {
        if (cancelled) return
        setStatus(s)
        setUrl(suggestedPublicURL(s, window.location.origin))
      })
      .catch(() => { /* an older server has no /onboarding — stay quiet */ })
    return () => { cancelled = true }
  }, [])

  if (!shouldPromptForPublicURL(status)) return null

  const submit = async (body: { public_url?: string; dismiss?: boolean }) => {
    setSaving(true)
    setError(null)
    try {
      setStatus(await api.setOnboarding(body))
    } catch (e) {
      setError(e instanceof APIError ? e.message : 'Could not save that URL.')
    } finally {
      setSaving(false)
    }
  }

  return (
    // Not dismissible by backdrop or Escape: both ways out are explicit
    // buttons, so closing it is always a decision rather than an accident.
    <Modal open onClose={() => {}} title="How do people reach this Vega?" widthClass="w-[520px]">
      <div className="space-y-4 text-sm">
        <p className="text-muted-foreground">
          Agents put links to their work in chat — documents, dashboards, apps.
          This server is reachable from other machines, but it can only see
          itself as{' '}
          <code className="px-1 py-0.5 rounded bg-background border border-border">
            {status!.public_url}
          </code>
          , so that is the address those links would carry, and it works
          nowhere but this machine.
        </p>

        <label className="block space-y-1.5">
          <span className="font-medium">Base URL</span>
          <input
            type="text"
            value={url}
            autoFocus
            onChange={e => { setUrl(e.target.value); setError(null) }}
            onKeyDown={e => { if (e.key === 'Enter' && url.trim()) submit({ public_url: url }) }}
            placeholder="http://vega.const"
            className="w-full px-3 py-2 rounded-lg bg-background border border-border text-sm text-foreground focus:outline-none focus:border-primary"
          />
          <span className="block text-xs text-muted-foreground">
            Host only, no path — e.g. <code>http://vega.const</code> or{' '}
            <code>https://vega.example.com</code>.
          </span>
        </label>

        {status!.suggestions && status!.suggestions.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {status!.suggestions.map(s => (
              <button
                key={s}
                type="button"
                onClick={() => setUrl(s)}
                className="px-2 py-1 rounded-md border border-border text-xs text-muted-foreground hover:text-foreground hover:border-primary"
              >
                {s}
              </button>
            ))}
          </div>
        )}

        {error && <p className="text-xs text-red-400">{error}</p>}

        <div className="flex items-center justify-between gap-3 pt-1">
          <button
            type="button"
            disabled={saving}
            onClick={() => submit({ dismiss: true })}
            className="text-xs text-muted-foreground hover:text-foreground disabled:opacity-50"
          >
            localhost is fine — don't ask again
          </button>
          <button
            type="button"
            disabled={saving || !url.trim()}
            onClick={() => submit({ public_url: url })}
            className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>

        <p className="text-xs text-muted-foreground">
          You can change this later in Settings, or by setting{' '}
          <code>PUBLIC_URL</code>.
        </p>
      </div>
    </Modal>
  )
}
