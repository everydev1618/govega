// Reference snippets showing how Cody (or any TS consumer) uses the
// generated types. This file isn't shipped at runtime — it exists for
// type-checking against the same compilation that publishes api.ts.

import type { components, paths } from './api'
import type { VegaEvent } from './events'

// --- 1. Type aliases for common response shapes ----------------------------

type IdentityResponse = components['schemas']['IdentityResponse']
type ConfigResponse = components['schemas']['ConfigResponse']
type AgentResponse = components['schemas']['AgentResponse']
type GmailStatus = components['schemas']['GmailStatus']

// --- 2. Path-keyed access for full request/response inference --------------

// Pick the success response of a single endpoint without aliasing.
type GetIdentity200 =
  paths['/api/v1/identity']['get']['responses']['200']['content']['application/json']

// Pick the request body of an endpoint that takes one.
type GmailStartRequest =
  paths['/api/v1/integrations/gmail/start']['post']['requestBody']['content']['application/json']

// --- 3. A typed fetch wrapper Cody might write -----------------------------

// Minimal example. In a real app, use openapi-fetch (companion library) for
// fully type-inferred client calls.
async function getIdentity(baseURL: string, accessToken: string): Promise<IdentityResponse> {
  const res = await fetch(`${baseURL}/api/v1/identity`, {
    headers: { Authorization: `Bearer ${accessToken}` },
  })
  if (!res.ok) throw new Error(`identity: ${res.status}`)
  return (await res.json()) as IdentityResponse
}

// --- 4. SSE event handling --------------------------------------------------

function dispatchEvent(ev: VegaEvent) {
  switch (ev.type) {
    case 'process.started':
      console.log('process started for', ev.agent, 'pid', ev.process_id)
      return
    case 'process.completed':
    case 'process.failed':
      console.log('process', ev.type, 'pid', ev.process_id)
      return
    case 'workflow.completed':
    case 'workflow.failed':
      console.log('workflow', ev.type)
      return
    case 'agent.created':
    case 'agent.deleted':
      console.log('agent registry changed; refresh list')
      return
    case 'chat.update':
      console.log('refresh chat for', ev.agent)
      return
    case 'chat.event':
      // Live token deltas / tool starts. Render in-progress preview from ev.data.
      return
    case 'channel.message':
    case 'channel.thread_reply':
      console.log('channel updated:', ev.type)
      return
  }
  // Exhaustiveness check — TS errors if a new variant is added without
  // a matching case above. Removing this `never` assignment is a sign the
  // switch is no longer total.
  const _exhaustive: never = ev
  void _exhaustive
}

// Re-export so ts-check above doesn't flag unused locals.
export { getIdentity, dispatchEvent }
export type {
  IdentityResponse,
  ConfigResponse,
  AgentResponse,
  GmailStatus,
  GetIdentity200,
  GmailStartRequest,
}
