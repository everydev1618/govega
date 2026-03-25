import { useState, useEffect, useRef, useCallback } from 'react'
import { api } from '../../lib/api'
import type { ChannelMessage, ChannelEvent, ChatEventMetrics } from '../../lib/types'
import { AgentAvatar, UserAvatar } from './AgentAvatar'
import { getUserName } from '../UserIdentityPrompt'
import Markdown from 'react-markdown'

interface StreamingReply {
  agent: string
  sender?: string
  role: string
  content: string
  streaming: boolean
  error?: string
  metrics?: ChatEventMetrics
}

interface ThreadPanelProps {
  channelName: string
  messageId: number
  agentDisplayInfo: Map<string, { displayName: string; title: string; avatar: string }>
  onClose: () => void
}

export function ThreadPanel({ channelName, messageId, agentDisplayInfo, onClose }: ThreadPanelProps) {
  const [replies, setReplies] = useState<(ChannelMessage | StreamingReply)[]>([])
  const [original, setOriginal] = useState<ChannelMessage | null>(null)
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    api.getThreadMessages(channelName, messageId)
      .then(msgs => {
        if (msgs && msgs.length > 0) {
          setOriginal(msgs[0])
          setReplies(msgs.slice(1))
        }
      })
      .catch(() => {})
  }, [channelName, messageId])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [replies])

  // Auto-resize textarea
  useEffect(() => {
    const ta = textareaRef.current
    if (!ta) return
    ta.style.height = 'auto'
    ta.style.height = Math.min(ta.scrollHeight, 4 * 24) + 'px'
  }, [input])

  const sendReply = useCallback(async () => {
    const msg = input.trim()
    if (!msg || sending) return
    setInput('')

    const userReply: StreamingReply = { agent: '', sender: getUserName() || '', role: 'user', content: msg, streaming: false }
    setReplies(prev => [...prev, userReply])

    const assistantReply: StreamingReply = { agent: '', role: 'assistant', content: '', streaming: true }
    setReplies(prev => [...prev, assistantReply])
    setSending(true)

    const abort = new AbortController()
    abortRef.current = abort

    const handleEvent = (event: ChannelEvent) => {
      if (event.type === 'channel.text_delta') {
        setReplies(prev => {
          const msgs = [...prev]
          const last = msgs[msgs.length - 1] as StreamingReply
          if (last?.streaming) {
            msgs[msgs.length - 1] = { ...last, agent: event.agent || last.agent, content: last.content + (event.delta || '') }
          }
          return msgs
        })
      } else if (event.type === 'channel.done') {
        setReplies(prev => {
          const msgs = [...prev]
          const last = msgs[msgs.length - 1] as StreamingReply
          if (last?.streaming) {
            msgs[msgs.length - 1] = { ...last, streaming: false, metrics: event.metrics }
          }
          return msgs
        })
      } else if (event.type === 'channel.error') {
        setReplies(prev => {
          const msgs = [...prev]
          const last = msgs[msgs.length - 1] as StreamingReply
          if (last?.streaming) {
            msgs[msgs.length - 1] = { ...last, streaming: false, error: event.error }
          }
          return msgs
        })
      }
    }

    try {
      await api.channelStream(channelName, msg, handleEvent, abort.signal, messageId)
    } catch { /* ignore */ }
    finally {
      setSending(false)
      abortRef.current = null
    }
  }, [input, sending, channelName, messageId])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendReply()
    }
    if (e.key === 'Escape') {
      onClose()
    }
  }

  const renderMessage = (msg: ChannelMessage | StreamingReply, idx: number) => {
    const isUser = msg.role === 'user'
    const agentName = msg.agent || 'unknown'
    const info = agentDisplayInfo.get(agentName)
    const streaming = 'streaming' in msg && msg.streaming
    const sender = 'sender' in msg ? (msg as ChannelMessage).sender : undefined
    const currentUser = getUserName()
    const displayName = isUser
      ? (sender && sender !== currentUser ? sender : (sender || 'You'))
      : (info?.displayName || agentName)

    return (
      <div key={idx} className="px-4 py-2">
        <div className="flex gap-2">
          {isUser ? (
            <UserAvatar name={sender || undefined} size={6} />
          ) : (
            <AgentAvatar name={agentName} displayName={info?.displayName} avatar={info?.avatar} size={6} />
          )}
          <div className="flex-1 min-w-0">
            <p className="text-xs font-semibold text-foreground">
              {displayName}
            </p>
            {msg.content ? (
              <div className="text-sm prose prose-invert prose-sm max-w-none prose-p:my-1 prose-code:text-purple-400 prose-code:before:content-none prose-code:after:content-none">
                <Markdown>{msg.content}</Markdown>
              </div>
            ) : streaming ? (
              <p className="text-xs text-muted-foreground italic">Thinking...</p>
            ) : null}
            {streaming && msg.content && (
              <span className="inline-block w-1.5 h-3 bg-primary animate-pulse ml-0.5 align-text-bottom rounded-sm" />
            )}
            {'error' in msg && msg.error && (
              <p className="text-xs text-red-400 mt-1">{msg.error}</p>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="fixed inset-0 z-40 md:static md:z-auto w-full md:w-[350px] border-l border-border bg-card flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-border">
        <h3 className="text-sm font-semibold">Thread in #{channelName}</h3>
        <button
          onClick={onClose}
          className="text-muted-foreground hover:text-foreground transition-colors p-1 rounded hover:bg-accent"
        >
          <svg width="16" height="16" viewBox="0 0 20 20" fill="none">
            <path d="M5 5l10 10M15 5L5 15" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </svg>
        </button>
      </div>

      {/* Original message */}
      {original && (
        <div className="border-b border-border">
          {renderMessage(original, -1)}
        </div>
      )}

      {/* Replies */}
      <div className="flex-1 overflow-y-auto">
        {replies.length > 0 && (
          <div className="px-4 py-2">
            <p className="text-xs text-muted-foreground">{replies.length} {replies.length === 1 ? 'reply' : 'replies'}</p>
          </div>
        )}
        {replies.map((msg, i) => renderMessage(msg, i))}
        <div ref={bottomRef} />
      </div>

      {/* Reply input */}
      <div className="p-3 border-t border-border">
        <div className="flex gap-2 items-end">
          <textarea
            ref={textareaRef}
            rows={1}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Reply..."
            disabled={sending}
            className="flex-1 px-3 py-2 rounded-lg bg-background border border-border text-sm focus:outline-none focus:border-primary disabled:opacity-50 resize-none overflow-y-auto"
            style={{ maxHeight: '96px' }}
          />
          <button
            onClick={sendReply}
            disabled={sending || !input.trim()}
            className="p-2 rounded-lg bg-primary text-primary-foreground disabled:opacity-50 flex-shrink-0"
          >
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 10.5L12 3m0 0l7.5 7.5M12 3v18" />
            </svg>
          </button>
        </div>
      </div>
    </div>
  )
}
