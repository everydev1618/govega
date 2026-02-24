import { useState } from 'react'
import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'

export function Settings() {
  const { data: settings, loading, refetch } = useAPI(() => api.getSettings())

  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const [sensitive, setSensitive] = useState(false)
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSave = async () => {
    if (!key.trim()) return
    setSaving(true)
    setError(null)
    try {
      await api.upsertSetting(key.trim(), value, sensitive)
      setKey('')
      setValue('')
      setSensitive(false)
      setEditingKey(null)
      refetch()
    } catch (err: any) {
      setError(err.message || 'Failed to save setting')
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (k: string) => {
    try {
      await api.deleteSetting(k)
      refetch()
    } catch (err: any) {
      setError(err.message || 'Failed to delete setting')
    }
  }

  const handleEdit = (s: { key: string; value: string; sensitive: boolean }) => {
    setKey(s.key)
    setValue(s.sensitive ? '' : s.value)
    setSensitive(s.sensitive)
    setEditingKey(s.key)
  }

  const handleCancel = () => {
    setKey('')
    setValue('')
    setSensitive(false)
    setEditingKey(null)
  }

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-2xl font-bold">Settings</h2>
        <p className="text-sm text-muted-foreground">
          Key-value settings injected into dynamic tool templates via {'{{.KEY}}'}.
        </p>
      </div>

      {error && (
        <div className="p-3 rounded bg-red-900/30 text-red-400 text-sm">{error}</div>
      )}

      {/* Add / Edit form */}
      <div className="p-4 rounded-lg bg-card border border-border space-y-3">
        <h3 className="font-semibold text-sm">{editingKey ? 'Edit Setting' : 'Add Setting'}</h3>
        <div className="flex flex-col sm:flex-row gap-3">
          <input
            type="text"
            placeholder="KEY"
            value={key}
            onChange={(e) => setKey(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, ''))}
            disabled={editingKey !== null}
            className="flex-1 px-3 py-2 rounded bg-background border border-border text-sm font-mono disabled:opacity-50"
          />
          <input
            type={sensitive ? 'password' : 'text'}
            placeholder="Value"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            className="flex-[2] px-3 py-2 rounded bg-background border border-border text-sm font-mono"
          />
          <label className="flex items-center gap-2 text-sm text-muted-foreground whitespace-nowrap">
            <input
              type="checkbox"
              checked={sensitive}
              onChange={(e) => setSensitive(e.target.checked)}
              className="rounded"
            />
            Sensitive
          </label>
          <div className="flex gap-2">
            <button
              onClick={handleSave}
              disabled={saving || !key.trim()}
              className="px-4 py-2 rounded bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium disabled:opacity-50 transition-colors"
            >
              {saving ? 'Saving...' : 'Save'}
            </button>
            {editingKey && (
              <button
                onClick={handleCancel}
                className="px-4 py-2 rounded bg-muted hover:bg-muted/80 text-sm transition-colors"
              >
                Cancel
              </button>
            )}
          </div>
        </div>
      </div>

      {/* Settings table */}
      {!settings || settings.length === 0 ? (
        <p className="text-muted-foreground text-sm">No settings configured.</p>
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-muted/50 border-b border-border">
                <th className="text-left px-4 py-2 font-medium">Key</th>
                <th className="text-left px-4 py-2 font-medium">Value</th>
                <th className="text-left px-4 py-2 font-medium">Sensitive</th>
                <th className="text-right px-4 py-2 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {settings.map((s) => (
                <tr key={s.key} className="border-b border-border last:border-0 hover:bg-muted/30">
                  <td className="px-4 py-2 font-mono">{s.key}</td>
                  <td className="px-4 py-2 font-mono text-muted-foreground">
                    {s.sensitive ? '********' : s.value}
                  </td>
                  <td className="px-4 py-2">
                    {s.sensitive && (
                      <span className="text-xs px-2 py-0.5 rounded bg-yellow-900/40 text-yellow-400">
                        yes
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-right space-x-2">
                    <button
                      onClick={() => handleEdit(s)}
                      className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
                    >
                      Edit
                    </button>
                    <button
                      onClick={() => handleDelete(s.key)}
                      className="text-xs text-red-400 hover:text-red-300 transition-colors"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
