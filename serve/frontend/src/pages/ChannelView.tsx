import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from '../lib/api'
import { useChannelStream } from '../hooks/useChannelStream'
import type { AgentResponse, Channel, ChannelMessage } from '../lib/types'
import { AgentAvatar, UserAvatar } from '../components/chat/AgentAvatar'
import { ThreadPanel } from '../components/chat/ThreadPanel'
import { ScrollToBottom } from '../components/chat/ScrollToBottom'
import { ToolCallBadges, statusDotClass, shortToolName, ActivityConstellation, ActivityNarrative } from '../components/chat/ToolCallDisplay'
import { getUserName } from '../components/UserIdentityPrompt'

const META_AGENTS = new Set(['hermes', 'mother'])

export function ChannelView() {
  const { name } = useParams<{ name: string }>()
  const channelName = name || ''

  const [channel, setChannel] = useState<Channel | null>(null)
  const [agents, setAgents] = useState<AgentResponse[]>([])
  const [threadMessageId, setThreadMessageId] = useState<number | null>(null)
  const [showScrollBtn, setShowScrollBtn] = useState(false)

  const { messages, setMessages, typingAgents, postMessage, isStreaming, loadMessages } = useChannelStream(channelName)

  const bottomRef = useRef<HTMLDivElement>(null)
  const messagesRef = useRef<HTMLDivElement>(null)

  // Load channel info and messages
  useEffect(() => {
    if (!channelName) return
    api.getChannel(channelName).then(setChannel).catch(() => {})
    api.getAgents().then(list => setAgents(list ?? [])).catch(() => {})
    loadMessages()
    // Mark channel as read when viewing
    api.markChannelRead(channelName).catch(() => {})
  }, [channelName, loadMessages])

  const agentDisplayInfo = useMemo(() => {
    const m = new Map<string, { displayName: string; title: string; avatar: string }>()
    for (const a of agents) {
      m.set(a.name, {
        displayName: a.display_name || a.name,
        title: a.title || '',
        avatar: a.avatar || '',
      })
    }
    m.set('hermes', { displayName: 'Hermes', title: 'Orchestrator', avatar: 'n2' })
    m.set('mother', { displayName: 'Mother', title: 'Agent Builder', avatar: 'n6' })
    return m
  }, [agents])

  // Auto-scroll
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
  }, [channelName])

  // Input state
  const [input, setInput] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    const ta = textareaRef.current
    if (!ta) return
    ta.style.height = 'auto'
    ta.style.height = Math.min(ta.scrollHeight, 6 * 24) + 'px'
  }, [input])

  const handleSend = () => {
    const msg = input.trim()
    if (!msg || isStreaming) return
    setInput('')
    postMessage(msg)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  // Escape to close thread
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && threadMessageId !== null) {
        setThreadMessageId(null)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [threadMessageId])

  const teamAvatars = channel?.team || []

  return (
    <div className="flex h-full min-h-0">
      {/* Main channel area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <div className="flex items-center gap-3">
            <h2 className="text-lg font-semibold">#{channelName}</h2>
            {channel?.description && (
              <span className="text-sm text-muted-foreground hidden sm:inline">{channel.description}</span>
            )}
          </div>
          <div className="flex items-center gap-1">
            {teamAvatars.map(agentName => {
              const info = agentDisplayInfo.get(agentName)
              return (
                <div key={agentName} title={info?.displayName || agentName}>
                  <AgentAvatar name={agentName} displayName={info?.displayName} avatar={info?.avatar} size={6} />
                </div>
              )
            })}
          </div>
        </div>

        {/* Messages */}
        <div ref={messagesRef} className="flex-1 overflow-auto px-4 space-y-4 pb-4 relative">
          {messages.length === 0 && (
            <div className="flex items-center justify-center h-full">
              <div className="text-center space-y-2">
                <h3 className="text-lg font-semibold text-foreground">#{channelName}</h3>
                <p className="text-sm text-muted-foreground">
                  {channel?.description || 'Start the conversation by sending a message.'}
                </p>
              </div>
            </div>
          )}

          {messages.map((msg, i) => {
            const isUser = msg.role === 'user'
            const agentName = msg.agent || ''
            const info = agentDisplayInfo.get(agentName)
            const isStreamingMsg = 'streaming' in msg && msg.streaming
            const replyCount = 'reply_count' in msg ? (msg as ChannelMessage).reply_count : 0
            const msgId = 'id' in msg ? msg.id : undefined
            const sender = 'sender' in msg ? msg.sender : undefined
            const currentUser = getUserName()
            const displayName = isUser
              ? (sender && sender !== currentUser ? sender : (sender || 'You'))
              : (info?.displayName || agentName)

            return (
              <div key={i} className="flex gap-2.5">
                {isUser ? (
                  <UserAvatar name={sender || undefined} />
                ) : (
                  <AgentAvatar name={agentName} displayName={info?.displayName} avatar={info?.avatar} />
                )}
                <div className="flex-1 min-w-0">
                  <div className="flex items-baseline gap-2">
                    <span className="text-sm font-semibold text-foreground">
                      {displayName}
                    </span>
                    {'created_at' in msg && msg.created_at && (
                      <span className="text-[11px] text-muted-foreground/60">
                        {new Date(msg.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                      </span>
                    )}
                  </div>
                  {msg.content ? (
                    <div className="text-sm prose prose-invert prose-sm max-w-none prose-p:my-1 prose-code:text-purple-400 prose-code:before:content-none prose-code:after:content-none">
                      <Markdown remarkPlugins={[remarkGfm]}>{msg.content}</Markdown>
                    </div>
                  ) : isStreamingMsg ? (
                    <p className="text-xs text-muted-foreground italic">Thinking...</p>
                  ) : null}
                  {isStreamingMsg && msg.content && (
                    <span className="inline-block w-1.5 h-4 bg-primary animate-pulse ml-0.5 align-text-bottom rounded-sm" />
                  )}
                  {'toolCalls' in msg && msg.toolCalls && msg.toolCalls.length > 0 && (
                    <div className="my-1">
                      <div className="flex flex-row flex-wrap gap-1.5">
                        {msg.toolCalls.map((tc) => (
                          <span key={tc.id}
                            className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-mono border border-border bg-background/50 text-muted-foreground">
                            <span className={`w-1.5 h-1.5 rounded-full ${statusDotClass(tc)}`} />
                            <span>{shortToolName(tc.name)}</span>
                            {tc.duration_ms != null && <span className="text-muted-foreground">{tc.duration_ms}ms</span>}
                          </span>
                        ))}
                      </div>
                      {isStreamingMsg && msg.toolCalls.length > 0 && (
                        <div className="flex items-center gap-1 pt-1 constellation-activity">
                          <ActivityConstellation tools={msg.toolCalls as any} />
                          <ActivityNarrative tools={msg.toolCalls as any} />
                        </div>
                      )}
                    </div>
                  )}
                  {'error' in msg && msg.error && (
                    <p className="text-xs text-red-400 mt-1">{msg.error}</p>
                  )}
                  {'metrics' in msg && msg.metrics && !isStreamingMsg && (
                    <div className="mt-1 text-[11px] text-muted-foreground/60 font-mono">
                      {msg.metrics.cost_usd >= 0.01 ? `$${msg.metrics.cost_usd.toFixed(2)}` : `$${msg.metrics.cost_usd.toFixed(4)}`}
                      {' · '}
                      {(msg.metrics.input_tokens + msg.metrics.output_tokens).toLocaleString()} tokens
                    </div>
                  )}
                  {replyCount > 0 && msgId != null && (
                    <button
                      onClick={() => setThreadMessageId(msgId as number)}
                      className="mt-1 text-xs text-primary hover:underline"
                    >
                      {replyCount} {replyCount === 1 ? 'reply' : 'replies'}
                    </button>
                  )}
                </div>
              </div>
            )
          })}

          {/* Typing indicators */}
          {typingAgents.size > 0 && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground italic px-1">
              {[...typingAgents].map(a => {
                const info = agentDisplayInfo.get(a)
                return info?.displayName || a
              }).join(', ')} {typingAgents.size === 1 ? 'is' : 'are'} typing...
            </div>
          )}

          <div ref={bottomRef} />

          {showScrollBtn && (
            <ScrollToBottom onClick={() => bottomRef.current?.scrollIntoView({ behavior: 'smooth' })} />
          )}
        </div>

        {/* Input */}
        <div className="px-4 pt-3 pb-4 border-t border-border">
          <div className="flex gap-2 items-end">
            <textarea
              ref={textareaRef}
              rows={1}
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={`Message #${channelName}...`}
              disabled={isStreaming}
              className="flex-1 px-4 py-2.5 rounded-xl bg-background border border-border text-sm focus:outline-none focus:border-primary disabled:opacity-50 resize-none overflow-y-auto transition-colors"
              style={{ maxHeight: '144px' }}
            />
            <button
              onClick={handleSend}
              disabled={isStreaming || !input.trim()}
              className="p-2.5 rounded-xl bg-primary text-primary-foreground disabled:opacity-50 flex-shrink-0"
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 10.5L12 3m0 0l7.5 7.5M12 3v18" />
              </svg>
            </button>
          </div>
          <p className="text-xs text-muted-foreground px-1 mt-1">Enter to send · Shift+Enter for new line</p>
        </div>
      </div>

      {/* Thread panel */}
      {threadMessageId !== null && (
        <ThreadPanel
          channelName={channelName}
          messageId={threadMessageId}
          agentDisplayInfo={agentDisplayInfo}
          onClose={() => setThreadMessageId(null)}
        />
      )}
    </div>
  )
}
