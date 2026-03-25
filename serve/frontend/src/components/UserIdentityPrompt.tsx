import { useState } from 'react'

export function getUserName(): string | null {
  return localStorage.getItem('vega-user-name')
}

export function setUserName(name: string) {
  localStorage.setItem('vega-user-name', name)
}

export function UserIdentityPrompt({ onComplete }: { onComplete: (name: string) => void }) {
  const [name, setName] = useState('')

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) return
    setUserName(trimmed)
    onComplete(trimmed)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <form
        onSubmit={handleSubmit}
        className="bg-card border border-border rounded-xl p-6 w-80 space-y-4 shadow-xl"
      >
        <h2 className="text-lg font-semibold text-foreground">What's your name?</h2>
        <p className="text-sm text-muted-foreground">
          This will be shown to agents and other users in channels.
        </p>
        <input
          type="text"
          value={name}
          onChange={e => setName(e.target.value)}
          placeholder="Enter your name..."
          autoFocus
          className="w-full px-3 py-2 rounded-lg bg-background border border-border text-sm text-foreground focus:outline-none focus:border-primary"
        />
        <button
          type="submit"
          disabled={!name.trim()}
          className="w-full py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          Continue
        </button>
      </form>
    </div>
  )
}
