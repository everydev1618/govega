export function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    running: 'bg-blue-900/50 text-blue-400',
    pending: 'bg-yellow-900/50 text-yellow-400',
    completed: 'bg-green-900/50 text-green-400',
    failed: 'bg-red-900/50 text-red-400',
    timeout: 'bg-orange-900/50 text-orange-400',
  }
  return (
    <span className={`text-xs px-2 py-0.5 rounded ${colors[status] || 'bg-muted text-muted-foreground'}`}>
      {status}
    </span>
  )
}
