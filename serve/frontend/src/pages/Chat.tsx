import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useSSE } from '../hooks/useSSE'
import { api, APIError } from '../lib/api'
import type { AgentResponse, ChatEvent, FileContentResponse } from '../lib/types'
import { AgentAvatar } from '../components/chat/AgentAvatar'
import { MessageBubble, type ChatMessage } from '../components/chat/MessageBubble'
import { ChatInput } from '../components/chat/ChatInput'
import { FilePreview } from '../components/chat/FilePreview'
import { ScrollToBottom } from '../components/chat/ScrollToBottom'

const HERMES = 'hermes'
const META_AGENTS = new Set(['hermes', 'mother'])

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
            {agents.map(a => {
              const label = a.display_name || a.name
              return (
                <button
                  key={a.name}
                  onClick={() => { onSelect(a.name); setOpen(false) }}
                  className={`flex items-center gap-2.5 w-full px-3 py-2 text-sm hover:bg-accent/50 transition-colors text-left ${
                    activeAgent === a.name ? 'bg-accent/30 text-foreground' : 'text-muted-foreground'
                  }`}
                >
                  <AgentAvatar name={a.name} displayName={label} avatar={a.avatar} size={6} />
                  <div className="flex flex-col min-w-0">
                    <span className="truncate font-medium">{label}</span>
                    {a.title && <span className="truncate text-xs text-muted-foreground/70">{a.title}</span>}
                  </div>
                  {activeAgent === a.name && (
                    <svg className="w-3 h-3 ml-auto text-primary flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
                      <path fillRule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clipRule="evenodd" />
                    </svg>
                  )}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

function TabBar({
  tabs,
  activeAgent,
  onSelect,
  onClose,
  displayInfo,
}: {
  tabs: string[]
  activeAgent: string
  onSelect: (name: string) => void
  onClose: (name: string) => void
  displayInfo: Map<string, { displayName: string; title: string; avatar: string }>
}) {
  return (
    <div className="flex">
      {tabs.map((name, idx) => {
        const active = name === activeAgent
        const isHermes = name === HERMES
        const info = displayInfo.get(name)
        const label = info?.displayName || name
        const borderColor = active
          ? isHermes ? 'border-primary' : 'border-emerald-500'
          : 'border-transparent'
        const showDivider = isHermes && tabs.length > 1
        return (
          <div key={name} className="flex items-stretch">
            <button
              onClick={() => onSelect(name)}
              className={`group flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors flex-shrink-0 ${borderColor} ${
                active
                  ? 'bg-background text-foreground'
                  : 'text-muted-foreground hover:text-foreground hover:bg-accent/30'
              }`}
              title={info?.title || undefined}
            >
              <AgentAvatar name={name} displayName={label} avatar={info?.avatar} size={5} />
              <div className="flex flex-col items-start min-w-0">
                <span className="truncate max-w-[8rem]">{label}</span>
                {info?.title && (
                  <span className="truncate max-w-[8rem] text-[10px] font-normal text-muted-foreground leading-tight">{info.title}</span>
                )}
              </div>
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
            {showDivider && (
              <div className="flex items-center px-1">
                <div className="w-px h-4 bg-border" />
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function VegaStar() {
  return (
    <pre className="text-xs leading-snug font-mono select-none inline-block text-left" aria-hidden="true">
      <span className="text-blue-300">{'        ·   '}</span><span className="text-cyan-400">{'✦'}</span><span className="text-blue-300">{'   ·'}</span>{'\n'}
      <span className="text-indigo-400">{'         \\  '}</span><span className="text-cyan-400">{'│'}</span><span className="text-indigo-400">{'  /'}</span>{'\n'}
      <span className="text-indigo-400">{'          \\ '}</span><span className="text-cyan-400">{'│'}</span><span className="text-indigo-400">{' /'}</span>{'\n'}
      <span className="text-blue-300">{'  · '}</span><span className="text-rose-400">{'✦ ─────'}</span><span className="text-amber-300">{' ★ '}</span><span className="text-purple-400">{'───── ✦'}</span><span className="text-blue-300">{' ·'}</span>{'\n'}
      <span className="text-orange-400">{'          / '}</span><span className="text-emerald-400">{'│'}</span><span className="text-orange-400">{' \\'}</span>{'\n'}
      <span className="text-orange-400">{'         /  '}</span><span className="text-emerald-400">{'│'}</span><span className="text-orange-400">{'  \\'}</span>{'\n'}
      <span className="text-blue-300">{'        ·   '}</span><span className="text-emerald-400">{'✦'}</span><span className="text-blue-300">{'   ·'}</span>
    </pre>
  )
}

export function Chat() {
  const { agent: agentParam } = useParams<{ agent?: string }>()
  const navigate = useNavigate()
  const { events } = useSSE()

  const [activeAgent, setActiveAgent] = useState(agentParam || HERMES)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [sending, setSending] = useState(false)
  const [showScrollBtn, setShowScrollBtn] = useState(false)
  const [showWelcomeTools, setShowWelcomeTools] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [handoffFrom, setHandoffFrom] = useState<string | null>(null)
  const [specialists, setSpecialists] = useState<AgentResponse[]>([])

  const [previewFile, setPreviewFile] = useState<FileContentResponse | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [copied, setCopied] = useState(false)

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
      if (name === activeAgent) {
        const newActive = next[Math.min(idx, next.length - 1)] || HERMES
        setTimeout(() => switchToAgent(newActive), 0)
      }
      return next
    })
  }, [activeAgent]) // eslint-disable-line react-hooks/exhaustive-deps

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
  const abortRef = useRef<AbortController | null>(null)
  const streamStartEventCount = useRef(0)

  const fetchAgents = useCallback(() => {
    api.getAgents()
      .then(list => {
        setSpecialists((list ?? []).filter(a => !META_AGENTS.has(a.name)))
      })
      .catch(() => {})
  }, [])

  useEffect(() => { fetchAgents() }, [fetchAgents])

  useEffect(() => {
    const last = events[events.length - 1]
    if (!last) return
    if (last.type === 'agent.created' || last.type === 'agent.deleted') {
      fetchAgents()
    }
  }, [events, fetchAgents])

  useEffect(() => { ensureTab(activeAgent) }, [activeAgent, ensureTab])

  useEffect(() => {
    if (specialists.length === 0) return
    const specialistNames = new Set(specialists.map(a => a.name))
    setOpenTabs(prev => prev.filter(t => META_AGENTS.has(t) || specialistNames.has(t) || t === activeAgent))
  }, [specialists, activeAgent])

  const reconnectAbortRef = useRef<AbortController | null>(null)

  useEffect(() => {
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
        // Mark DM as read when viewing
        api.markChatRead(activeAgent).catch(() => {})
        setLoaded(true)

        api.chatStatus(activeAgent)
          .then(status => {
            if (!status?.streaming) return

            setMessages(prev => [...prev, { role: 'assistant', content: '', toolCalls: [], streaming: true }])
            setSending(true)

            const abort = new AbortController()
            reconnectAbortRef.current = abort

            let finalContent = ''
            const wrappedHandler = (event: ChatEvent) => {
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

  const checkForHandoff = useCallback((finalContent: string) => {
    if (activeAgent !== HERMES) return
    const match = finalContent.match(HANDOFF_RE)
    if (match) {
      const target = match[1].trim()
      setHandoffFrom(HERMES)
      setActiveAgent(target)
    }
  }, [activeAgent])

  const send = async (text: string) => {
    const msg = text.trim()
    if (!msg || sending) return
    setMessages(prev => [...prev, { role: 'user', content: msg }])
    setMessages(prev => [...prev, { role: 'assistant', content: '', toolCalls: [], streaming: true }])
    setSending(true)

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

  useEffect(() => {
    if (agentParam && agentParam !== activeAgent) {
      setActiveAgent(agentParam)
    }
  }, [agentParam]) // eslint-disable-line react-hooks/exhaustive-deps

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
    setShowWelcomeTools(false)
    setActiveAgent(name)
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

  const isHermes = activeAgent === HERMES
  const activeAgentData = specialists.find(a => a.name === activeAgent)
  const agentNames = new Set([HERMES, 'mother', ...specialists.map(a => a.name)])

  const agentDisplayInfo = useMemo(() => {
    const m = new Map<string, { displayName: string; title: string; avatar: string }>()
    for (const a of specialists) {
      m.set(a.name, {
        displayName: a.display_name || a.name,
        title: a.title || '',
        avatar: a.avatar || '',
      })
    }
    m.set(HERMES, { displayName: 'Hermes', title: 'Orchestrator', avatar: 'n2' })
    m.set('mother', { displayName: 'Mother', title: 'Agent Builder', avatar: 'n6' })
    return m
  }, [specialists])

  const agentNamesList = useMemo(() => [...agentNames], [agentNames])

  return (
    <div className="flex flex-col h-[calc(100vh-5rem)] md:h-[calc(100vh-3rem)]">
      {/* Tab bar + actions */}
      <div className="flex items-end border-b border-border">
        <div className="flex-1 min-w-0 overflow-x-auto scrollbar-none">
          <TabBar
            tabs={openTabs}
            activeAgent={activeAgent}
            onSelect={switchToAgent}
            onClose={closeTab}
            displayInfo={agentDisplayInfo}
          />
        </div>
        <div className="flex items-center gap-1 pl-2 pb-1.5 flex-shrink-0">
          {(() => {
            const totals = messages.reduce((acc, m) => {
              if (m.metrics) {
                acc.cost += m.metrics.cost_usd
                acc.tokens += m.metrics.input_tokens + m.metrics.output_tokens
              }
              return acc
            }, { cost: 0, tokens: 0 })
            if (totals.tokens === 0) return null
            return (
              <span className="text-[11px] text-muted-foreground/60 font-mono pr-2 hidden sm:inline">
                {totals.cost >= 0.01 ? `$${totals.cost.toFixed(2)}` : `$${totals.cost.toFixed(4)}`}
                {' · '}
                {totals.tokens >= 1000 ? `${(totals.tokens / 1000).toFixed(1)}k` : totals.tokens} tokens
              </span>
            )
          })()}
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

        {messages.map((msg, i) => (
          <MessageBubble
            key={i}
            msg={msg}
            msgIdx={i}
            agentName={activeAgent}
            agentDisplayName={agentDisplayInfo.get(activeAgent)?.displayName}
            agentAvatar={agentDisplayInfo.get(activeAgent)?.avatar}
            agentNames={agentNames}
            onToggleToolCall={toggleToolCall}
            onFileClick={openFilePreview}
            onSwitchAgent={switchToAgent}
          />
        ))}

        {/* Specialist empty state */}
        {!isHermes && messages.length === 0 && loaded && (() => {
          const info = agentDisplayInfo.get(activeAgent)
          const displayName = info?.displayName || activeAgent
          const title = info?.title || ''
          const systemText = activeAgentData?.system || ''
          const descriptionRaw = systemText.replace(/^you are /i, '').split(/\n/)[0]?.trim() || ''
          const description = descriptionRaw.length > 200 ? descriptionRaw.slice(0, 200).replace(/\s+\S*$/, '') + '...' : descriptionRaw
          const examplePrompts: string[] = []
          const tools = activeAgentData?.tools || []
          for (const tool of tools) {
            if (examplePrompts.length >= 3) break
            const name = tool.includes('__') ? tool.split('__').pop()! : tool
            const parts = name.split('_')
            const verb = parts[0]
            const noun = parts.slice(1).join(' ')
            if (verb === 'list' && noun) examplePrompts.push(`Show me all ${noun}`)
            else if (verb === 'get' && noun) examplePrompts.push(`Look up a specific ${noun.replace(/s$/, '')}`)
            else if (verb === 'create' && noun) examplePrompts.push(`Help me create a new ${noun.replace(/s$/, '')}`)
            else if (verb === 'search' && noun) examplePrompts.push(`Search for ${noun}`)
            else if (verb === 'send' && noun) examplePrompts.push(`Send a ${noun.replace(/s$/, '')}`)
          }
          if (examplePrompts.length === 0) {
            examplePrompts.push(`What can you help me with?`)
            if (title) examplePrompts.push(`Tell me about your role as ${title}`)
          }
          return (
          <div className="flex items-center justify-center h-full">
            <div className="text-center space-y-6 max-w-md px-4">
              <div className="flex justify-center">
                <AgentAvatar name={activeAgent} displayName={info?.displayName} avatar={info?.avatar} size={16} />
              </div>
              <div className="space-y-1">
                <h2 className="text-xl font-bold text-foreground">{displayName}</h2>
                {title && <p className="text-sm font-medium text-muted-foreground">{title}</p>}
              </div>
              {description && (
                <p className="text-sm text-muted-foreground leading-relaxed max-w-sm mx-auto">{description}</p>
              )}
              {activeAgentData?.team && activeAgentData.team.length > 0 && (
                <div className="flex flex-wrap gap-1.5 justify-center">
                  <span className="text-xs text-muted-foreground mr-1">Team:</span>
                  {activeAgentData.team.map(member => (
                    <button key={member} onClick={() => switchToAgent(member)}
                      className="text-xs px-2.5 py-1 rounded-full border border-border text-muted-foreground hover:text-foreground hover:border-primary/50 hover:bg-accent/30 transition-colors"
                    >
                      {agentDisplayInfo.get(member)?.displayName || member}
                    </button>
                  ))}
                </div>
              )}
              <div className="space-y-2 pt-2">
                <p className="text-xs text-muted-foreground font-medium uppercase tracking-wider">Try asking</p>
                <div className="flex flex-col gap-2 items-center">
                  {examplePrompts.map((prompt, idx) => (
                    <button key={idx} onClick={() => send(prompt)}
                      className="text-sm px-4 py-2 rounded-lg border border-border text-muted-foreground hover:text-foreground hover:border-primary/50 hover:bg-accent/30 transition-colors max-w-xs"
                    >
                      {prompt}
                    </button>
                  ))}
                </div>
              </div>
              {tools.length > 0 && (
                <div className="pt-2">
                  <button onClick={() => setShowWelcomeTools(!showWelcomeTools)}
                    className="text-xs text-muted-foreground hover:text-foreground transition-colors inline-flex items-center gap-1"
                  >
                    <svg className={`w-3 h-3 transition-transform ${showWelcomeTools ? 'rotate-90' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
                    </svg>
                    {tools.length} tool{tools.length !== 1 ? 's' : ''} available
                  </button>
                  {showWelcomeTools && (
                    <div className="flex flex-wrap gap-1.5 justify-center mt-2">
                      {tools.map(tool => (
                        <span key={tool} className="text-xs px-2 py-0.5 rounded-full bg-muted text-muted-foreground font-mono">{tool}</span>
                      ))}
                    </div>
                  )}
                </div>
              )}
              {handoffFrom && (
                <p className="text-xs text-muted-foreground">
                  <span className="text-emerald-400">{'✦'}</span> Hermes connected you here
                </p>
              )}
            </div>
          </div>
          )
        })()}

        <div ref={bottomRef} />

        {showScrollBtn && (
          <ScrollToBottom onClick={() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' })} />
        )}
      </div>

      {/* Input */}
      <ChatInput
        onSend={send}
        sending={sending}
        placeholder={isHermes ? 'Tell Hermes what you need...' : `Message ${agentDisplayInfo.get(activeAgent)?.displayName || activeAgent}...`}
        borderColor={isHermes ? 'border-border focus:border-primary' : 'border-emerald-500/30 focus:border-emerald-500/60'}
        agentNames={agentNamesList}
        agentDisplayInfo={agentDisplayInfo}
      />

      {/* File preview modal */}
      {previewLoading && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center">
          <div className="animate-pulse text-white">Loading preview...</div>
        </div>
      )}
      {previewFile && (
        <FilePreview file={previewFile} onClose={() => setPreviewFile(null)} />
      )}
    </div>
  )
}
