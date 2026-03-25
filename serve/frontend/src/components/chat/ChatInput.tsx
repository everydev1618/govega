import { useState, useRef, useEffect, useCallback } from 'react'
import { AgentAvatar } from './AgentAvatar'

interface ChatInputProps {
  onSend: (text: string) => void
  sending: boolean
  placeholder?: string
  borderColor?: string
  agentNames?: string[]
  agentDisplayInfo?: Map<string, { displayName: string; title: string; avatar: string }>
}

function MentionDropdown({
  agents,
  selectedIndex,
  onSelect,
  onHover,
  displayInfo,
}: {
  agents: string[]
  selectedIndex: number
  onSelect: (name: string) => void
  onHover: (index: number) => void
  displayInfo?: Map<string, { displayName: string; title: string; avatar: string }>
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
      {agents.map((name, i) => {
        const info = displayInfo?.get(name)
        const label = info?.displayName || name
        return (
          <button
            key={name}
            onMouseDown={e => { e.preventDefault(); onSelect(name) }}
            onMouseEnter={() => onHover(i)}
            className={`flex items-center gap-2.5 w-full px-3 py-2 text-sm transition-colors text-left ${
              i === selectedIndex ? 'bg-accent/50 text-foreground' : 'text-muted-foreground hover:bg-accent/30'
            }`}
          >
            <AgentAvatar name={name} displayName={label} avatar={info?.avatar} size={6} />
            <div className="flex flex-col min-w-0">
              <span className="truncate font-medium">{label}</span>
              {info?.title && <span className="truncate text-xs text-muted-foreground/70">{info.title}</span>}
            </div>
          </button>
        )
      })}
    </div>
  )
}

export function ChatInput({ onSend, sending, placeholder, borderColor, agentNames, agentDisplayInfo }: ChatInputProps) {
  const [input, setInput] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const mentionRef = useRef<HTMLDivElement>(null)

  // @-mention state
  const [mentionOpen, setMentionOpen] = useState(false)
  const [mentionQuery, setMentionQuery] = useState('')
  const [mentionIndex, setMentionIndex] = useState(0)
  const [mentionStartPos, setMentionStartPos] = useState(0)

  const mentionAgents = mentionOpen && agentNames
    ? agentNames
        .filter(n => n.toLowerCase().includes(mentionQuery.toLowerCase()))
        .sort((a, b) => a.localeCompare(b))
    : []

  useEffect(() => {
    if (mentionIndex >= mentionAgents.length) {
      setMentionIndex(Math.max(0, mentionAgents.length - 1))
    }
  }, [mentionAgents.length, mentionIndex])

  // Auto-resize textarea
  const resizeTextarea = useCallback(() => {
    const ta = textareaRef.current
    if (!ta) return
    ta.style.height = 'auto'
    ta.style.height = Math.min(ta.scrollHeight, 6 * 24) + 'px'
  }, [])

  useEffect(() => { resizeTextarea() }, [input, resizeTextarea])

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

  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value
    setInput(val)

    if (!agentNames?.length) return

    const cursor = e.target.selectionStart ?? val.length
    let atPos = -1
    for (let i = cursor - 1; i >= 0; i--) {
      if (val[i] === ' ' || val[i] === '\n') break
      if (val[i] === '@') {
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
      const msg = input.trim()
      if (msg && !sending) {
        setInput('')
        onSend(msg)
      }
    }
  }

  const handleSendClick = () => {
    const msg = input.trim()
    if (msg && !sending) {
      setInput('')
      onSend(msg)
    }
  }

  const borderClass = borderColor || 'border-border focus:border-primary'

  return (
    <div className="pt-3 border-t border-border space-y-1.5">
      <div className="flex gap-2 items-end">
        <div className="relative flex-1" ref={mentionRef}>
          {mentionOpen && agentNames && (
            <div className="absolute bottom-full mb-1.5 left-0 w-64 rounded-xl border border-border bg-card shadow-lg z-20 overflow-hidden">
              <div className="px-3 py-2 border-b border-border">
                <p className="text-xs text-muted-foreground font-medium">Mention an agent</p>
              </div>
              <MentionDropdown
                agents={mentionAgents}
                selectedIndex={mentionIndex}
                onSelect={selectMention}
                onHover={setMentionIndex}
                displayInfo={agentDisplayInfo}
              />
            </div>
          )}
          <textarea
            ref={textareaRef}
            rows={1}
            value={input}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            placeholder={placeholder || 'Type a message...'}
            disabled={sending}
            className={`w-full px-4 py-2.5 rounded-xl bg-background border text-sm focus:outline-none disabled:opacity-50 resize-none overflow-y-auto transition-colors ${borderClass}`}
            style={{ maxHeight: '144px' }}
          />
        </div>
        <button
          onClick={handleSendClick}
          disabled={sending || !input.trim()}
          className="p-2.5 rounded-xl bg-primary text-primary-foreground disabled:opacity-50 flex-shrink-0"
        >
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 10.5L12 3m0 0l7.5 7.5M12 3v18" />
          </svg>
        </button>
      </div>
      <p className="text-xs text-muted-foreground px-1">Enter to send · Shift+Enter for new line{agentNames?.length ? ' · @ to mention' : ''}</p>
    </div>
  )
}
