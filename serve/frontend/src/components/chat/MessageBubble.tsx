import type { ReactNode } from 'react'
import Markdown from 'react-markdown'
import type { ChatEventMetrics, ToolCallState } from '../../lib/types'
import { AgentAvatar, UserAvatar } from './AgentAvatar'
import { ToolCallBadges } from './ToolCallDisplay'

// Matches absolute paths under ~/.vega/workspace/
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

export { fileExtIcon }

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

export function ErrorBanner({ error, errorType }: { error: string; errorType?: string }) {
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

export interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  agent?: string
  toolCalls?: ToolCallState[]
  streaming?: boolean
  error?: string
  errorType?: 'auth' | 'rate_limit' | 'generic'
  metrics?: ChatEventMetrics
  replyCount?: number
  id?: number
}

interface MessageBubbleProps {
  msg: ChatMessage
  msgIdx: number
  agentName: string
  agentDisplayName?: string
  agentAvatar?: string
  agentNames?: Set<string>
  onToggleToolCall: (msgIdx: number, tcIdx: number) => void
  onFileClick: (relPath: string) => void
  onSwitchAgent?: (name: string) => void
  onThreadClick?: (msgId: number) => void
  showAgentLabel?: boolean
}

export function MessageBubble({
  msg,
  msgIdx,
  agentName,
  agentDisplayName,
  agentAvatar,
  agentNames,
  onToggleToolCall,
  onFileClick,
  onSwitchAgent,
  onThreadClick,
  showAgentLabel,
}: MessageBubbleProps) {
  // Build a map of file basenames -> relative workspace paths from tool calls
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
        const basename = p.split('/').pop() || p
        fileLinks.set(basename, basename)
      }
    }
  }

  return (
    <div className={`flex gap-2.5 ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
      {msg.role === 'assistant' && <AgentAvatar name={agentName} displayName={agentDisplayName} avatar={agentAvatar} />}
      {msg.role === 'user' ? (
        <div className="max-w-[85%] md:max-w-[75%] rounded-2xl shadow-sm px-3 py-2 md:px-4 md:py-2.5 text-sm whitespace-pre-wrap bg-primary text-primary-foreground">
          {msg.content}
        </div>
      ) : (
        <div className="max-w-[85%] md:max-w-[75%] rounded-2xl shadow-sm px-3 py-2 md:px-4 md:py-2.5 text-sm bg-card border border-border prose prose-invert prose-sm prose-p:my-2 prose-headings:my-3 prose-ul:my-2 prose-ol:my-2 prose-li:my-0.5 prose-pre:bg-background prose-pre:border prose-pre:border-border prose-code:text-purple-400 prose-code:before:content-none prose-code:after:content-none max-w-none">
          {showAgentLabel && msg.agent && (
            <p className="text-xs font-semibold text-primary mb-1">{agentDisplayName || agentName}</p>
          )}
          {msg.streaming && !msg.content && !(msg.toolCalls?.length) && (
            <p className="text-xs text-muted-foreground italic py-1">Thinking...</p>
          )}
          {msg.content && (
            <Markdown components={{
              p({ children }) {
                return <p>{processChildren(children, onFileClick)}</p>
              },
              li({ children }) {
                return <li>{processChildren(children, onFileClick)}</li>
              },
              strong({ children }) {
                const text = typeof children === 'string' ? children
                  : Array.isArray(children) ? children.map(c => typeof c === 'string' ? c : '').join('')
                  : ''
                if (text && agentNames?.has(text) && onSwitchAgent) {
                  return (
                    <strong
                      className="cursor-pointer text-primary hover:underline decoration-primary/50"
                      onClick={(e) => { e.stopPropagation(); onSwitchAgent(text) }}
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
                if (className) return <code className={className}>{children}</code>
                const text = typeof children === 'string' ? children : ''
                if (text && fileLinks.has(text)) {
                  const relPath = fileLinks.get(text)!
                  return (
                    <code
                      className="cursor-pointer !text-indigo-400 hover:underline decoration-indigo-400/50"
                      onClick={(e) => { e.stopPropagation(); onFileClick(relPath) }}
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
            <ToolCallBadges
              toolCalls={msg.toolCalls}
              streaming={msg.streaming}
              onToggle={(tcIdx) => onToggleToolCall(msgIdx, tcIdx)}
            />
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
          {onThreadClick && msg.id != null && msg.replyCount != null && msg.replyCount > 0 && (
            <button
              onClick={() => onThreadClick(msg.id!)}
              className="mt-1.5 text-xs text-primary hover:underline"
            >
              {msg.replyCount} {msg.replyCount === 1 ? 'reply' : 'replies'}
            </button>
          )}
        </div>
      )}
      {msg.role === 'user' && <UserAvatar />}
    </div>
  )
}
