import { useState, useRef, useCallback } from 'react'
import { api } from '../lib/api'
import type { ChannelEvent, ChannelMessage, ChatEventMetrics } from '../lib/types'

interface StreamingMessage {
  id?: number
  agent: string
  sender?: string
  role: string
  content: string
  streaming: boolean
  toolCalls?: { id: string; name: string; arguments: Record<string, unknown>; result?: string; duration_ms?: number; status: 'running' | 'completed' | 'error'; collapsed: boolean }[]
  error?: string
  metrics?: ChatEventMetrics
}

export function useChannelStream(channelName: string) {
  const [messages, setMessages] = useState<(ChannelMessage | StreamingMessage)[]>([])
  const [typingAgents, setTypingAgents] = useState<Set<string>>(new Set())
  const [isStreaming, setIsStreaming] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  const loadMessages = useCallback(async () => {
    try {
      const msgs = await api.getChannelMessages(channelName)
      setMessages(msgs ?? [])
    } catch { /* ignore */ }
  }, [channelName])

  const postMessage = useCallback(async (text: string, threadId?: number, agent?: string) => {
    // Optimistic user message
    const userMsg: StreamingMessage = {
      agent: '',
      sender: '',
      role: 'user',
      content: text,
      streaming: false,
    }
    setMessages(prev => [...prev, userMsg])

    // Add placeholder for assistant response
    const assistantMsg: StreamingMessage = {
      agent: agent || '',
      role: 'assistant',
      content: '',
      streaming: true,
      toolCalls: [],
    }
    setMessages(prev => [...prev, assistantMsg])
    setIsStreaming(true)

    const abort = new AbortController()
    abortRef.current = abort

    const handleEvent = (event: ChannelEvent) => {
      switch (event.type) {
        case 'channel.typing':
          setTypingAgents(prev => new Set(prev).add(event.agent))
          break
        case 'channel.message':
          // A finalized post from another participant — a teammate reacting
          // via post_to_channel, or a delegation response — arriving while
          // this stream is open. The primary agent's own reply does NOT come
          // through here (it streams via text_delta + channel.done), so this
          // won't double-render it.
          //
          // Skip role 'user': the poster's own message is already shown
          // optimistically, and the relay replays it here with a real id.
          if (event.role === 'user') break
          setMessages(prev => {
            // Dedup by id — history replay, prior loads (getChannelMessages),
            // and repeated events all carry the same message_id.
            if (event.message_id != null &&
                prev.some(m => (m as { id?: number }).id === event.message_id)) {
              return prev
            }
            const posted: StreamingMessage = {
              id: event.message_id,
              agent: event.agent || '',
              sender: event.sender,
              role: event.role || 'assistant',
              content: event.content || '',
              streaming: false,
            }
            // Keep any actively-streaming assistant placeholder pinned to the
            // bottom so the live reply stays last.
            const msgs = [...prev]
            let at = msgs.length
            while (at > 0 && (msgs[at - 1] as StreamingMessage).streaming) at--
            msgs.splice(at, 0, posted)
            return msgs
          })
          break
        case 'channel.text_delta':
          setMessages(prev => {
            const msgs = [...prev]
            const last = msgs[msgs.length - 1] as StreamingMessage
            if (last?.role === 'assistant' && last.streaming) {
              msgs[msgs.length - 1] = {
                ...last,
                agent: event.agent || last.agent,
                content: last.content + (event.delta || ''),
              }
            }
            return msgs
          })
          break
        case 'channel.tool_start':
          setMessages(prev => {
            const msgs = [...prev]
            const last = msgs[msgs.length - 1] as StreamingMessage
            if (last?.role === 'assistant' && last.streaming) {
              const tc = [...(last.toolCalls || [])]
              tc.push({
                id: event.tool_call_id || String(Date.now()),
                name: event.tool_name || '',
                arguments: event.arguments || {},
                status: 'running',
                collapsed: true,
              })
              msgs[msgs.length - 1] = { ...last, agent: event.agent || last.agent, toolCalls: tc }
            }
            return msgs
          })
          break
        case 'channel.tool_end':
          setMessages(prev => {
            const msgs = [...prev]
            const last = msgs[msgs.length - 1] as StreamingMessage
            if (last?.role === 'assistant' && last.streaming && last.toolCalls) {
              const tc = last.toolCalls.map(t =>
                t.id === event.tool_call_id
                  ? { ...t, result: event.result, duration_ms: event.duration_ms, status: (event.error ? 'error' : 'completed') as 'completed' | 'error' }
                  : t
              )
              msgs[msgs.length - 1] = { ...last, toolCalls: tc }
            }
            return msgs
          })
          break
        case 'channel.error':
          setMessages(prev => {
            const msgs = [...prev]
            const last = msgs[msgs.length - 1] as StreamingMessage
            if (last?.role === 'assistant' && last.streaming) {
              msgs[msgs.length - 1] = { ...last, error: event.error || 'Error', streaming: false }
            }
            return msgs
          })
          break
        case 'channel.done':
          setMessages(prev => {
            const msgs = [...prev]
            const last = msgs[msgs.length - 1] as StreamingMessage
            if (last?.role === 'assistant' && last.streaming) {
              msgs[msgs.length - 1] = {
                ...last,
                id: event.message_id,
                streaming: false,
                metrics: event.metrics,
              }
            }
            return msgs
          })
          setTypingAgents(prev => {
            const next = new Set(prev)
            next.delete(event.agent)
            return next
          })
          break
      }
    }

    try {
      await api.channelStream(channelName, text, handleEvent, abort.signal, threadId, agent)
    } catch {
      // Handle abort/error
    } finally {
      setIsStreaming(false)
      abortRef.current = null
      setTypingAgents(new Set())
    }
  }, [channelName])

  const cancel = useCallback(() => {
    abortRef.current?.abort()
    abortRef.current = null
    setIsStreaming(false)
  }, [])

  return { messages, setMessages, typingAgents, postMessage, isStreaming, loadMessages, cancel }
}
