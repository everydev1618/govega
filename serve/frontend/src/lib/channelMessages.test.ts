import { describe, it, expect } from 'vitest'
import { applyChannelMessageEvent, type ChannelMessageItem, type StreamingMessage } from './channelMessages'
import type { ChannelEvent } from './types'

function msgEvent(overrides: Partial<ChannelEvent>): ChannelEvent {
  return {
    type: 'channel.message',
    channel: 'launch',
    agent: 'quill',
    role: 'assistant',
    content: 'hello team',
    ...overrides,
  }
}

describe('applyChannelMessageEvent', () => {
  it('appends an agent post to an empty list', () => {
    const out = applyChannelMessageEvent([], msgEvent({ message_id: 1, content: 'shipped it' }))
    expect(out).toHaveLength(1)
    expect(out[0].content).toBe('shipped it')
    expect(out[0].agent).toBe('quill')
    expect((out[0] as StreamingMessage).streaming).toBe(false)
  })

  it('skips the user role echo (shown optimistically already)', () => {
    const start: ChannelMessageItem[] = []
    const out = applyChannelMessageEvent(start, msgEvent({ message_id: 5, role: 'user', content: 'my message' }))
    expect(out).toBe(start) // same reference — no change
  })

  it('dedups by message_id (broker + stream deliver the same post)', () => {
    const first = applyChannelMessageEvent([], msgEvent({ message_id: 42 }))
    const second = applyChannelMessageEvent(first, msgEvent({ message_id: 42 }))
    expect(second).toBe(first) // idempotent — same reference
    expect(second).toHaveLength(1)
  })

  it('dedups against messages already loaded from the store', () => {
    const loaded: ChannelMessageItem[] = [
      { id: 7, channel_id: 'c', agent: 'scout', role: 'assistant', content: 'listings', created_at: '', reply_count: 0 },
    ]
    const out = applyChannelMessageEvent(loaded, msgEvent({ message_id: 7 }))
    expect(out).toBe(loaded)
  })

  it('inserts before a trailing streaming placeholder so the live reply stays last', () => {
    const placeholder: StreamingMessage = { agent: 'iris', role: 'assistant', content: 'typing', streaming: true }
    const out = applyChannelMessageEvent([placeholder], msgEvent({ message_id: 9, agent: 'quill', content: 'aside' }))
    expect(out).toHaveLength(2)
    expect(out[0].content).toBe('aside')          // inserted post
    expect((out[1] as StreamingMessage).streaming).toBe(true) // placeholder still last
  })

  it('applies a post lacking a message_id (no dedup key) by appending', () => {
    const out = applyChannelMessageEvent([], msgEvent({ content: 'no id here' }))
    expect(out).toHaveLength(1)
    expect(out[0].content).toBe('no id here')
  })
})
