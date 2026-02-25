import { useState, useRef, useEffect, useCallback, type ReactNode } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import Markdown from 'react-markdown'
import { useSSE } from '../hooks/useSSE'
import { api, APIError } from '../lib/api'
import type { AgentResponse, ChatEvent, ChatEventMetrics, ToolCallState, FileContentResponse } from '../lib/types'

const HERMES = 'hermes'
const META_AGENTS = new Set(['hermes', 'mother'])

// Matches the handoff line Hermes emits: → Handing you to **agent-name** for this conversation.
const HANDOFF_RE = /→\s+Handing you to \*\*([^*]+)\*\*/

function classifyErrorType(msg: string): 'auth' | 'rate_limit' | 'generic' {
  const lower = msg.toLowerCase()
  if (lower.includes('api key') || lower.includes('unauthorized') || lower.includes('authentication') || lower.includes('401'))
    return 'auth'
  if (lower.includes('rate limit') || lower.includes('429'))
    return 'rate_limit'
  return 'generic'
}

function classifyErrorTypeFromStatus(status: number): 'auth' | 'rate_limit' | 'generic' {
  if (status === 401) return 'auth'
  if (status === 429) return 'rate_limit'
  return 'generic'
}

const starterPrompts = [
  'What agents do I have and what can they do?',
  'Create me a research agent that can search the web',
  'Schedule a daily news summary and email it to me',
]

interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  toolCalls?: ToolCallState[]
  streaming?: boolean
  error?: string
  errorType?: 'auth' | 'rate_limit' | 'generic'
  metrics?: ChatEventMetrics
}

function statusDotClass(tc: ToolCallState): string {
  return tc.status === 'running' ? 'bg-yellow-400 animate-pulse'
    : tc.status === 'error' ? 'bg-red-400' : 'bg-green-400'
}

function shortToolName(name: string): string {
  const idx = name.indexOf('__')
  return idx >= 0 ? name.slice(idx + 2) : name
}

const NODE_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24']

function toolNarrative(name: string, args: Record<string, unknown>): string {
  switch (name) {
    case 'send_to_agent': return `Traveling to ${args.agent || 'an agent'}...`
    case 'delegate': return `Delegating to ${args.agent || 'an agent'}...`
    case 'list_agents': return 'Surveying the universe...'
    case 'remember': return 'Committing to memory...'
    case 'recall': return 'Searching memories...'
    case 'forget': return 'Letting go...'
    case 'set_project': return `Opening ${args.name || 'project'} workspace...`
    case 'list_projects': return 'Checking project workspaces...'
    case 'write_file': return 'Writing a file...'
    case 'read_file': return 'Reading a file...'
    case 'list_files': return 'Browsing files...'
    case 'exec': return 'Running a command...'
    default: return `Using ${name}...`
  }
}

function ActivityConstellation({ tools }: { tools: ToolCallState[] }) {
  const cx = 20, cy = 20
  const orbitR = 12

  return (
    <svg width="40" height="40" viewBox="0 0 40 40" className="block">
      {/* Central planet */}
      <circle cx={cx} cy={cy} r={5} fill="#a78bfa" opacity={0.12} className="constellation-core" />
      <circle cx={cx} cy={cy} r={3} fill="#a78bfa" opacity={0.7} />

      {/* Orbiting tool dots — all orbit while stream is active */}
      {tools.map((tc, i) => {
        const color = NODE_COLORS[i % NODE_COLORS.length]
        const startAngle = (360 * i) / Math.max(tools.length, 1)
        const dur = 3 + i * 0.7

        return (
          <g key={tc.id}>
            <animateTransform
              attributeName="transform" type="rotate"
              from={`${startAngle} ${cx} ${cy}`}
              to={`${startAngle + 360} ${cx} ${cy}`}
              dur={`${dur}s`}
              repeatCount="indefinite"
            />
            <circle
              cx={cx + orbitR} cy={cy}
              r={2.5} fill={color} opacity={0.8}
            />
          </g>
        )
      })}
    </svg>
  )
}

function ActivityNarrative({ tools }: { tools: ToolCallState[] }) {
  const runningNested = [...tools].reverse().find(t => t.status === 'running' && t.nested_agent)
  const running = [...tools].reverse().find(t => t.status === 'running')
  const target = runningNested || running

  let text: string
  if (target?.nested_agent) {
    const agent = target.nested_agent.split('/').pop() || target.nested_agent
    text = `${agent}: ${toolNarrative(target.name, target.arguments || {})}`
  } else if (target) {
    text = toolNarrative(target.name, target.arguments || {})
  } else {
    text = 'Thinking...'
  }

  return (
    <span className="text-xs text-muted-foreground italic ml-1">
      {text}
    </span>
  )
}

