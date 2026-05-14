import { useEffect, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

interface ModalProps {
  open: boolean
  onClose: () => void
  title?: string
  children: ReactNode
  // Tailwind width override; defaults to a reading-friendly mid size.
  // Use 'w-[600px]' for typical detail views; 'w-[480px]' for compact forms.
  widthClass?: string
}

// Modal is the project-wide convention for detail and form surfaces.
// Detail views (clicking a row, card, or item to inspect it) are modals,
// NOT side panels — side panels squeeze the main content and bury the
// detail in a narrow column. Modals dim the page, focus the user, and
// scale better at all viewports.
//
// Rendered through a portal into document.body so the overlay escapes any
// ancestor with `transform` / `filter` / `will-change`, which would
// otherwise create a containing block for `position: fixed` and pin the
// modal inside (e.g. the slide-in sidebar in Layout.tsx clips child modals
// without this).
export function Modal({ open, onClose, title, children, widthClass = 'w-[600px]' }: ModalProps) {
  // Close on Escape — standard accelerator users expect.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return createPortal(
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
    >
      <div
        onClick={e => e.stopPropagation()}
        className={`bg-card border border-border rounded-lg p-5 ${widthClass} max-w-[90vw] max-h-[85vh] flex flex-col`}
      >
        {title !== undefined && (
          <div className="flex items-center justify-between mb-4 shrink-0">
            <h3 className="font-bold truncate">{title}</h3>
            <button
              onClick={onClose}
              aria-label="Close"
              className="text-muted-foreground hover:text-foreground text-xl leading-none"
            >
              &times;
            </button>
          </div>
        )}
        <div className="overflow-auto flex-1 min-h-0">{children}</div>
      </div>
    </div>,
    document.body,
  )
}
