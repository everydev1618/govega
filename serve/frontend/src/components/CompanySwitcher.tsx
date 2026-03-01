import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import type { CompanyResponse } from '../lib/types'

export function CompanySwitcher() {
  const [company, setCompany] = useState<CompanyResponse | null>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    api.getCompany().then(setCompany).catch(() => {})
  }, [])

  const name = company?.name || 'Vega'
  const logoUrl = company?.logo_url
  const siblings = company?.siblings || []
  const hasSiblings = siblings.length > 0
  const accentColor = company?.accent_color

  const initial = name.charAt(0).toUpperCase()

  return (
    <div className="relative">
      <button
        onClick={() => hasSiblings && setOpen(!open)}
        className={`flex items-center gap-2.5 w-full text-left px-1 py-1 rounded-lg transition-colors ${
          hasSiblings ? 'hover:bg-accent/50 cursor-pointer' : 'cursor-default'
        }`}
      >
        {/* Logo or letter avatar */}
        {logoUrl ? (
          <img
            src={logoUrl}
            alt={name}
            className="w-8 h-8 rounded-lg object-contain bg-accent/30 shrink-0"
          />
        ) : (
          <div
            className="w-8 h-8 rounded-lg flex items-center justify-center text-sm font-bold shrink-0"
            style={accentColor
              ? { backgroundColor: accentColor + '20', color: accentColor }
              : undefined
            }
          >
            {!accentColor && (
              <span className="bg-gradient-to-br from-indigo-400 to-purple-400 bg-clip-text text-transparent">
                {initial}
              </span>
            )}
            {accentColor && initial}
          </div>
        )}

        <div className="min-w-0 flex-1">
          <div className="text-sm font-semibold truncate">{name}</div>
          <div className="text-[11px] text-muted-foreground leading-tight">
            Agent Dashboard
          </div>
        </div>

        {hasSiblings && (
          <svg
            className={`w-4 h-4 text-muted-foreground shrink-0 transition-transform ${open ? 'rotate-180' : ''}`}
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        )}
      </button>

      {/* Dropdown */}
      {open && hasSiblings && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute left-0 right-0 top-full mt-1 z-50 bg-popover border border-border rounded-lg shadow-lg py-1">
            {/* Current instance */}
            <div className="px-3 py-2 text-xs text-muted-foreground font-medium">
              Current
            </div>
            <div className="px-3 py-1.5 text-sm font-medium flex items-center gap-2">
              <div className="w-2 h-2 rounded-full bg-green-500" />
              {name}
            </div>
            <div className="my-1 border-t border-border" />
            <div className="px-3 py-2 text-xs text-muted-foreground font-medium">
              Switch to
            </div>
            {siblings.map((sib) => (
              <a
                key={sib.url}
                href={sib.url}
                className="block px-3 py-1.5 text-sm hover:bg-accent/50 transition-colors flex items-center gap-2"
              >
                {sib.icon ? (
                  <span className="text-base">{sib.icon}</span>
                ) : (
                  <span className="w-5 h-5 rounded text-[10px] font-bold flex items-center justify-center bg-accent/30 text-muted-foreground">
                    {sib.name.charAt(0).toUpperCase()}
                  </span>
                )}
                {sib.name}
              </a>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