function ErrorBanner({ error, errorType }: { error: string; errorType?: string }) {
  const isAuth = errorType === 'auth'
  const isRateLimit = errorType === 'rate_limit'

  return (
    <div className={`mt-2 rounded-lg border px-3 py-2.5 text-sm ${
      isAuth
        ? 'border-red-400/50 bg-red-500/10 text-red-300'
        : isRateLimit
          ? 'border-yellow-400/50 bg-yellow-500/10 text-yellow-300'
          : 'border-red-400/50 bg-red-500/10 text-red-300'
    }`}>
      <div className="flex items-start gap-2">
        <svg className="w-4 h-4 mt-0.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
        </svg>
        <div>
          <p className="font-medium">{error}</p>
          {isAuth && (
            <p className="mt-1 text-xs opacity-80">
              Run <code className="px-1 py-0.5 rounded bg-black/20 font-mono">vega init</code> to configure your API key.
            </p>
          )}
          {isRateLimit && (
            <p className="mt-1 text-xs opacity-80">
              Wait a moment, then try your message again.
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

// --- Workspace file path detection & card ---
// Matches absolute paths under ~/.vega/workspace/ or relative paths that look like workspace files
const WORKSPACE_PATH_RE = /(?:\/[^\s"'<>|&;(){}\[\]\\]*\/\.vega\/workspace\/([^\s"'<>|&;(){}\[\]\\]+))/g

function fileExtIcon(name: string): string {
  const ext = name.includes('.') ? name.split('.').pop()?.toLowerCase() : ''
  switch (ext) {
    case 'html': case 'htm': return '\u{1F310}'
    case 'md': case 'markdown': return '\u{1F4DD}'
    case 'json': return '\u{1F4CB}'
    case 'png': case 'jpg': case 'jpeg': case 'gif': case 'svg': case 'webp': return '\u{1F5BC}\uFE0F'
    case 'pdf': return '\u{1F4C4}'
    case 'csv': return '\u{1F4CA}'
    case 'txt': return '\u{1F4C4}'
    default: return '\u{1F4CE}'
  }
}

function FileCard({ relPath, onClick }: { relPath: string; onClick: () => void }) {
  const name = relPath.split('/').pop() || relPath

  return (
    <button
      onClick={onClick}
      className="inline-flex items-center gap-2 my-1 px-3 py-2 rounded-lg border border-border bg-background/50 hover:border-indigo-500/50 hover:bg-accent/30 transition-all text-sm group"
    >
      <span className="text-lg">{fileExtIcon(name)}</span>
      <span className="font-medium text-foreground group-hover:text-indigo-400 transition-colors truncate max-w-xs">{name}</span>
      <svg className="w-3.5 h-3.5 text-muted-foreground group-hover:text-indigo-400 transition-colors flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25" />
      </svg>
    </button>
  )
}

/** Splits text content, replacing workspace paths with FileCard components */
function renderWithFileCards(text: string, onFileClick: (relPath: string) => void): ReactNode[] {
  const parts: ReactNode[] = []
  let lastIndex = 0
  let match: RegExpExecArray | null

  const re = new RegExp(WORKSPACE_PATH_RE.source, 'g')
  while ((match = re.exec(text)) !== null) {
    if (match.index > lastIndex) {
      parts.push(text.slice(lastIndex, match.index))
    }
    const relPath = match[1]
    parts.push(
      <FileCard key={match.index} relPath={relPath} onClick={() => onFileClick(relPath)} />
    )
    lastIndex = re.lastIndex
  }
  if (lastIndex < text.length) {
    parts.push(text.slice(lastIndex))
  }
  return parts
}

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

function ChatFilePreview({ file, onClose }: { file: FileContentResponse; onClose: () => void }) {
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

/** Recursively process React children, replacing workspace paths in text nodes */
function processChildren(children: ReactNode, onFileClick: (relPath: string) => void): ReactNode {
  if (typeof children === 'string') {
    if (WORKSPACE_PATH_RE.test(children)) {
      WORKSPACE_PATH_RE.lastIndex = 0
      return renderWithFileCards(children, onFileClick)
    }
    return children
  }
  if (Array.isArray(children)) {
    return children.map((child, i) => {
      if (typeof child === 'string') {
        if (WORKSPACE_PATH_RE.test(child)) {
          WORKSPACE_PATH_RE.lastIndex = 0
          return <span key={i}>{renderWithFileCards(child, onFileClick)}</span>
        }
        return child
      }
      return child
    })
  }
  return children
}

function UserAvatar() {
  return (
    <div className="w-7 h-7 rounded-full bg-muted flex items-center justify-center flex-shrink-0">
      <svg className="w-3.5 h-3.5 text-muted-foreground" fill="currentColor" viewBox="0 0 20 20">
        <path d="M10 10a4 4 0 100-8 4 4 0 000 8zm-7 8a7 7 0 1114 0H3z" />
      </svg>
    </div>
  )
}

function AgentAvatar({ name }: { name: string }) {
  return (
    <div className="w-7 h-7 rounded-full bg-primary/20 text-primary flex items-center justify-center flex-shrink-0 text-xs font-semibold">
      {name[0]?.toUpperCase()}
    </div>
  )
}


function AgentPicker({
  agents,
  activeAgent,
  onSelect,
}: {
  agents: AgentResponse[]
  activeAgent: string
  onSelect: (name: string) => void
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  if (agents.length === 0) return null

  return (
    <div ref={ref} className="relative flex-shrink-0">
      <button
        onClick={() => setOpen(v => !v)}
        title="Switch agent"
        className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg border border-border text-xs text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
      >
        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0z" />
        </svg>
        <span>Agents</span>
        <span className="bg-primary/20 text-primary rounded-full px-1.5 py-0.5 text-[10px] font-medium leading-none">
          {agents.length}
        </span>
      </button>

      {open && (
        <div className="absolute right-0 top-full mt-1.5 w-52 rounded-xl border border-border bg-card shadow-lg z-20 overflow-hidden">
          <div className="px-3 py-2 border-b border-border">
            <p className="text-xs text-muted-foreground font-medium">Your agents</p>
          </div>
          <div className="max-h-64 overflow-y-auto py-1">
            {agents.map(a => (
              <button
                key={a.name}
                onClick={() => { onSelect(a.name); setOpen(false) }}
                className={`flex items-center gap-2.5 w-full px-3 py-2 text-sm hover:bg-accent/50 transition-colors text-left ${
                  activeAgent === a.name ? 'bg-accent/30 text-foreground' : 'text-muted-foreground'
                }`}
              >
                <div className="w-6 h-6 rounded-full bg-primary/20 text-primary flex items-center justify-center flex-shrink-0 text-[10px] font-semibold">
                  {a.name[0]?.toUpperCase()}
                </div>
                <span className="truncate font-medium">{a.name}</span>
                {activeAgent === a.name && (
                  <svg className="w-3 h-3 ml-auto text-primary flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                    <path fillRule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clipRule="evenodd" />
                  </svg>
                )}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function MentionDropdown({
  agents,
  selectedIndex,
  onSelect,
  onHover,
}: {
  agents: string[]
  selectedIndex: number
  onSelect: (name: string) => void
  onHover: (index: number) => void
}) {
  const listRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const item = listRef.current?.children[selectedIndex] as HTMLElement | undefined
    item?.scrollIntoView({ block: 'nearest' })
  }, [selectedIndex])

  if (agents.length === 0) {
    return (
      <div className="px-3 py-2 text-xs text-muted-foreground">No matching agents</div>
    )
  }

  return (
    <div ref={listRef} className="max-h-48 overflow-y-auto py-1">
      {agents.map((name, i) => (
        <button
          key={name}
          onMouseDown={e => { e.preventDefault(); onSelect(name) }}
          onMouseEnter={() => onHover(i)}
          className={`flex items-center gap-2.5 w-full px-3 py-2 text-sm transition-colors text-left ${
            i === selectedIndex ? 'bg-accent/50 text-foreground' : 'text-muted-foreground hover:bg-accent/30'
          }`}
        >
          <div className="w-6 h-6 rounded-full bg-primary/20 text-primary flex items-center justify-center flex-shrink-0 text-[10px] font-semibold">
            {name[0]?.toUpperCase()}
          </div>
          <span className="truncate font-medium">{name}</span>
        </button>
      ))}
    </div>
  )
}

function TabBar({
  tabs,
  activeAgent,
  onSelect,
  onClose,
}: {
  tabs: string[]
  activeAgent: string
  onSelect: (name: string) => void
  onClose: (name: string) => void
}) {
  return (
    <div className="flex">
      {tabs.map(name => {
        const active = name === activeAgent
        const isHermes = name === HERMES
        const borderColor = active
          ? isHermes ? 'border-primary' : 'border-emerald-500'
          : 'border-transparent'
        return (
          <button
            key={name}
            onClick={() => onSelect(name)}
            className={`group flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors flex-shrink-0 ${borderColor} ${
              active
                ? 'bg-background text-foreground'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent/30'
            }`}
          >
            <div className={`w-5 h-5 rounded-full flex items-center justify-center flex-shrink-0 text-[10px] font-semibold ${
              isHermes ? 'bg-primary/20 text-primary' : 'bg-emerald-500/20 text-emerald-400'
            }`}>
              {name[0]?.toUpperCase()}
            </div>
            <span className="truncate max-w-[8rem]">{name}</span>
            {!isHermes && (
              <span
                onMouseDown={e => { e.preventDefault(); e.stopPropagation(); onClose(name) }}
                className={`ml-0.5 p-0.5 rounded hover:bg-accent transition-colors ${
                  active ? 'text-muted-foreground hover:text-foreground' : 'opacity-0 group-hover:opacity-100 text-muted-foreground hover:text-foreground'
                }`}
              >
                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}

function VegaStar() {
  return (
    <pre className="text-xs leading-snug font-mono select-none inline-block text-left" aria-hidden="true">
      <span className="text-blue-300">{'        ·   '}</span><span className="text-cyan-400">✦</span><span className="text-blue-300">{'   ·'}</span>{'\n'}
      <span className="text-indigo-400">{'         \\  '}</span><span className="text-cyan-400">│</span><span className="text-indigo-400">{'  /'}</span>{'\n'}
      <span className="text-indigo-400">{'          \\ '}</span><span className="text-cyan-400">│</span><span className="text-indigo-400">{' /'}</span>{'\n'}
      <span className="text-blue-300">{'  · '}</span><span className="text-rose-400">{'✦ ─────'}</span><span className="text-amber-300">{' ★ '}</span><span className="text-purple-400">{'───── ✦'}</span><span className="text-blue-300">{' ·'}</span>{'\n'}
      <span className="text-orange-400">{'          / '}</span><span className="text-emerald-400">│</span><span className="text-orange-400">{' \\'}</span>{'\n'}
      <span className="text-orange-400">{'         /  '}</span><span className="text-emerald-400">│</span><span className="text-orange-400">{'  \\'}</span>{'\n'}
      <span className="text-blue-300">{'        ·   '}</span><span className="text-emerald-400">✦</span><span className="text-blue-300">{'   ·'}</span>
    </pre>
  )
}

export function Chat() {
  const { agent: agentParam } = useParams<{ agent?: string }>()
  const navigate = useNavigate()
  const { events } = useSSE()

  // Which agent the chat is currently directed to
  const [activeAgent, setActiveAgent] = useState(agentParam || HERMES)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const [showScrollBtn, setShowScrollBtn] = useState(false)
  const [loaded, setLoaded] = useState(false)
  // Set when Hermes hands off — shows a "connected via Hermes" notice in the new chat
  const [handoffFrom, setHandoffFrom] = useState<string | null>(null)
  // Specialist agents (excludes hermes + mother)
  const [specialists, setSpecialists] = useState<AgentResponse[]>([])

  // File preview state
  const [previewFile, setPreviewFile] = useState<FileContentResponse | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [copied, setCopied] = useState(false)

  // Tab bar state — persisted to sessionStorage so tabs survive page navigation
  const [openTabs, setOpenTabs] = useState<string[]>(() => {
    try {
      const stored = sessionStorage.getItem('vega-chat-tabs')
      if (stored) {
        const parsed = JSON.parse(stored) as string[]
        if (Array.isArray(parsed) && parsed.length > 0) {
          return parsed.includes(HERMES) ? parsed : [HERMES, ...parsed]
        }
      }
    } catch { /* ignore */ }
    return [HERMES]
  })

  useEffect(() => {
    sessionStorage.setItem('vega-chat-tabs', JSON.stringify(openTabs))
  }, [openTabs])

  const ensureTab = useCallback((name: string) => {
    setOpenTabs(prev => prev.includes(name) ? prev : [...prev, name])
  }, [])

  const closeTab = useCallback((name: string) => {
    if (name === HERMES) return
    setOpenTabs(prev => {
      const idx = prev.indexOf(name)
      if (idx < 0) return prev
      const next = prev.filter(t => t !== name)
      if (next.length === 0) return [HERMES]
      // If closing the active tab, switch to adjacent or fallback to Hermes
      if (name === activeAgent) {
        const newActive = next[Math.min(idx, next.length - 1)] || HERMES
        // Defer the agent switch to avoid state conflicts
        setTimeout(() => switchToAgent(newActive), 0)
      }
      return next
    })
  }, [activeAgent]) // eslint-disable-line react-hooks/exhaustive-deps

  // @-mention autocomplete state
  const [mentionOpen, setMentionOpen] = useState(false)
  const [mentionQuery, setMentionQuery] = useState('')
  const [mentionIndex, setMentionIndex] = useState(0)
  const [mentionStartPos, setMentionStartPos] = useState(0)
  const mentionRef = useRef<HTMLDivElement>(null)

  const openFilePreview = useCallback(async (relPath: string) => {
    setPreviewLoading(true)
    try {
      const file = await api.getFileContent(relPath)
      setPreviewFile(file)
    } catch {
      // best-effort
    } finally {
      setPreviewLoading(false)
    }
  }, [])

  const bottomRef = useRef<HTMLDivElement>(null)
  const messagesRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  // Track the event index at stream start so we only look at events fired during this stream
  const streamStartEventCount = useRef(0)

  // Fetch specialist agents (all agents except meta-agents)
  const fetchAgents = useCallback(() => {
    api.getAgents()
      .then(list => {
        setSpecialists((list ?? []).filter(a => !META_AGENTS.has(a.name)))
      })
      .catch(() => {})
  }, [])

  useEffect(() => { fetchAgents() }, [fetchAgents])

  // Refresh agent list when Mother creates or deletes an agent
  useEffect(() => {
    const last = events[events.length - 1]
    if (!last) return
    if (last.type === 'agent.created' || last.type === 'agent.deleted') {
      fetchAgents()
    }
  }, [events, fetchAgents])

  // Auto-add a tab when activeAgent changes (covers switchToAgent, handoff, URL nav, initial load)
  useEffect(() => { ensureTab(activeAgent) }, [activeAgent, ensureTab])

  // Prune tabs for deleted agents (except hermes/mother which aren't in specialists).
  // Skip when specialists is empty (fetch hasn't returned yet). Always keep activeAgent.
  useEffect(() => {
    if (specialists.length === 0) return
    const specialistNames = new Set(specialists.map(a => a.name))
    setOpenTabs(prev => prev.filter(t => META_AGENTS.has(t) || specialistNames.has(t) || t === activeAgent))
  }, [specialists, activeAgent])

  // Track reconnection abort controller so we can cancel on unmount/agent switch
  const reconnectAbortRef = useRef<AbortController | null>(null)

  // Load history for the active agent whenever it changes, then check for
  // an active stream and reconnect to it automatically.
  useEffect(() => {
    // Cancel any previous reconnection attempt
    if (reconnectAbortRef.current) {
      reconnectAbortRef.current.abort()
      reconnectAbortRef.current = null
    }

    setLoaded(false)
    setMessages([])
    setSending(false)

    api.chatHistory(activeAgent)
      .then(history => {
        if (history?.length) {
          setMessages(history.map(m => ({
            role: m.role as 'user' | 'assistant',
            content: m.content,
          })))
        }
      })
      .catch(() => {})
      .finally(() => {
        setLoaded(true)

        // After history loads, check if agent has an active stream to reconnect to
        api.chatStatus(activeAgent)
          .then(status => {
            if (!status?.streaming) return

            // Add a placeholder assistant message and reconnect to the stream
            setMessages(prev => [...prev, { role: 'assistant', content: '', toolCalls: [], streaming: true }])
            setSending(true)

            const abort = new AbortController()
            reconnectAbortRef.current = abort

            let finalContent = ''
            const wrappedHandler = (event: import('../lib/types').ChatEvent) => {
              if (event.type === 'text_delta') finalContent += event.delta || ''
              handleEvent(event)
            }

            api.chatStreamReconnect(activeAgent, wrappedHandler, abort.signal)
              .then(() => checkForHandoff(finalContent))
              .catch(() => {})
              .finally(() => {
                setSending(false)
                reconnectAbortRef.current = null
              })
          })
          .catch(() => {})
      })
  }, [activeAgent]) // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-resize textarea
  const resizeTextarea = useCallback(() => {
    const ta = textareaRef.current
    if (!ta) return
    ta.style.height = 'auto'
    ta.style.height = Math.min(ta.scrollHeight, 6 * 24) + 'px'
  }, [])

  useEffect(() => { resizeTextarea() }, [input, resizeTextarea])

  // Smart auto-scroll
  const isNearBottom = useCallback(() => {
    const el = messagesRef.current
    if (!el) return true
    return el.scrollHeight - el.scrollTop - el.clientHeight < 150
  }, [])

  useEffect(() => {
    if (isNearBottom()) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [messages, isNearBottom])

  useEffect(() => {
    const el = messagesRef.current
    if (!el) return
    const onScroll = () => {
      setShowScrollBtn(el.scrollHeight - el.scrollTop - el.clientHeight > 200)
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [activeAgent])

  // Click outside to close mention dropdown
  useEffect(() => {
    if (!mentionOpen) return
    const handler = (e: MouseEvent) => {
      if (mentionRef.current && !mentionRef.current.contains(e.target as Node)) {
        setMentionOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [mentionOpen])

  const handleEvent = useCallback((event: ChatEvent) => {
    setMessages(prev => {
      const msgs = [...prev]
      const last = msgs[msgs.length - 1]
      if (!last || last.role !== 'assistant') return msgs

      const updated = { ...last, toolCalls: [...(last.toolCalls || [])] }

      switch (event.type) {
        case 'text_delta':
          updated.content += event.delta || ''
          break
        case 'tool_start':
          // Ensure a newline break before tool calls so post-tool text
          // doesn't run into pre-tool text.
          if (updated.content && !updated.content.endsWith('\n')) {
            updated.content += '\n'
          }
          updated.toolCalls!.push({
            id: event.tool_call_id!,
            name: event.tool_name!,
            arguments: (event.arguments || {}) as Record<string, unknown>,
            status: 'running',
            collapsed: true,
            nested_agent: event.nested_agent,
          })
          break
        case 'tool_end': {
          const tc = updated.toolCalls!.find(t => t.id === event.tool_call_id)
          if (tc) {
            tc.result = event.result
            tc.duration_ms = event.duration_ms
            tc.status = event.error ? 'error' : 'completed'
          }
          break
        }
        case 'error': {
          const errMsg = event.error || 'An unexpected error occurred'
          const errType = classifyErrorType(errMsg)
          updated.error = errMsg
          updated.errorType = errType
          updated.streaming = false
          break
        }
        case 'done':
          updated.streaming = false
          if (event.metrics) updated.metrics = event.metrics
          break
      }

      msgs[msgs.length - 1] = updated
      return msgs
    })
  }, [])

  // After a stream ends, check if Hermes emitted a handoff line and auto-switch
  const checkForHandoff = useCallback((finalContent: string) => {
    if (activeAgent !== HERMES) return
    const match = finalContent.match(HANDOFF_RE)
    if (match) {
      const target = match[1].trim()
      setHandoffFrom(HERMES)
      setActiveAgent(target)
    }
  }, [activeAgent])

  const send = async (text?: string) => {
    const msg = (text ?? input).trim()
    if (!msg || sending) return
    setInput('')
    setMessages(prev => [...prev, { role: 'user', content: msg }])
    setMessages(prev => [...prev, { role: 'assistant', content: '', toolCalls: [], streaming: true }])
    setSending(true)

    // Remember SSE event count so we can detect new agent.created events during this stream
    streamStartEventCount.current = events.length

    const abort = new AbortController()
    abortRef.current = abort

    let finalContent = ''
    const wrappedHandler = (event: ChatEvent) => {
      if (event.type === 'text_delta') finalContent += event.delta || ''
      handleEvent(event)
    }

    try {
      await api.chatStream(activeAgent, msg, wrappedHandler, abort.signal)
      checkForHandoff(finalContent)
    } catch (err) {
      setMessages(prev => {
        const msgs = [...prev]
        const last = msgs[msgs.length - 1]
        if (last?.role === 'assistant') {
          const errMsg = err instanceof APIError ? err.message : String(err)
          const errType = err instanceof APIError ? classifyErrorTypeFromStatus(err.status) : classifyErrorType(errMsg)
          msgs[msgs.length - 1] = { ...last, error: errMsg, errorType: errType, streaming: false }
        }
        return msgs
      })
    } finally {
      setSending(false)
      abortRef.current = null
    }
  }

  // Sync activeAgent when navigating to /chat/:agent from another page
  useEffect(() => {
    if (agentParam && agentParam !== activeAgent) {
      setActiveAgent(agentParam)
    }
  }, [agentParam]) // eslint-disable-line react-hooks/exhaustive-deps

  // Keep URL in sync with activeAgent
  useEffect(() => {
    const target = activeAgent === HERMES ? '/chat' : `/chat/${activeAgent}`
    navigate(target, { replace: true })
  }, [activeAgent, navigate])

  const switchToAgent = (name: string) => {
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
      setSending(false)
    }
    if (reconnectAbortRef.current) {
      reconnectAbortRef.current.abort()
      reconnectAbortRef.current = null
    }
    setHandoffFrom(null)
    setActiveAgent(name)
    // History load handled by the activeAgent useEffect
  }

  const clearChat = async () => {
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
      setSending(false)
    }
    if (reconnectAbortRef.current) {
      reconnectAbortRef.current.abort()
      reconnectAbortRef.current = null
    }
    setMessages([])
    try { await api.resetChat(activeAgent) } catch { /* best-effort */ }
  }

  const copyTranscript = useCallback(() => {
    const lines = messages.map(msg => {
      const role = msg.role === 'user' ? 'User' : activeAgent
      let text = `[${role}]\n${msg.content}`
      if (msg.toolCalls?.length) {
        for (const tc of msg.toolCalls) {
          text += `\n  [tool: ${tc.name}] status=${tc.status}`
          if (tc.arguments && Object.keys(tc.arguments).length > 0) {
            text += `\n    args: ${JSON.stringify(tc.arguments)}`
          }
          if (tc.result != null) {
            text += `\n    result: ${tc.result}`
          }
        }
      }
      if (msg.error) {
        text += `\n  [error] ${msg.error}`
      }
      return text
    })
    const transcript = `--- Chat Transcript (${activeAgent}) ---\n${new Date().toISOString()}\n\n${lines.join('\n\n')}`
    navigator.clipboard.writeText(transcript).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }, [messages, activeAgent])

  const toggleToolCall = (msgIdx: number, tcIdx: number) => {
    setMessages(prev => prev.map((msg, i) => {
      if (i !== msgIdx || !msg.toolCalls) return msg
      const wasCollapsed = msg.toolCalls[tcIdx].collapsed
      return {
        ...msg,
        toolCalls: msg.toolCalls.map((tc, j) => ({
          ...tc,
          collapsed: j === tcIdx ? !wasCollapsed : true,
        })),
      }
    }))
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value
    setInput(val)

    const cursor = e.target.selectionStart ?? val.length
    // Walk backwards from cursor to find an unescaped '@'
    let atPos = -1
    for (let i = cursor - 1; i >= 0; i--) {
      if (val[i] === ' ' || val[i] === '\n') break
      if (val[i] === '@') {
        // '@' must be at start or preceded by whitespace
        if (i === 0 || val[i - 1] === ' ' || val[i - 1] === '\n') {
          atPos = i
        }
        break
      }
    }

    if (atPos >= 0) {
      const query = val.slice(atPos + 1, cursor)
      if (!query.includes(' ')) {
        setMentionOpen(true)
        setMentionQuery(query)
        setMentionStartPos(atPos)
        setMentionIndex(0)
        return
      }
    }
    setMentionOpen(false)
  }

  const selectMention = useCallback((name: string) => {
    const before = input.slice(0, mentionStartPos)
    const after = input.slice(mentionStartPos + 1 + mentionQuery.length)
    const newVal = before + '@' + name + ' ' + after
    setInput(newVal)
    setMentionOpen(false)

    const cursorPos = mentionStartPos + 1 + name.length + 1
    requestAnimationFrame(() => {
      const ta = textareaRef.current
      if (ta) {
        ta.focus()
        ta.setSelectionRange(cursorPos, cursorPos)
      }
    })
  }, [input, mentionStartPos, mentionQuery])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionOpen && mentionAgents.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setMentionIndex(i => (i + 1) % mentionAgents.length)
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setMentionIndex(i => (i - 1 + mentionAgents.length) % mentionAgents.length)
        return
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault()
        selectMention(mentionAgents[mentionIndex])
        return
      }
    }
    if (mentionOpen && e.key === 'Escape') {
      e.preventDefault()
      setMentionOpen(false)
      return
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  const isHermes = activeAgent === HERMES
  const agentNames = new Set([HERMES, 'mother', ...specialists.map(a => a.name)])

  const mentionAgents = mentionOpen
    ? [...agentNames]
        .filter(n => n.toLowerCase().includes(mentionQuery.toLowerCase()))
        .sort((a, b) => a.localeCompare(b))
    : []

  // Clamp mention index when filtered results shrink
  useEffect(() => {
    if (mentionIndex >= mentionAgents.length) {
      setMentionIndex(Math.max(0, mentionAgents.length - 1))
    }
  }, [mentionAgents.length, mentionIndex])

  return (
    <div className="flex flex-col h-[calc(100vh-3rem)]">
      {/* Tab bar + actions */}
      <div className="flex items-end border-b border-border">
        <div className="flex-1 min-w-0 overflow-x-auto scrollbar-none">
          <TabBar
            tabs={openTabs}
            activeAgent={activeAgent}
            onSelect={switchToAgent}
            onClose={closeTab}
          />
        </div>
        <div className="flex items-center gap-1 pl-2 pb-1.5 flex-shrink-0">
          <AgentPicker
            agents={specialists}
            activeAgent={activeAgent}
            onSelect={switchToAgent}
          />
          <button
            onClick={copyTranscript}
            title="Copy transcript"
            className="p-2 rounded-lg text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
          >
            {copied ? (
              <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
              </svg>
            ) : (
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9.75a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
              </svg>
            )}
          </button>
          <button
            onClick={clearChat}
            title="Clear chat"
            className="p-2 rounded-lg text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0" />
            </svg>
          </button>
        </div>
      </div>

      {/* Messages */}
      <div ref={messagesRef} className="flex-1 overflow-auto space-y-5 pb-4 relative">
        {loaded && messages.length === 0 && isHermes && (
          <div className="flex items-center justify-center h-full">
            <div className="text-center space-y-4 max-w-md">
              <VegaStar />
              <div>
                <h3 className="text-lg font-semibold text-foreground">What do you need?</h3>
                <p className="text-sm text-muted-foreground mt-1.5 leading-relaxed">
                  Hermes routes your goals across all agents — or calls on Mother to build new ones.
                </p>
              </div>
              <div className="flex flex-wrap gap-2 justify-center pt-2">
                {starterPrompts.map((prompt) => (
                  <button
                    key={prompt}
                    onClick={() => send(prompt)}
                    className="text-sm px-3 py-1.5 rounded-full border border-border text-muted-foreground hover:text-foreground hover:border-primary/50 hover:bg-accent/30 transition-colors"
                  >
                    {prompt}
                  </button>
                ))}
              </div>
            </div>
          </div>
        )}

        {messages.map((msg, i) => {
          // Build a map of file basenames → relative workspace paths from tool calls
          const fileLinks = new Map<string, string>()
          for (const tc of msg.toolCalls || []) {
            if ((tc.name === 'write_file' || tc.name === 'read_file') && tc.arguments?.path) {
              const p = String(tc.arguments.path)
              const wsIdx = p.indexOf('.vega/workspace/')
              if (wsIdx >= 0) {
                const relPath = p.slice(wsIdx + '.vega/workspace/'.length)
                const basename = relPath.split('/').pop() || relPath
                fileLinks.set(basename, relPath)
              } else {
                // Relative path or bare filename — use as-is for the API
                const basename = p.split('/').pop() || p
                fileLinks.set(basename, basename)
              }
            }
          }

          return (
          <div key={i} className={`flex gap-2.5 ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            {msg.role === 'assistant' && <AgentAvatar name={activeAgent} />}
            {msg.role === 'user' ? (
              <div className="max-w-[75%] rounded-2xl shadow-sm px-4 py-2.5 text-sm whitespace-pre-wrap bg-primary text-primary-foreground">
                {msg.content}
              </div>
            ) : (
              <div className="max-w-[75%] rounded-2xl shadow-sm px-4 py-2.5 text-sm bg-card border border-border prose prose-invert prose-sm prose-p:my-2 prose-headings:my-3 prose-ul:my-2 prose-ol:my-2 prose-li:my-0.5 prose-pre:bg-background prose-pre:border prose-pre:border-border prose-code:text-purple-400 prose-code:before:content-none prose-code:after:content-none max-w-none">
                {msg.streaming && !msg.content && !(msg.toolCalls?.length) && (
                  <p className="text-xs text-muted-foreground italic py-1">Thinking...</p>
                )}
                {msg.content && (
                  <Markdown components={{
                    p({ children }) {
                      return <p>{processChildren(children, openFilePreview)}</p>
                    },
                    li({ children }) {
                      return <li>{processChildren(children, openFilePreview)}</li>
                    },
                    strong({ children }) {
                      const text = typeof children === 'string' ? children
                        : Array.isArray(children) ? children.map(c => typeof c === 'string' ? c : '').join('')
                        : ''
                      if (text && agentNames.has(text)) {
                        return (
                          <strong
                            className="cursor-pointer text-primary hover:underline decoration-primary/50"
                            onClick={(e) => { e.stopPropagation(); switchToAgent(text) }}
                            title={`Switch to ${text}`}
                            role="button"
                          >
                            {children}
                          </strong>
                        )
                      }
                      return <strong>{children}</strong>
                    },
                    code({ children, className }) {
                      // Fenced code blocks (with language class) render normally
                      if (className) return <code className={className}>{children}</code>
                      const text = typeof children === 'string' ? children : ''
                      if (text && fileLinks.has(text)) {
                        const relPath = fileLinks.get(text)!
                        return (
                          <code
                            className="cursor-pointer !text-indigo-400 hover:underline decoration-indigo-400/50"
                            onClick={(e) => { e.stopPropagation(); openFilePreview(relPath) }}
                            title="Click to preview file"
                            role="button"
                          >
                            {fileExtIcon(text)} {children}
                          </code>
                        )
                      }
                      return <code>{children}</code>
                    },
                  }}>{msg.content}</Markdown>
                )}
                {msg.streaming && msg.content && !(msg.toolCalls?.length) && (
                  <span className="inline-block w-1.5 h-4 bg-primary animate-pulse ml-0.5 align-text-bottom rounded-sm" />
                )}
                {msg.toolCalls && msg.toolCalls.length > 0 && (
                  <div className="my-1.5">
                    {/* Tool badges */}
                    <div className="flex flex-row flex-wrap gap-1.5">
                      {msg.toolCalls.map((tc, j) => (
                        <button key={tc.id} onClick={() => toggleToolCall(i, j)}
                          className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-mono
                            border transition-colors ${!tc.collapsed
                              ? 'border-indigo-500/50 bg-indigo-500/10 text-foreground'
                              : 'border-border bg-background/50 text-muted-foreground hover:text-foreground hover:border-muted-foreground/30'
                            }`}>
                          <span className={`w-1.5 h-1.5 rounded-full ${statusDotClass(tc)}`} />
                          <span>{shortToolName(tc.name)}</span>
                          {tc.duration_ms != null && <span className="text-muted-foreground">{tc.duration_ms}ms</span>}
                        </button>
                      ))}
                    </div>
                    {/* Constellation + narrative — visible while stream is active */}
                    {msg.streaming && msg.toolCalls.length > 0 && (
                      <div className="flex items-center gap-1 pt-1.5 constellation-activity">
                        <ActivityConstellation tools={msg.toolCalls} />
                        <ActivityNarrative tools={msg.toolCalls} />
                      </div>
                    )}
                    {/* Expanded detail panel for the selected tab */}
                    {msg.toolCalls.map((tc, j) => !tc.collapsed && (
                      <div key={tc.id} className="mt-2 rounded-lg border border-border bg-background/50 px-3 py-2 space-y-1.5 text-sm">
                        {tc.arguments && Object.keys(tc.arguments).length > 0 && (
                          <div>
                            <span className="text-xs text-muted-foreground">args</span>
                            <pre className="mt-0.5 text-xs font-mono bg-background rounded p-2 overflow-x-auto border border-border whitespace-pre-wrap">
                              {JSON.stringify(tc.arguments, null, 2)}
                            </pre>
                          </div>
                        )}
                        {tc.result != null && (
                          <div>
                            <span className="text-xs text-muted-foreground">result</span>
                            <pre className="mt-0.5 text-xs font-mono bg-background rounded p-2 overflow-x-auto border border-border whitespace-pre-wrap max-h-60">
                              {tc.result}
                            </pre>
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}
                {msg.error && <ErrorBanner error={msg.error} errorType={msg.errorType} />}
                {msg.metrics && !msg.streaming && (
                  <div className="mt-1.5 text-[11px] text-muted-foreground/60 text-right font-mono">
                    {msg.metrics.cost_usd >= 0.01
                      ? `$${msg.metrics.cost_usd.toFixed(2)}`
                      : `$${msg.metrics.cost_usd.toFixed(4)}`}
                    {' · '}
                    {(msg.metrics.input_tokens + msg.metrics.output_tokens).toLocaleString()} tokens
                    {' · '}
                    {msg.metrics.duration_ms >= 1000
                      ? `${(msg.metrics.duration_ms / 1000).toFixed(1)}s`
                      : `${msg.metrics.duration_ms}ms`}
                  </div>
                )}
              </div>
            )}
            {msg.role === 'user' && <UserAvatar />}
          </div>
          )
        })}

        {/* Handoff notice — shown at top of specialist conversation */}
        {!isHermes && handoffFrom && messages.length === 0 && loaded && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground px-1 py-2">
            <span className="text-emerald-400">✦</span>
            <span>Hermes connected you. Your messages go directly to <span className="font-medium text-foreground">{activeAgent}</span>.</span>
          </div>
        )}

        <div ref={bottomRef} />

        {showScrollBtn && (
          <button
            onClick={() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' })}
            className="sticky bottom-3 left-1/2 -translate-x-1/2 w-8 h-8 rounded-full bg-accent border border-border shadow-md flex items-center justify-center text-muted-foreground hover:text-foreground transition-colors z-10"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
            </svg>
          </button>
        )}
      </div>

      {/* Input */}
      <div className="pt-3 border-t border-border space-y-1.5">
        <div className="flex gap-2 items-end">
          <div className="relative flex-1" ref={mentionRef}>
            {mentionOpen && (
              <div className="absolute bottom-full mb-1.5 left-0 w-64 rounded-xl border border-border bg-card shadow-lg z-20 overflow-hidden">
                <div className="px-3 py-2 border-b border-border">
                  <p className="text-xs text-muted-foreground font-medium">Mention an agent</p>
                </div>
                <MentionDropdown
                  agents={mentionAgents}
                  selectedIndex={mentionIndex}
                  onSelect={selectMention}
                  onHover={setMentionIndex}
                />
              </div>
            )}
            <textarea
              ref={textareaRef}
              rows={1}
              value={input}
              onChange={handleInputChange}
              onKeyDown={handleKeyDown}
              placeholder={isHermes ? 'Tell Hermes what you need…' : `Message ${activeAgent}…`}
              disabled={sending}
              className={`w-full px-4 py-2.5 rounded-xl bg-background border text-sm focus:outline-none disabled:opacity-50 resize-none overflow-y-auto transition-colors ${isHermes ? 'border-border focus:border-primary' : 'border-emerald-500/30 focus:border-emerald-500/60'}`}
              style={{ maxHeight: '144px' }}
            />
          </div>
          <button
            onClick={() => send()}
            disabled={sending || !input.trim()}
            className="p-2.5 rounded-xl bg-primary text-primary-foreground disabled:opacity-50 flex-shrink-0"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 10.5L12 3m0 0l7.5 7.5M12 3v18" />
            </svg>
          </button>
        </div>
        <p className="text-xs text-muted-foreground px-1">Enter to send · Shift+Enter for new line · @ to mention</p>
      </div>

      {/* File preview modal */}
      {previewLoading && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center">
          <div className="animate-pulse text-white">Loading preview...</div>
        </div>
      )}
      {previewFile && (
        <ChatFilePreview file={previewFile} onClose={() => setPreviewFile(null)} />
      )}
    </div>
  )
}
