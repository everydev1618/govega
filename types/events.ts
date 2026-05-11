// Convenience layer over api.ts for the SSE event stream at /api/v1/events.
//
// The OpenAPI spec models BrokerEvent as a generic shape (type, process_id,
// agent, data, timestamp) since YAML can't express a tagged union with
// per-type payload shapes well. This file enumerates the event types the
// Vega backend emits and gives each a discriminant-narrowable interface.
//
// Source of truth: govega/serve emits these via Server.broker.Publish.
// If a new event type is added there, add it here too.

import type { components } from './api'

/** All event type discriminators emitted by Vega. */
export type EventType =
  | 'process.started'
  | 'process.completed'
  | 'process.failed'
  | 'workflow.completed'
  | 'workflow.failed'
  | 'agent.created'
  | 'agent.deleted'
  | 'chat.update'
  | 'chat.event'
  | 'channel.message'
  | 'channel.thread_reply'

/**
 * Generic broker event as defined in OpenAPI. Use the type-narrowed
 * interfaces below in handler code instead.
 */
export type BrokerEvent = components['schemas']['BrokerEvent']

/** Lifecycle: a process for {agent} started. */
export interface ProcessStartedEvent extends BrokerEvent {
  type: 'process.started'
  process_id: string
  agent: string
}

/** Lifecycle: a process completed successfully. data may carry the result. */
export interface ProcessCompletedEvent extends BrokerEvent {
  type: 'process.completed'
  process_id: string
  agent: string
}

/** Lifecycle: a process failed. data may carry the error. */
export interface ProcessFailedEvent extends BrokerEvent {
  type: 'process.failed'
  process_id: string
  agent: string
}

/** A workflow run finished successfully. */
export interface WorkflowCompletedEvent extends BrokerEvent {
  type: 'workflow.completed'
}

/** A workflow run failed. */
export interface WorkflowFailedEvent extends BrokerEvent {
  type: 'workflow.failed'
}

/** A new agent was created via the API. Refresh agent lists. */
export interface AgentCreatedEvent extends BrokerEvent {
  type: 'agent.created'
}

/** An agent was deleted. Drop from any cached lists. */
export interface AgentDeletedEvent extends BrokerEvent {
  type: 'agent.deleted'
}

/**
 * A direct-message conversation has new persisted messages. Re-fetch
 * /api/v1/agents/{name}/chat to render the canonical state. Fires once
 * per turn, not per token.
 */
export interface ChatUpdateEvent extends BrokerEvent {
  type: 'chat.update'
  agent: string
}

/**
 * Per-token / per-tool-call event within a streaming chat response.
 * Mirrors the ChatEventType enum in govega/stream_event.go.
 */
export type ChatEventType =
  | 'text_delta'
  | 'tool_start'
  | 'tool_end'
  | 'error'
  | 'done'
  /**
   * Emitted on the reconnect endpoint when the agent has no in-progress
   * stream to resume. Always followed by `done`. Replaces the prior
   * "JSON status body" behavior in 0.2.0 — reconnect is now SSE-always
   * and callers don't need a content-type heuristic.
   */
  | 'no_active_stream'

/**
 * Stable error classifier on `ChatStreamEvent.code` when type === 'error'.
 * Switch on this instead of substring-matching the prose `error` field.
 * Mirrors ErrorClass in govega/agent.go.
 */
export type ChatEventCode =
  | 'rate_limit'
  | 'overloaded'
  | 'timeout'
  | 'temporary'
  | 'invalid_request'
  | 'authentication'
  | 'budget_exceeded'

/**
 * Token-level metrics for a completed response (only present on the
 * final 'done' event of a streaming run).
 */
export interface ChatEventMetrics {
  input_tokens: number
  output_tokens: number
  cost_usd: number
  duration_ms: number
}

/**
 * The structured payload that rides in `data` for chat.event broker
 * events (and also serializes directly when reading the per-agent
 * chat-stream SSE endpoint).
 */
export interface ChatStreamEvent {
  type: ChatEventType
  delta?: string
  tool_call_id?: string
  tool_name?: string
  arguments?: Record<string, unknown>
  result?: string
  duration_ms?: number
  error?: string
  /** Only set when type === 'error'. */
  code?: ChatEventCode
  nested_agent?: string
  metrics?: ChatEventMetrics
}

/**
 * Live progress within a streaming agent run (tool call started/ended,
 * text delta, etc). UIs typically render these as an in-progress preview
 * that's replaced by the final message when the matching chat.update
 * arrives. `data` is narrowed to ChatStreamEvent here (the BrokerEvent
 * parent types it as a generic record).
 */
export type ChatEventEvent = Omit<BrokerEvent, 'type' | 'agent' | 'data'> & {
  type: 'chat.event'
  agent: string
  data?: ChatStreamEvent
}

/** A channel received a new top-level message. */
export interface ChannelMessageEvent extends BrokerEvent {
  type: 'channel.message'
}

/** A channel received a new threaded reply. */
export interface ChannelThreadReplyEvent extends BrokerEvent {
  type: 'channel.thread_reply'
}

/** Discriminated union of every broker event Vega emits. */
export type VegaEvent =
  | ProcessStartedEvent
  | ProcessCompletedEvent
  | ProcessFailedEvent
  | WorkflowCompletedEvent
  | WorkflowFailedEvent
  | AgentCreatedEvent
  | AgentDeletedEvent
  | ChatUpdateEvent
  | ChatEventEvent
  | ChannelMessageEvent
  | ChannelThreadReplyEvent
