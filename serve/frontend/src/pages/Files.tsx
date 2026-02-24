import { useState, useEffect, useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { FileEntry, FileContentResponse, WorkspaceFileMetadata, FileMetadataResponse } from '../lib/types'

type ViewMode = 'gallery' | 'tree' | 'agents'

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function formatDate(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  const diff = now.getTime() - d.getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 604_800_000) return `${Math.floor(diff / 86_400_000)}d ago`
  return d.toLocaleDateString()
}

function fileIcon(entry: FileEntry): string {
  if (entry.is_dir) return '📁'
  const ct = entry.content_type || ''
  if (ct.startsWith('image/')) return '🖼️'
  if (ct === 'text/markdown') return '📝'
  if (ct === 'text/html') return '🌐'
  if (ct === 'application/json') return '📋'
  if (ct.startsWith('text/')) return '📄'
  return '📎'
}

function isPreviewableImage(ct: string): boolean {
  return ct.startsWith('image/') && ct !== 'image/svg+xml'
}

function isTextType(ct: string): boolean {
  if (ct.startsWith('text/')) return true
  if (['application/json', 'application/xml', 'application/javascript'].includes(ct)) return true
  return false
}

// Strip charset and other parameters from content type (e.g. "text/html; charset=utf-8" → "text/html")
function baseType(ct: string): string {
  return ct.split(';')[0].trim()
}

