import Markdown from 'react-markdown'
import type { FileContentResponse } from '../../lib/types'
import { fileExtIcon } from './MessageBubble'

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function baseType(ct: string): string {
  return ct.split(';')[0].trim()
}

function isTextContentType(ct: string): boolean {
  if (ct.startsWith('text/')) return true
  if (['application/json', 'application/xml', 'application/javascript'].includes(ct)) return true
  return false
}

export function FilePreview({ file, onClose }: { file: FileContentResponse; onClose: () => void }) {
  const ct = baseType(file.content_type)
  const name = file.path.split('/').pop() || file.path

  return (
    <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4"
      onClick={onClose}>
      <div
        className="bg-card border border-border rounded-xl shadow-2xl w-full max-w-4xl max-h-[85vh] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
          <div className="flex items-center gap-3 min-w-0">
            <span className="text-lg">{fileExtIcon(name)}</span>
            <div className="min-w-0">
              <h3 className="font-semibold truncate">{name}</h3>
              <p className="text-xs text-muted-foreground">{ct} &middot; {formatSize(file.size)}</p>
            </div>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors p-1 rounded hover:bg-accent">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="none"><path d="M5 5l10 10M15 5L5 15" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
          </button>
        </div>
        <div className="flex-1 overflow-auto p-5">
          {ct === 'text/html' && (
            <iframe srcDoc={file.content} sandbox="allow-scripts" className="w-full h-[65vh] rounded-lg border border-border bg-white" title={name} />
          )}
          {ct === 'text/markdown' && file.encoding === 'utf-8' && (
            <div className="prose prose-invert max-w-none text-foreground leading-relaxed">
              <Markdown>{file.content}</Markdown>
            </div>
          )}
          {ct.startsWith('image/') && ct !== 'image/svg+xml' && file.encoding === 'base64' && (
            <div className="flex items-center justify-center">
              <img src={`data:${ct};base64,${file.content}`} alt={name} className="max-w-full max-h-[65vh] object-contain rounded-lg" />
            </div>
          )}
          {ct === 'image/svg+xml' && file.encoding === 'utf-8' && (
            <div className="flex items-center justify-center" dangerouslySetInnerHTML={{ __html: file.content }} />
          )}
          {isTextContentType(ct) && ct !== 'text/markdown' && ct !== 'text/html' && file.encoding === 'utf-8' && (
            <pre className="text-sm font-mono bg-black/20 rounded-lg p-4 overflow-auto max-h-[65vh] leading-relaxed">
              {file.content.split('\n').map((line, i) => (
                <div key={i} className="flex">
                  <span className="text-muted-foreground/40 select-none w-10 text-right mr-4 flex-shrink-0">{i + 1}</span>
                  <span className="flex-1 whitespace-pre-wrap break-all">{line}</span>
                </div>
              ))}
            </pre>
          )}
          {!isTextContentType(ct) && !ct.startsWith('image/') && (
            <div className="text-center py-12 text-muted-foreground">
              <p className="text-4xl mb-3">{fileExtIcon(name)}</p>
              <p className="font-medium">Binary file</p>
              <p className="text-sm mt-1">{ct} &middot; {formatSize(file.size)}</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
