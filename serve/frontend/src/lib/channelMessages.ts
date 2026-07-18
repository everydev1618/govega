import type { ChannelEvent, ChannelMessage, ChatEventMetrics } from './types'

// StreamingMessage is an in-flight or client-synthesized channel message. It
// shares the fields ChannelView renders with the stored ChannelMessage, plus
// the streaming/tool-call scaffolding the live stream needs.
export interface StreamingMessage {
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

export type ChannelMessageItem = ChannelMessage | StreamingMessage

// applyChannelMessageEvent folds a finalized `channel.message` event into the
// message list. It is the single reducer shared by two delivery paths — the
// active post stream (useChannelStream) and the persistent broker
// subscription for idle viewing (ChannelView) — so both stay consistent.
//
// It is deliberately idempotent: dedup-by-id means re-applying the same event
// (broker replay, an event arriving on both paths, useSSE's rolling buffer
// being re-scanned) is a no-op. That idempotency is what lets the two paths
// compose without coordination.
export function applyChannelMessageEvent(
  messages: ChannelMessageItem[],
  event: ChannelEvent,
): ChannelMessageItem[] {
  // The poster's own message is shown optimistically and echoed here with a
  // real id; skip role 'user' to avoid duplicating the optimistic bubble.
  if (event.role === 'user') return messages

  // Dedup by id — history replay, prior loads (getChannelMessages), and the
  // same post arriving on both the stream and the broker all carry the same
  // message_id.
  if (event.message_id != null &&
      messages.some(m => (m as { id?: number }).id === event.message_id)) {
    return messages
  }

  const posted: StreamingMessage = {
    id: event.message_id,
    agent: event.agent || '',
    sender: event.sender,
    role: event.role || 'assistant',
    content: event.content || '',
    streaming: false,
  }

  // Keep any actively-streaming assistant placeholder pinned to the bottom so
  // the live reply stays last.
  const msgs = [...messages]
  let at = msgs.length
  while (at > 0 && (msgs[at - 1] as StreamingMessage).streaming) at--
  msgs.splice(at, 0, posted)
  return msgs
}
