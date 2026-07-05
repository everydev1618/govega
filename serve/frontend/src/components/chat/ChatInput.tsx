import { useState, useRef, useEffect, useCallback } from 'react'
import { AgentAvatar } from './AgentAvatar'
import type { ChatImage } from '../../lib/types'

// fileToChatImage reads an image File into a ChatImage (base64, no data: prefix).
async function fileToChatImage(file: File): Promise<ChatImage | null> {
  if (!file.type.startsWith('image/')) return null
  const dataURL: string = await new Promise((resolve, reject) => {
    const fr = new FileReader()
    fr.onload = () => resolve(String(fr.result))
    fr.onerror = reject
    fr.readAsDataURL(file)
  })
  const comma = dataURL.indexOf(',')
  if (comma < 0) return null
  return { media_type: file.type, data: dataURL.slice(comma + 1) }
}

interface ChatInputProps {
  onSend: (text: string, images?: ChatImage[]) => void
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
      <div className="px-3 py-2 text-xs text-ink-faint">No matching agents</div>
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
              i === selectedIndex ? 'bg-paper-deep text-ink' : 'text-ink-soft hover:bg-paper-deep/60'
            }`}
          >
            <AgentAvatar name={name} displayName={label} avatar={info?.avatar} size={6} />
            <div className="flex flex-col min-w-0">
              <span className="truncate font-medium">{label}</span>
              {info?.title && <span className="truncate text-2xs text-ink-faint">{info.title}</span>}
            </div>
          </button>
        )
      })}
    </div>
  )
}

export function ChatInput({ onSend, sending, placeholder, borderColor, agentNames, agentDisplayInfo }: ChatInputProps) {
  const [input, setInput] = useState('')
  const [images, setImages] = useState<ChatImage[]>([])
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const mentionRef = useRef<HTMLDivElement>(null)

  const addFiles = useCallback(async (files: FileList | File[]) => {
    const next: ChatImage[] = []
    for (const f of Array.from(files)) {
      const img = await fileToChatImage(f)
      if (img) next.push(img)
    }
    if (next.length) setImages(prev => [...prev, ...next].slice(0, 8))
  }, [])

  const submit = useCallback((msg: string) => {
    if (sending) return
    if (!msg && images.length === 0) return
    onSend(msg, images.length ? images : undefined)
    setInput('')
    setImages([])
  }, [sending, images, onSend])

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
      submit(input.trim())
    }
  }

  const handleSendClick = () => submit(input.trim())

  const handlePaste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData.files || [])
    const imgs = files.filter(f => f.type.startsWith('image/'))
    if (imgs.length) {
      e.preventDefault()
      void addFiles(imgs)
    }
  }

  const borderClass = borderColor || 'border-rule focus:border-brand'

  return (
    <div className="pt-3 border-t border-rule space-y-1.5">
      {images.length > 0 && (
        <div className="flex flex-wrap gap-2 px-1">
          {images.map((img, i) => (
            <div key={i} className="relative group">
              <img
                src={`data:${img.media_type};base64,${img.data}`}
                alt={`attachment ${i + 1}`}
                className="h-14 w-14 object-cover rounded-sm border border-rule"
              />
              <button
                onClick={() => setImages(prev => prev.filter((_, j) => j !== i))}
                className="absolute -top-1.5 -right-1.5 h-5 w-5 rounded-full bg-ink text-paper text-xs leading-none flex items-center justify-center hover:bg-brand-deep"
                aria-label="Remove image"
              >×</button>
            </div>
          ))}
        </div>
      )}
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        multiple
        className="hidden"
        onChange={e => { if (e.target.files) { void addFiles(e.target.files); e.target.value = '' } }}
      />
      <div className="flex gap-2 items-end">
        <button
          onClick={() => fileInputRef.current?.click()}
          disabled={sending}
          className="p-2.5 rounded-sm border border-rule text-ink-faint hover:text-ink hover:border-ink disabled:opacity-40 transition-colors flex-shrink-0"
          aria-label="Attach image"
          title="Attach image"
        >
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" />
          </svg>
        </button>
        {/* min-w-0 is load-bearing: Safari gives the <textarea> an intrinsic
            min-width and won't shrink this flex-1 wrapper below it without
            it, pushing the send button off-screen. Chrome shrinks anyway,
            so this bug only shows on real iOS Safari. */}
        <div className="relative flex-1 min-w-0" ref={mentionRef}>
          {mentionOpen && agentNames && (
            <div className="absolute bottom-full mb-1.5 left-0 w-64 rounded-sm border border-rule bg-paper z-20 overflow-hidden" style={{ boxShadow: '0 4px 16px -8px oklch(22% 0.018 40 / 0.12)' }}>
              <div className="px-3 py-2 border-b border-rule">
                <p className="anno">Mention an agent</p>
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
            onPaste={handlePaste}
            placeholder={placeholder || 'Type a message…'}
            disabled={sending}
            // Exactly 16px on mobile to defeat iOS Safari's zoom-on-focus
            // (this theme's text-base is 15px — still under the 16px
            // threshold — so an arbitrary value is required); sm:text-sm
            // restores the denser 13px desktop size.
            className={`w-full px-3.5 py-2.5 rounded-sm bg-paper border text-[1rem] sm:text-sm text-ink placeholder:text-ink-faint focus:outline-none focus:ring-1 focus:ring-brand disabled:opacity-50 resize-none overflow-y-auto transition-colors ${borderClass}`}
            style={{ maxHeight: '144px' }}
          />
        </div>
        <button
          onClick={handleSendClick}
          disabled={sending || (!input.trim() && images.length === 0)}
          className="p-2.5 rounded-sm bg-ink text-paper hover:bg-brand-deep disabled:opacity-40 disabled:hover:bg-ink transition-colors flex-shrink-0"
          aria-label="Send"
        >
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.75}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 10.5L12 3m0 0l7.5 7.5M12 3v18" />
          </svg>
        </button>
      </div>
      <p className="text-xs text-ink-faint px-1">Enter to send · Shift+Enter for new line{agentNames?.length ? ' · @ to mention' : ''}</p>
    </div>
  )
}
