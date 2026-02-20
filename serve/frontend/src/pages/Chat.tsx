import { useState, useRef, useEffect, useCallback } from 'react'
import Markdown from 'react-markdown'
import { useSSE } from '../hooks/useSSE'
import { api } from '../lib/api'
import type { ChatEvent, ToolCallState } from '../lib/types'

const HERMES = 'hermes'

// Matches the handoff line Hermes emits: → Handing you to **agent-name** for this conversation.
const HANDOFF_RE = /→\s+Handing you to \*\*([^*]+)\*\*/

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
}

function ToolCallPanel({ tc, onToggle }: { tc: ToolCallState; onToggle: () => void }) {
  const statusDot =
    tc.status === 'running'
      ? 'bg-yellow-400 animate-pulse'
      : tc.status === 'error'
        ? 'bg-red-400'
        : 'bg-green-400'

  return (
    <div className="my-2 rounded-lg border border-border bg-background/50 text-sm overflow-hidden">
      <button
        onClick={onToggle}
        className="flex items-center gap-2 w-full px-3 py-2 hover:bg-accent/30 transition-colors text-left"
      >
        <span className={`w-2 h-2 rounded-full flex-shrink-0 ${statusDot}`} />
        <span className="font-mono text-xs font-medium text-foreground">{tc.name}</span>
        {tc.duration_ms != null && (
          <span className="ml-auto text-xs text-muted-foreground">{tc.duration_ms}ms</span>
        )}
        <svg
          className={`w-3 h-3 text-muted-foreground transition-transform ${tc.collapsed ? '' : 'rotate-180'}`}
          fill="none" viewBox="0 0 24 24" stroke="currentColor"
        >
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
        </svg>
      </button>
      {!tc.collapsed && (
        <div className="px-3 pb-2 space-y-1.5 border-t border-border pt-2">
          {tc.arguments && Object.keys(tc.arguments).length > 0 && (
            <div>
              <span className="text-xs text-muted-foreground">Arguments</span>
              <pre className="mt-0.5 text-xs font-mono bg-background rounded p-2 overflow-x-auto border border-border whitespace-pre-wrap">
                {JSON.stringify(tc.arguments, null, 2)}
              </pre>
            </div>
          )}
          {tc.result != null && (
            <div>
              <span className="text-xs text-muted-foreground">Result</span>
              <pre className="mt-0.5 text-xs font-mono bg-background rounded p-2 overflow-x-auto border border-border whitespace-pre-wrap max-h-60">
                {tc.result}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function TypingIndicator() {
  return (
    <span className="inline-flex items-center gap-1 text-muted-foreground py-1">
      <span className="typing-dot" style={{ animationDelay: '0ms' }} />
      <span className="typing-dot" style={{ animationDelay: '150ms' }} />
      <span className="typing-dot" style={{ animationDelay: '300ms' }} />
    </span>
  )
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

function HandoffBanner({ from, to, onSwitch }: { from: string; to: string; onSwitch: () => void }) {
  return (
    <div className="flex items-center gap-3 px-4 py-3 rounded-xl border border-primary/30 bg-primary/5 text-sm">
      <div className="flex items-center gap-1.5 text-muted-foreground">
        <AgentAvatar name={from} />
        <svg className="w-4 h-4 text-primary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
        </svg>
        <AgentAvatar name={to} />
      </div>
      <span className="flex-1 text-muted-foreground">
        Hermes connected you with <span className="font-semibold text-foreground">{to}</span>
      </span>
      <button
        onClick={onSwitch}
        className="px-3 py-1.5 rounded-lg bg-primary text-primary-foreground text-xs font-medium hover:opacity-90 transition-opacity"
      >
        Talk to {to} →
      </button>
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
  const { events } = useSSE()

  // Which agent the chat is currently directed to
  const [activeAgent, setActiveAgent] = useState(HERMES)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const [showScrollBtn, setShowScrollBtn] = useState(false)
  const [loaded, setLoaded] = useState(false)
  // Agent Hermes wants to hand us off to, pending user confirmation
  const [pendingHandoff, setPendingHandoff] = useState<string | null>(null)

  const bottomRef = useRef<HTMLDivElement>(null)
  const messagesRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  // Track the event index at stream start so we only look at events fired during this stream
  const streamStartEventCount = useRef(0)

  // Load history for the active agent whenever it changes
  useEffect(() => {
    setLoaded(false)
    setMessages([])
    setPendingHandoff(null)
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
      .finally(() => setLoaded(true))
  }, [activeAgent])

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
          updated.toolCalls!.push({
            id: event.tool_call_id!,
            name: event.tool_name!,
            arguments: (event.arguments || {}) as Record<string, unknown>,
            status: 'running',
            collapsed: true,
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
        case 'error':
          updated.content += `\n\nError: ${event.error}`
          updated.streaming = false
          break
        case 'done':
          updated.streaming = false
          break
      }

      msgs[msgs.length - 1] = updated
      return msgs
    })
  }, [])

  // After a stream ends, check if Hermes emitted a handoff line
  const checkForHandoff = useCallback((finalContent: string) => {
    if (activeAgent !== HERMES) return
    const match = finalContent.match(HANDOFF_RE)
    if (match) {
      setPendingHandoff(match[1].trim())
    }
  }, [activeAgent])

  const send = async (text?: string) => {
    const msg = (text ?? input).trim()
    if (!msg || sending) return
    setInput('')
    setPendingHandoff(null)
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
          msgs[msgs.length - 1] = { ...last, content: last.content + `\n\nError: ${err}`, streaming: false }
        }
        return msgs
      })
    } finally {
      setSending(false)
      abortRef.current = null
    }
  }

  const switchToAgent = async (name: string) => {
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
      setSending(false)
    }
    setPendingHandoff(null)
    setActiveAgent(name)
    // History load handled by the activeAgent useEffect
  }

  const clearChat = async () => {
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
      setSending(false)
    }
    setMessages([])
    setPendingHandoff(null)
    try { await api.resetChat(activeAgent) } catch { /* best-effort */ }
  }

  const goBackToHermes = () => switchToAgent(HERMES)

  const toggleToolCall = (msgIdx: number, tcIdx: number) => {
    setMessages(prev => {
      const msgs = [...prev]
      const msg = { ...msgs[msgIdx], toolCalls: [...(msgs[msgIdx].toolCalls || [])] }
      msg.toolCalls![tcIdx] = { ...msg.toolCalls![tcIdx], collapsed: !msg.toolCalls![tcIdx].collapsed }
      msgs[msgIdx] = msg
      return msgs
    })
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  const isHermes = activeAgent === HERMES

  return (
    <div className="flex flex-col h-[calc(100vh-3rem)]">
      {/* Header */}
      <div className="flex items-center gap-3 pb-3 border-b border-border mb-3">
        {!isHermes && (
          <button
            onClick={goBackToHermes}
            title="Back to Hermes"
            className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors flex-shrink-0"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" />
            </svg>
          </button>
        )}
        <div className="w-9 h-9 rounded-full bg-primary/20 text-primary flex items-center justify-center flex-shrink-0 text-sm font-semibold">
          {activeAgent[0]?.toUpperCase()}
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-lg font-semibold">{activeAgent}</h2>
          <p className="text-xs text-muted-foreground">
            {isHermes
              ? 'Cosmic orchestrator — routes your goals across the whole agent universe'
              : 'Specialist agent — connected by Hermes'}
          </p>
        </div>
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
          <div key={i} className={`flex gap-2.5 ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            {msg.role === 'assistant' && <AgentAvatar name={activeAgent} />}
            {msg.role === 'user' ? (
              <div className="max-w-[75%] rounded-2xl shadow-sm px-4 py-2.5 text-sm whitespace-pre-wrap bg-primary text-primary-foreground">
                {msg.content}
              </div>
            ) : (
              <div className="max-w-[75%] rounded-2xl shadow-sm px-4 py-2.5 text-sm bg-card border border-border prose prose-invert prose-sm prose-p:my-2 prose-headings:my-3 prose-ul:my-2 prose-ol:my-2 prose-li:my-0.5 prose-pre:bg-background prose-pre:border prose-pre:border-border prose-code:text-purple-400 prose-code:before:content-none prose-code:after:content-none max-w-none">
                {msg.streaming && !msg.content && (!msg.toolCalls || msg.toolCalls.length === 0) && (
                  <TypingIndicator />
                )}
                {msg.content && <Markdown>{msg.content}</Markdown>}
                {msg.streaming && msg.content && (
                  <span className="inline-block w-1.5 h-4 bg-primary animate-pulse ml-0.5 align-text-bottom rounded-sm" />
                )}
                {msg.toolCalls?.map((tc, j) => (
                  <ToolCallPanel key={tc.id} tc={tc} onToggle={() => toggleToolCall(i, j)} />
                ))}
              </div>
            )}
            {msg.role === 'user' && <UserAvatar />}
          </div>
        ))}

        {/* Handoff banner — shown after Hermes routes to a specialist */}
        {pendingHandoff && (
          <HandoffBanner
            from={HERMES}
            to={pendingHandoff}
            onSwitch={() => switchToAgent(pendingHandoff)}
          />
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
          <textarea
            ref={textareaRef}
            rows={1}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={isHermes ? 'Tell Hermes what you need…' : `Message ${activeAgent}…`}
            disabled={sending}
            className="flex-1 px-4 py-2.5 rounded-xl bg-background border border-border text-sm focus:outline-none focus:border-primary disabled:opacity-50 resize-none overflow-y-auto"
            style={{ maxHeight: '144px' }}
          />
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
        <p className="text-xs text-muted-foreground px-1">Enter to send · Shift+Enter for new line</p>
      </div>
    </div>
  )
}
