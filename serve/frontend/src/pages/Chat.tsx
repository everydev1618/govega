import { useState, useRef, useEffect } from 'react'
import Markdown from 'react-markdown'
import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'

interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
}

export function Chat() {
  const { data: agents } = useAPI(() => api.getAgents())
  const [selected, setSelected] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const send = async () => {
    if (!selected || !input.trim() || sending) return
    const msg = input.trim()
    setInput('')
    setMessages(prev => [...prev, { role: 'user', content: msg }])
    setSending(true)
    try {
      const res = await api.chat(selected, msg)
      setMessages(prev => [...prev, { role: 'assistant', content: res.response }])
    } catch (err) {
      setMessages(prev => [...prev, { role: 'assistant', content: `Error: ${err}` }])
    } finally {
      setSending(false)
    }
  }

  const switchAgent = async (name: string) => {
    setSelected(name)
    setMessages([])

    // Load persisted chat history from SQLite.
    try {
      const history = await api.chatHistory(name)
      if (history?.length) {
        setMessages(history.map(m => ({
          role: m.role as 'user' | 'assistant',
          content: m.content,
        })))
      }
    } catch {
      // No history yet — that's fine.
    }
  }

  return (
    <div className="flex h-[calc(100vh-3rem)] gap-4">
      {/* Agent picker */}
      <div className="w-56 flex-shrink-0 space-y-1 overflow-auto py-1">
        <h2 className="text-sm font-semibold text-muted-foreground px-2 mb-2">Agents</h2>
        {agents?.map(agent => (
          <button
            key={agent.name}
            onClick={() => switchAgent(agent.name)}
            className={`w-full text-left px-3 py-2 rounded-lg text-sm transition-colors ${
              selected === agent.name
                ? 'bg-accent text-accent-foreground font-medium'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent/50'
            }`}
          >
            <div className="flex items-center gap-2">
              <span>{agent.name}</span>
              {agent.source === 'composed' && (
                <span className="text-xs px-1 py-0.5 rounded bg-purple-900/50 text-purple-400">c</span>
              )}
            </div>
            {agent.model && (
              <p className="text-xs text-muted-foreground font-mono truncate">{agent.model}</p>
            )}
          </button>
        ))}
      </div>

      {/* Chat area */}
      <div className="flex-1 flex flex-col min-w-0">
        {!selected ? (
          <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm">
            Pick an agent to start chatting
          </div>
        ) : (
          <>
            {/* Header */}
            <div className="flex items-center gap-3 pb-3 border-b border-border mb-3">
              <h2 className="text-lg font-semibold">{selected}</h2>
              {agents?.find(a => a.name === selected)?.tools && (
                <span className="text-xs text-muted-foreground">
                  {agents.find(a => a.name === selected)!.tools!.length} tools
                </span>
              )}
            </div>

            {/* Messages */}
            <div className="flex-1 overflow-auto space-y-4 pb-4">
              {messages.length === 0 && (
                <p className="text-sm text-muted-foreground">Send a message to start the conversation.</p>
              )}
              {messages.map((msg, i) => (
                <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  {msg.role === 'user' ? (
                    <div className="max-w-[80%] rounded-lg px-4 py-2.5 text-sm whitespace-pre-wrap bg-primary text-primary-foreground">
                      {msg.content}
                    </div>
                  ) : (
                    <div className="max-w-[80%] rounded-lg px-4 py-2.5 text-sm bg-card border border-border prose prose-invert prose-sm prose-p:my-2 prose-headings:my-3 prose-ul:my-2 prose-ol:my-2 prose-li:my-0.5 prose-pre:bg-background prose-pre:border prose-pre:border-border prose-code:text-purple-400 prose-code:before:content-none prose-code:after:content-none max-w-none">
                      <Markdown>{msg.content}</Markdown>
                    </div>
                  )}
                </div>
              ))}
              {sending && (
                <div className="flex justify-start">
                  <div className="bg-card border border-border rounded-lg px-4 py-2.5 text-sm text-muted-foreground">
                    <span className="animate-pulse">Thinking...</span>
                  </div>
                </div>
              )}
              <div ref={bottomRef} />
            </div>

            {/* Input */}
            <div className="flex gap-2 pt-3 border-t border-border">
              <input
                type="text"
                value={input}
                onChange={e => setInput(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && !e.shiftKey && send()}
                placeholder={`Message ${selected}...`}
                disabled={sending}
                className="flex-1 px-4 py-2.5 rounded-lg bg-background border border-border text-sm focus:outline-none focus:border-primary disabled:opacity-50"
              />
              <button
                onClick={send}
                disabled={sending || !input.trim()}
                className="px-5 py-2.5 rounded-lg bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
              >
                Send
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
