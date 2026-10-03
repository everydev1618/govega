import { fileExtIcon } from './MessageBubble'
import type { UploadedFile } from '../../lib/types'

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

// AttachmentChips shows the files already uploaded for the message being
// composed, plus a count of the ones still in flight. The workspace path
// is the title rather than the label: it's what the agent will be given,
// so it must be inspectable, but it's too long to show inline.
export function AttachmentChips({
  files,
  uploading,
  error,
  onRemove,
}: {
  files: UploadedFile[]
  uploading: number
  error: string | null
  onRemove: (path: string) => void
}) {
  if (files.length === 0 && uploading === 0 && !error) return null

  return (
    <div className="flex flex-wrap items-center gap-2 px-1 pb-2">
      {files.map(f => (
        <span
          key={f.path}
          title={f.path}
          className="inline-flex items-center gap-1.5 pl-2 pr-1 py-1 rounded-md border border-border bg-muted text-xs"
        >
          <span>{fileExtIcon(f.name)}</span>
          <span className="font-medium truncate max-w-[14rem]">{f.name}</span>
          <span className="text-muted-foreground">{formatSize(f.size)}</span>
          <button
            onClick={() => onRemove(f.path)}
            className="ml-0.5 w-4 h-4 rounded-full text-muted-foreground hover:text-foreground hover:bg-border flex items-center justify-center leading-none"
            aria-label={`Remove ${f.name}`}
          >×</button>
        </span>
      ))}
      {uploading > 0 && (
        <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md border border-border border-dashed text-xs text-muted-foreground">
          Uploading {uploading} file{uploading === 1 ? '' : 's'}…
        </span>
      )}
      {error && <span className="text-xs text-red-500">{error}</span>}
    </div>
  )
}

// DropOverlay is the "let go here" affordance. It's absolutely positioned
// and pointer-events-none so it never swallows the drop event itself.
export function DropOverlay({ label = 'Drop files to attach' }: { label?: string }) {
  return (
    <div className="absolute inset-0 z-30 pointer-events-none flex items-center justify-center rounded-xl border-2 border-dashed border-primary bg-background/85">
      <div className="flex items-center gap-2 text-sm font-medium text-primary">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 16.5V3m0 0L7.5 7.5M12 3l4.5 4.5M3 16.5v1.875A2.625 2.625 0 005.625 21h12.75A2.625 2.625 0 0021 18.375V16.5" />
        </svg>
        {label}
      </div>
    </div>
  )
}
