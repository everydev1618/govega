import { useState, useEffect, useRef } from 'react'
import type { BrokerEvent } from '../lib/types'

export function useSSE(url: string = '/api/events', maxEvents: number = 200) {
  const [events, setEvents] = useState<BrokerEvent[]>([])
  const [connected, setConnected] = useState(false)
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    const es = new EventSource(url)
    esRef.current = es

    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)

    const handler = (e: MessageEvent) => {
      try {
        const event: BrokerEvent = JSON.parse(e.data)
        setEvents(prev => [event, ...prev].slice(0, maxEvents))
      } catch {
        // Ignore parse errors
      }
    }

    // Listen for all event types we care about
    for (const type of ['process.started', 'process.completed', 'process.failed', 'workflow.completed', 'workflow.failed', 'agent.created', 'agent.deleted']) {
      es.addEventListener(type, handler)
    }

    return () => {
      es.close()
      esRef.current = null
    }
  }, [url, maxEvents])

  return { events, connected }
}