// Simple markdown to HTML (handles headers, bold, italic, code blocks, links, lists)
function renderMarkdown(text: string): string {
  let html = text
    // Code blocks
    .replace(/```(\w*)\n([\s\S]*?)```/g, '<pre class="bg-black/30 rounded p-3 overflow-x-auto text-sm my-2"><code>$2</code></pre>')
    // Inline code
    .replace(/`([^`]+)`/g, '<code class="bg-black/30 rounded px-1 py-0.5 text-sm">$1</code>')
    // Headers
    .replace(/^### (.+)$/gm, '<h3 class="text-base font-semibold mt-4 mb-1">$1</h3>')
    .replace(/^## (.+)$/gm, '<h2 class="text-lg font-semibold mt-4 mb-1">$1</h2>')
    .replace(/^# (.+)$/gm, '<h1 class="text-xl font-bold mt-4 mb-2">$1</h1>')
    // Bold & italic
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    // Links
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" class="text-indigo-400 underline" target="_blank" rel="noopener">$1</a>')
    // Unordered lists
    .replace(/^[*-] (.+)$/gm, '<li class="ml-4 list-disc">$1</li>')
    // Horizontal rules
    .replace(/^---$/gm, '<hr class="border-border my-3" />')
    // Line breaks (but not for block elements)
    .replace(/\n/g, '<br />')

  return html
}

// --- Breadcrumb ---
function Breadcrumb({ path, onNavigate }: { path: string; onNavigate: (p: string) => void }) {
  const parts = path ? path.split('/').filter(Boolean) : []

  return (
    <div className="flex items-center gap-1 text-sm text-muted-foreground">
      <button
        onClick={() => onNavigate('')}
        className="hover:text-foreground transition-colors font-medium"
      >
        workspace
      </button>
      {parts.map((part, i) => {
        const partPath = parts.slice(0, i + 1).join('/')
        const isLast = i === parts.length - 1
        return (
          <span key={partPath} className="flex items-center gap-1">
            <span className="text-muted-foreground/50">/</span>
            {isLast ? (
              <span className="text-foreground font-medium">{part}</span>
            ) : (
              <button
                onClick={() => onNavigate(partPath)}
                className="hover:text-foreground transition-colors"
              >
                {part}
              </button>
            )}
          </span>
        )
      })}
    </div>
  )
}

// --- File Preview Panel ---
function FilePreview({ file, onClose, onDelete }: { file: FileContentResponse | null; onClose: () => void; onDelete: (path: string) => void }) {
  if (!file) return null

  const ct = baseType(file.content_type)
  const name = file.path.split('/').pop() || file.path

  return (
    <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4"
      onClick={onClose}>
      <div
        className="bg-card border border-border rounded-xl shadow-2xl w-full max-w-4xl max-h-[85vh] flex flex-col"
        onClick={e => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
          <div className="flex items-center gap-3 min-w-0">
            <span className="text-lg">{fileIcon({ name, path: file.path, is_dir: false, size: file.size, mod_time: '', content_type: ct })}</span>
            <div className="min-w-0">
              <h3 className="font-semibold truncate">{name}</h3>
              <p className="text-xs text-muted-foreground">{ct} &middot; {formatSize(file.size)}</p>
            </div>
          </div>
          <div className="flex items-center gap-1">
            <button
              onClick={() => onDelete(file.path)}
              className="text-muted-foreground hover:text-red-400 transition-colors p-1 rounded hover:bg-red-500/10"
              title="Delete file"
            >
              <svg width="18" height="18" viewBox="0 0 14 14" fill="none">
                <path d="M2.5 3.5h9M5.5 3.5V2.5a1 1 0 011-1h1a1 1 0 011 1v1M6 6v4M8 6v4M3.5 3.5l.5 8a1 1 0 001 1h4a1 1 0 001-1l.5-8" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
            </button>
            <button
              onClick={onClose}
              className="text-muted-foreground hover:text-foreground transition-colors p-1 rounded hover:bg-accent"
            >
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none"><path d="M5 5l10 10M15 5L5 15" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
            </button>
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto p-5">
          {isPreviewableImage(ct) && file.encoding === 'base64' && (
            <div className="flex items-center justify-center">
              <img
                src={`data:${ct};base64,${file.content}`}
                alt={name}
                className="max-w-full max-h-[65vh] object-contain rounded-lg"
              />
            </div>
          )}

          {ct === 'image/svg+xml' && file.encoding === 'utf-8' && (
            <div className="flex items-center justify-center" dangerouslySetInnerHTML={{ __html: file.content }} />
          )}

          {ct === 'text/html' && (
            <iframe
              srcDoc={file.content}
              sandbox="allow-scripts"
              className="w-full h-[65vh] rounded-lg border border-border bg-white"
              title={name}
            />
          )}

          {ct === 'text/markdown' && file.encoding === 'utf-8' && (
            <div
              className="prose prose-invert max-w-none text-foreground leading-relaxed"
              dangerouslySetInnerHTML={{ __html: renderMarkdown(file.content) }}
            />
          )}

          {isTextType(ct) && ct !== 'text/markdown' && ct !== 'text/html' && file.encoding === 'utf-8' && (
            <pre className="text-sm font-mono bg-black/20 rounded-lg p-4 overflow-auto max-h-[65vh] leading-relaxed">
              {file.content.split('\n').map((line, i) => (
                <div key={i} className="flex">
                  <span className="text-muted-foreground/40 select-none w-10 text-right mr-4 flex-shrink-0">{i + 1}</span>
                  <span className="flex-1 whitespace-pre-wrap break-all">{line}</span>
                </div>
              ))}
            </pre>
          )}

          {!isTextType(ct) && !ct.startsWith('image/') && ct !== 'text/html' && (
            <div className="text-center py-12 text-muted-foreground">
              <p className="text-4xl mb-3">📎</p>
              <p className="font-medium">Binary file</p>
              <p className="text-sm mt-1">{ct} &middot; {formatSize(file.size)}</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// --- Delete Confirmation Dialog ---
function DeleteConfirm({
  path,
  onConfirm,
  onCancel,
}: {
  path: string
  onConfirm: () => void
  onCancel: () => void
}) {
  const name = path.split('/').pop() || path

  return (
    <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-[60] flex items-center justify-center p-4"
      onClick={onCancel}>
      <div className="bg-card border border-border rounded-xl shadow-2xl p-6 max-w-sm w-full"
        onClick={e => e.stopPropagation()}>
        <p className="text-lg font-semibold mb-2">Delete file?</p>
        <p className="text-sm text-muted-foreground mb-4">
          Are you sure you want to delete <span className="font-mono text-foreground">{name}</span>? This cannot be undone.
        </p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="px-4 py-2 text-sm rounded-lg bg-accent hover:bg-accent/80 transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            className="px-4 py-2 text-sm rounded-lg bg-red-600 hover:bg-red-700 text-white transition-colors"
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  )
}

// --- Delete Button ---
function DeleteButton({ onClick, className = '' }: { onClick: (e: React.MouseEvent) => void; className?: string }) {
  return (
    <button
      onClick={onClick}
      className={`text-muted-foreground/0 group-hover:text-muted-foreground hover:!text-red-400 transition-colors p-1 rounded hover:bg-red-500/10 ${className}`}
      title="Delete"
    >
      <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
        <path d="M2.5 3.5h9M5.5 3.5V2.5a1 1 0 011-1h1a1 1 0 011 1v1M6 6v4M8 6v4M3.5 3.5l.5 8a1 1 0 001 1h4a1 1 0 001-1l.5-8" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
    </button>
  )
}

// --- Gallery Card ---
function GalleryCard({
  entry,
  onNavigate,
  onPreview,
  onDelete,
}: {
  entry: FileEntry
  onNavigate: (p: string) => void
  onPreview: (p: string) => void
  onDelete: (p: string) => void
}) {
  const ct = entry.content_type || ''

  return (
    <button
      onClick={() => entry.is_dir ? onNavigate(entry.path) : onPreview(entry.path)}
      className="group bg-card border border-border rounded-xl p-4 text-left hover:border-indigo-500/50 hover:shadow-lg hover:shadow-indigo-500/5 transition-all duration-200 flex flex-col relative"
    >
      <div className="flex items-start justify-between mb-3">
        <span className="text-2xl">{fileIcon(entry)}</span>
        <div className="flex items-center gap-1">
          {!entry.is_dir && (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-accent text-muted-foreground font-mono">
              {entry.name.includes('.') ? entry.name.split('.').pop()?.toUpperCase() : ct.split('/').pop()}
            </span>
          )}
          <DeleteButton onClick={(e) => { e.stopPropagation(); onDelete(entry.path) }} />
        </div>
      </div>
      <p className="font-medium text-sm truncate group-hover:text-indigo-400 transition-colors">
        {entry.name}
      </p>
      <div className="flex items-center gap-2 mt-1 text-xs text-muted-foreground">
        {!entry.is_dir && <span>{formatSize(entry.size)}</span>}
        <span>{formatDate(entry.mod_time)}</span>
      </div>
    </button>
  )
}

// --- Tree Node ---
function TreeNode({
  entry,
  depth,
  expanded,
  onToggle,
  children: childEntries,
  selectedPath,
  onSelect,
  onNavigate,
}: {
  entry: FileEntry
  depth: number
  expanded: boolean
  onToggle: () => void
  children?: FileEntry[]
  selectedPath: string
  onSelect: (p: string) => void
  onNavigate: (p: string) => void
}) {
  const isSelected = selectedPath === entry.path

  return (
    <div>
      <button
        onClick={() => entry.is_dir ? onToggle() : onSelect(entry.path)}
        className={`w-full flex items-center gap-2 px-2 py-1.5 text-sm rounded transition-colors ${
          isSelected
            ? 'bg-indigo-500/20 text-indigo-400'
            : 'hover:bg-accent text-foreground'
        }`}
        style={{ paddingLeft: `${depth * 16 + 8}px` }}
      >
        {entry.is_dir ? (
          <span className="text-muted-foreground w-4 text-center text-xs">
            {expanded ? '▾' : '▸'}
          </span>
        ) : (
          <span className="w-4" />
        )}
        <span>{fileIcon(entry)}</span>
        <span className="truncate flex-1 text-left">{entry.name}</span>
        {!entry.is_dir && (
          <span className="text-xs text-muted-foreground">{formatSize(entry.size)}</span>
        )}
      </button>
      {entry.is_dir && expanded && childEntries && (
        <TreeChildren
          entries={childEntries}
          depth={depth + 1}
          selectedPath={selectedPath}
          onSelect={onSelect}
          onNavigate={onNavigate}
        />
      )}
    </div>
  )
}

function TreeChildren({
  entries,
  depth,
  selectedPath,
  onSelect,
  onNavigate,
}: {
  entries: FileEntry[]
  depth: number
  selectedPath: string
  onSelect: (p: string) => void
  onNavigate: (p: string) => void
}) {
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set())
  const [dirContents, setDirContents] = useState<Record<string, FileEntry[]>>({})

  const toggleDir = useCallback(async (path: string) => {
    setExpandedDirs(prev => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
      } else {
        next.add(path)
        // Load contents if not already loaded
        if (!dirContents[path]) {
          api.getFiles(path).then(files => {
            setDirContents(prev => ({ ...prev, [path]: files }))
          })
        }
      }
      return next
    })
  }, [dirContents])

  const dirs = entries.filter(e => e.is_dir).sort((a, b) => a.name.localeCompare(b.name))
  const files = entries.filter(e => !e.is_dir).sort((a, b) => a.name.localeCompare(b.name))

  return (
    <div>
      {[...dirs, ...files].map(entry => (
        <TreeNode
          key={entry.path}
          entry={entry}
          depth={depth}
          expanded={expandedDirs.has(entry.path)}
          onToggle={() => toggleDir(entry.path)}
          children={dirContents[entry.path]}
          selectedPath={selectedPath}
          onSelect={onSelect}
          onNavigate={onNavigate}
        />
      ))}
    </div>
  )
}

// --- Inline Preview for Tree View ---
function InlinePreview({ path }: { path: string }) {
  const [file, setFile] = useState<FileContentResponse | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    api.getFileContent(path).then(f => {
      setFile(f)
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [path])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground">
        <div className="animate-pulse">Loading...</div>
      </div>
    )
  }

  if (!file) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground">
        Failed to load file
      </div>
    )
  }

  const ct = baseType(file.content_type)
  const name = file.path.split('/').pop() || file.path

  return (
    <div className="h-full flex flex-col">
      <div className="flex items-center gap-3 px-4 py-3 border-b border-border">
        <span>{fileIcon({ name, path: file.path, is_dir: false, size: file.size, mod_time: '', content_type: ct })}</span>
        <span className="font-medium text-sm truncate">{file.path}</span>
        <span className="text-xs text-muted-foreground ml-auto">{formatSize(file.size)}</span>
      </div>
      <div className="flex-1 overflow-auto p-4">
        {isPreviewableImage(ct) && file.encoding === 'base64' && (
          <div className="flex items-center justify-center h-full">
            <img src={`data:${ct};base64,${file.content}`} alt={name} className="max-w-full max-h-full object-contain rounded" />
          </div>
        )}
        {ct === 'image/svg+xml' && file.encoding === 'utf-8' && (
          <div className="flex items-center justify-center h-full" dangerouslySetInnerHTML={{ __html: file.content }} />
        )}
        {ct === 'text/html' && (
          <iframe srcDoc={file.content} sandbox="allow-scripts" className="w-full h-full rounded border border-border bg-white" title={name} />
        )}
        {ct === 'text/markdown' && file.encoding === 'utf-8' && (
          <div className="prose prose-invert max-w-none text-foreground leading-relaxed" dangerouslySetInnerHTML={{ __html: renderMarkdown(file.content) }} />
        )}
        {isTextType(ct) && ct !== 'text/markdown' && ct !== 'text/html' && file.encoding === 'utf-8' && (
          <pre className="text-sm font-mono leading-relaxed">
            {file.content.split('\n').map((line, i) => (
              <div key={i} className="flex">
                <span className="text-muted-foreground/40 select-none w-10 text-right mr-4 flex-shrink-0">{i + 1}</span>
                <span className="flex-1 whitespace-pre-wrap break-all">{line}</span>
              </div>
            ))}
          </pre>
        )}
        {!isTextType(ct) && !ct.startsWith('image/') && ct !== 'text/html' && (
          <div className="flex flex-col items-center justify-center h-full text-muted-foreground">
            <p className="text-4xl mb-3">📎</p>
            <p className="font-medium">Binary file</p>
            <p className="text-sm mt-1">{ct} &middot; {formatSize(file.size)}</p>
          </div>
        )}
      </div>
    </div>
  )
}

// --- By Agent View ---
function AgentFileCard({ file, onPreview, onDelete }: { file: WorkspaceFileMetadata; onPreview: (path: string) => void; onDelete: (path: string) => void }) {
  const name = file.path.split('/').pop() || file.path
  const ext = name.includes('.') ? name.split('.').pop()?.toUpperCase() : ''

  return (
    <button
      onClick={() => onPreview(file.path)}
      className="group bg-card border border-border rounded-xl p-4 text-left hover:border-indigo-500/50 hover:shadow-lg hover:shadow-indigo-500/5 transition-all duration-200 flex flex-col"
    >
      <div className="flex items-start justify-between mb-2">
        <span className="text-lg">📄</span>
        <div className="flex items-center gap-1.5">
          <span className={`text-[10px] px-1.5 py-0.5 rounded font-mono ${
            file.operation === 'append' ? 'bg-amber-500/20 text-amber-400' : 'bg-accent text-muted-foreground'
          }`}>
            {file.operation === 'append' ? 'APPEND' : ext}
          </span>
          <DeleteButton onClick={(e) => { e.stopPropagation(); onDelete(file.path) }} />
        </div>
      </div>
      <p className="font-medium text-sm truncate group-hover:text-indigo-400 transition-colors">{name}</p>
      {file.description && (
        <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{file.description}</p>
      )}
      <div className="flex items-center gap-2 mt-auto pt-2 text-xs text-muted-foreground">
        <span>{formatDate(file.created_at)}</span>
      </div>
    </button>
  )
}

function AgentSection({
  agent,
  files,
  onPreview,
  onDelete,
}: {
  agent: string
  files: WorkspaceFileMetadata[]
  onPreview: (path: string) => void
  onDelete: (path: string) => void
}) {
  const [collapsed, setCollapsed] = useState(false)

  return (
    <div className="border border-border rounded-xl overflow-hidden">
      <button
        onClick={() => setCollapsed(!collapsed)}
        className="w-full flex items-center justify-between px-5 py-3 bg-card hover:bg-accent/50 transition-colors"
      >
        <div className="flex items-center gap-3">
          <span className="text-muted-foreground text-sm">{collapsed ? '▸' : '▾'}</span>
          <span className="font-semibold">{agent || 'Unknown Agent'}</span>
          <span className="text-xs text-muted-foreground bg-accent rounded-full px-2 py-0.5">
            {files.length} file{files.length !== 1 ? 's' : ''}
          </span>
        </div>
      </button>
      {!collapsed && (
        <div className="p-4 grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 gap-3">
          {files.map(file => (
            <AgentFileCard key={file.id} file={file} onPreview={onPreview} onDelete={onDelete} />
          ))}
        </div>
      )}
    </div>
  )
}

// --- Main Component ---
export function Files() {
  const [searchParams] = useSearchParams()
  const agentFilter = searchParams.get('agent')
  const [viewMode, setViewMode] = useState<ViewMode>(agentFilter ? 'agents' : 'gallery')
  const [currentPath, setCurrentPath] = useState('')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [previewFile, setPreviewFile] = useState<FileContentResponse | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [treeSelectedPath, setTreeSelectedPath] = useState('')
  const [metadata, setMetadata] = useState<FileMetadataResponse | null>(null)
  const [metadataLoading, setMetadataLoading] = useState(false)

  const loadEntries = useCallback(async (path: string) => {
    setLoading(true)
    setError(null)
    try {
      const data = await api.getFiles(path || undefined)
      setEntries(data)
    } catch (e: any) {
      setError(e.message || 'Failed to load files')
      setEntries([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadEntries(currentPath)
  }, [currentPath, loadEntries])

  useEffect(() => {
    if (viewMode === 'agents') {
      setMetadataLoading(true)
      api.getFileMetadata(agentFilter || undefined).then(data => {
        setMetadata(data)
      }).catch(() => {
        setMetadata(null)
      }).finally(() => setMetadataLoading(false))
    }
  }, [viewMode, agentFilter])

  const navigate = useCallback((path: string) => {
    setCurrentPath(path)
    setTreeSelectedPath('')
  }, [])

  const [previewError, setPreviewError] = useState<string | null>(null)

  const openPreview = useCallback(async (path: string) => {
    setPreviewLoading(true)
    setPreviewError(null)
    try {
      const file = await api.getFileContent(path)
      setPreviewFile(file)
    } catch (e: any) {
      setPreviewError(e.message || `Failed to load ${path}`)
    } finally {
      setPreviewLoading(false)
    }
  }, [])

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  const handleDelete = useCallback(async (path: string) => {
    try {
      await api.deleteFile(path)
      // Refresh the current view
      loadEntries(currentPath)
      // Close preview if we deleted the previewed file
      if (previewFile && previewFile.path === path) {
        setPreviewFile(null)
      }
      if (treeSelectedPath === path) {
        setTreeSelectedPath('')
      }
      // Refresh metadata if in agents view
      if (viewMode === 'agents') {
        api.getFileMetadata(agentFilter || undefined).then(setMetadata).catch(() => {})
      }
    } catch (e: any) {
      setError(e.message || 'Failed to delete file')
    }
    setDeleteTarget(null)
  }, [currentPath, loadEntries, previewFile, treeSelectedPath, viewMode, agentFilter])

  const dirs = entries.filter(e => e.is_dir).sort((a, b) => a.name.localeCompare(b.name))
  const files = entries.filter(e => !e.is_dir).sort((a, b) => a.name.localeCompare(b.name))
  const sorted = [...dirs, ...files]

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-bold">Files</h2>
          <p className="text-sm text-muted-foreground mt-0.5">
            {agentFilter
              ? <>Files by <span className="text-indigo-400 font-medium">{agentFilter}</span></>
              : 'Browse workspace files created by agents'}
          </p>
        </div>
        <div className="flex items-center gap-1 bg-accent rounded-lg p-0.5">
          <button
            onClick={() => setViewMode('gallery')}
            className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
              viewMode === 'gallery'
                ? 'bg-card text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            <span className="flex items-center gap-1.5">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><rect x="1" y="1" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5"/><rect x="8" y="1" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5"/><rect x="1" y="8" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5"/><rect x="8" y="8" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5"/></svg>
              Gallery
            </span>
          </button>
          <button
            onClick={() => setViewMode('tree')}
            className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
              viewMode === 'tree'
                ? 'bg-card text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            <span className="flex items-center gap-1.5">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M2 2h10M2 5h7M2 8h10M2 11h5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg>
              Tree
            </span>
          </button>
          <button
            onClick={() => setViewMode('agents')}
            className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
              viewMode === 'agents'
                ? 'bg-card text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            <span className="flex items-center gap-1.5">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><circle cx="5" cy="4" r="2.5" stroke="currentColor" strokeWidth="1.5"/><path d="M1 12c0-2.2 1.8-4 4-4s4 1.8 4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/><circle cx="10.5" cy="5.5" r="1.5" stroke="currentColor" strokeWidth="1.2"/><path d="M9 12c0-1.4.9-2.5 2-2.8" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/></svg>
              By Agent
            </span>
          </button>
        </div>
      </div>

      {/* Breadcrumb */}
      <Breadcrumb path={currentPath} onNavigate={navigate} />

      {/* Error */}
      {error && (
        <div className="bg-red-900/20 border border-red-500/30 rounded-lg px-4 py-3 text-sm text-red-400">
          {error}
        </div>
      )}

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-20 text-muted-foreground">
          <div className="animate-pulse">Loading files...</div>
        </div>
      )}

      {/* Empty state */}
      {!loading && !error && entries.length === 0 && (
        <div className="flex flex-col items-center justify-center py-20 text-muted-foreground">
          <p className="text-4xl mb-3">📂</p>
          <p className="font-medium">No files yet</p>
          <p className="text-sm mt-1">Files created by agents will appear here</p>
        </div>
      )}

      {/* Gallery View */}
      {!loading && viewMode === 'gallery' && entries.length > 0 && (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-3">
          {/* Parent directory */}
          {currentPath && (
            <button
              onClick={() => {
                const parent = currentPath.split('/').slice(0, -1).join('/')
                navigate(parent)
              }}
              className="group bg-card border border-border border-dashed rounded-xl p-4 text-left hover:border-indigo-500/50 transition-all duration-200 flex flex-col"
            >
              <span className="text-2xl mb-3">⬆️</span>
              <p className="font-medium text-sm text-muted-foreground group-hover:text-indigo-400 transition-colors">..</p>
            </button>
          )}
          {sorted.map(entry => (
            <GalleryCard
              key={entry.path}
              entry={entry}
              onNavigate={navigate}
              onPreview={openPreview}
              onDelete={setDeleteTarget}
            />
          ))}
        </div>
      )}

      {/* Tree View */}
      {!loading && viewMode === 'tree' && entries.length > 0 && (
        <div className="flex gap-4 min-h-[60vh]">
          {/* Tree sidebar */}
          <div className="w-72 flex-shrink-0 bg-card border border-border rounded-xl overflow-auto">
            <div className="p-2">
              {currentPath && (
                <button
                  onClick={() => {
                    const parent = currentPath.split('/').slice(0, -1).join('/')
                    navigate(parent)
                  }}
                  className="w-full flex items-center gap-2 px-2 py-1.5 text-sm rounded hover:bg-accent text-muted-foreground"
                >
                  <span className="w-4" />
                  <span>⬆️</span>
                  <span>..</span>
                </button>
              )}
              <TreeChildren
                entries={sorted}
                depth={0}
                selectedPath={treeSelectedPath}
                onSelect={(p) => setTreeSelectedPath(p)}
                onNavigate={navigate}
              />
            </div>
          </div>

          {/* Preview panel */}
          <div className="flex-1 bg-card border border-border rounded-xl overflow-hidden">
            {treeSelectedPath ? (
              <InlinePreview path={treeSelectedPath} />
            ) : (
              <div className="flex items-center justify-center h-full text-muted-foreground">
                <p className="text-sm">Select a file to preview</p>
              </div>
            )}
          </div>
        </div>
      )}

      {/* By Agent View */}
      {viewMode === 'agents' && metadataLoading && (
        <div className="flex items-center justify-center py-20 text-muted-foreground">
          <div className="animate-pulse">Loading file metadata...</div>
        </div>
      )}
      {viewMode === 'agents' && !metadataLoading && metadata && metadata.files.length === 0 && (
        <div className="flex flex-col items-center justify-center py-20 text-muted-foreground">
          <p className="text-4xl mb-3">📂</p>
          <p className="font-medium">No tracked files yet</p>
          <p className="text-sm mt-1">Files written by agents via write_file/append_file will appear here</p>
        </div>
      )}
      {viewMode === 'agents' && !metadataLoading && metadata && metadata.files.length > 0 && (
        <div className="space-y-4">
          {metadata.agents.map(agent => {
            const agentFiles = metadata.files.filter(f => f.agent === agent)
            if (agentFiles.length === 0) return null
            return (
              <AgentSection
                key={agent}
                agent={agent}
                files={agentFiles}
                onPreview={openPreview}
                onDelete={setDeleteTarget}
              />
            )
          })}
          {/* Files with no agent */}
          {metadata.files.some(f => !f.agent || !metadata.agents.includes(f.agent)) && (
            <AgentSection
              agent=""
              files={metadata.files.filter(f => !f.agent || !metadata.agents.includes(f.agent))}
              onPreview={openPreview}
              onDelete={setDeleteTarget}
            />
          )}
        </div>
      )}

      {/* Preview modal (gallery mode) */}
      {previewLoading && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center">
          <div className="animate-pulse text-white">Loading preview...</div>
        </div>
      )}
      {previewError && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4"
          onClick={() => setPreviewError(null)}>
          <div className="bg-card border border-red-500/30 rounded-xl shadow-2xl p-6 max-w-md text-center"
            onClick={e => e.stopPropagation()}>
            <p className="text-4xl mb-3">⚠️</p>
            <p className="font-medium text-red-400 mb-1">Failed to load file</p>
            <p className="text-sm text-muted-foreground">{previewError}</p>
            <button
              onClick={() => setPreviewError(null)}
              className="mt-4 px-4 py-2 text-sm bg-accent hover:bg-accent/80 rounded-lg transition-colors"
            >
              Close
            </button>
          </div>
        </div>
      )}
      <FilePreview file={previewFile} onClose={() => setPreviewFile(null)} onDelete={(path) => { setPreviewFile(null); setDeleteTarget(path) }} />
      {deleteTarget && (
        <DeleteConfirm
          path={deleteTarget}
          onConfirm={() => handleDelete(deleteTarget)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
