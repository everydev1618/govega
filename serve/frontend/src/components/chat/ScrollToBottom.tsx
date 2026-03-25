export function ScrollToBottom({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="sticky bottom-3 left-1/2 -translate-x-1/2 w-8 h-8 rounded-full bg-accent border border-border shadow-md flex items-center justify-center text-muted-foreground hover:text-foreground transition-colors z-10"
    >
      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
      </svg>
    </button>
  )
}
