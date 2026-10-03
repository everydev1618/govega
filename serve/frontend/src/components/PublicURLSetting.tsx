import { useEffect, useState } from 'react'
import { api, APIError } from '../lib/api'
import type { OnboardingStatus } from '../lib/types'

const SOURCE_LABEL: Record<OnboardingStatus['public_url_source'], string> = {
  config: 'set by PUBLIC_URL or --public-url',
  explicit: 'set here',
  observed: 'detected from how you reached this dashboard',
  fallback: 'not set — this only works on the server itself',
}

// The base URL agents put in the links they hand you. Normally detected, but
// editable: an instance reached by two names (tailnet and reverse proxy, say)
// should hand out the one you want people to use.
export function PublicURLSetting() {
  const [status, setStatus] = useState<OnboardingStatus | null>(null)
  const [url, setUrl] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    api.getOnboarding()
      .then(s => { if (!cancelled) { setStatus(s); setUrl(s.public_url) } })
      .catch(() => { /* older server without /onboarding */ })
    return () => { cancelled = true }
  }, [])

  if (!status) return null

  const locked = status.public_url_source === 'config'

  const save = async () => {
    setSaving(true)
    setError(null)
    setSaved(false)
    try {
      const next = await api.setOnboarding({ public_url: url })
      setStatus(next)
      setUrl(next.public_url)
      setSaved(true)
    } catch (e) {
      setError(e instanceof APIError ? e.message : 'Could not save that URL.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="p-4 rounded-lg bg-card border border-border space-y-3">
      <div>
        <h3 className="font-semibold text-sm">Public URL</h3>
        <p className="text-xs text-muted-foreground">
          The address agents put in links to their work — documents, dashboards,
          deployed apps. Currently {SOURCE_LABEL[status.public_url_source]}.
        </p>
      </div>
      <div className="flex flex-col sm:flex-row gap-3">
        <input
          type="text"
          value={url}
          disabled={locked}
          onChange={e => { setUrl(e.target.value); setError(null); setSaved(false) }}
          onKeyDown={e => { if (e.key === 'Enter' && !locked && url.trim()) save() }}
          placeholder="http://vega.const"
          className="flex-1 px-3 py-2 rounded bg-background border border-border text-sm font-mono disabled:opacity-50"
        />
        <button
          onClick={save}
          disabled={locked || saving || !url.trim() || url === status.public_url}
          className="px-4 py-2 rounded bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
      {locked && (
        <p className="text-xs text-muted-foreground">
          Pinned by the environment. Unset <code>PUBLIC_URL</code> (and{' '}
          <code>--public-url</code>) to edit it here.
        </p>
      )}
      {error && <p className="text-xs text-red-400">{error}</p>}
      {saved && <p className="text-xs text-green-400">Saved. New links will use it.</p>}
    </div>
  )
}
