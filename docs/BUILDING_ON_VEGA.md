# Building on Vega

This is the canonical entry point for anyone — human or agent — building a frontend, mobile app, or other client against the Vega API. From `git clone` to "I'm rendering data from a real API call with real auth" in about 10 minutes.

> If you're maintaining govega itself, see `ARCHITECTURE.md` and `BUILDING_APPS.md` instead. This doc is for *consumers* of the platform.

---

## 1. The layering

```
Vega   = the platform.
         govega module + control plane (auth.apex.io) + multi-tenant infra.
         Provides: agents, runtime, identity (JWT/JWKS), Gmail/Slack OAuth,
         persistence, SSE event stream.

Apex   = a product built on Vega.
         Has its own backend binary (apexvega) on top of govega's serve
         package, plus its own UI. Apex is real, brand-bearing, customer-
         facing.

Cody   = building Apex's real frontend.
         Talks to the Vega API surface. Doesn't care how auth is wired
         underneath — just consumes JWTs and bearer tokens.
```

The Vega API contract is the same whether you're building Apex's frontend, a partner integration, a mobile app, or a CLI. This doc focuses on building a web frontend.

## 2. Local dev stack — three processes

Three terminals, one per process.

### Terminal 1 — control plane (mints tokens)

```sh
cd govega
APEX_DEV_SECRET=shh go run ./cmd/control-plane
```

You'll see:
```
JWKS:    http://localhost:9001/jwks
Healthz: http://localhost:9001/healthz
DevMint: http://localhost:9001/dev/mint  (X-Dev-Secret: ***)
```

Leave it running. In production this will be backed by WorkOS; in dev it just signs whatever tokens you ask for.

### Terminal 2 — Apex tenant backend (the thing your frontend talks to)

```sh
cd apexvega
export APEX_TENANT_ID="acme"
export APEX_JWT_ISSUER="http://localhost:9001"
export APEX_JWKS_URL="http://localhost:9001/jwks"
export APEX_ALLOWED_ORIGINS="http://localhost:5173"
make run PORT=8081
```

Now `http://localhost:8081/api/v1/*` is auth-enforced and pointed at the dev control plane.

(For a no-auth single-binary experience — useful for very early dev — drop `APEX_TENANT_ID` and the others. The auth middleware becomes a no-op and you get the embedded SPA at `http://localhost:8080`. This is the self-hosted mode.)

### Terminal 3 — your frontend dev server

For example, with Vite:
```sh
cd your-frontend
VITE_API_BASE_URL=http://localhost:8081 npm run dev
```

## 3. Get a token

The control plane's `/dev/mint` endpoint is your one-stop shop in dev:

```sh
curl -s -H "X-Dev-Secret: shh" \
     -H "Content-Type: application/json" \
     -d '{"tenant":"acme","user":"alice","ttl_seconds":3600}' \
     http://localhost:9001/dev/mint | jq -r .access_token
```

That returns a JWT signed by the dev control plane and accepted by the Apex tenant backend. Copy it as `TOKEN` for the rest of this doc.

In production (Phase 2C.2), this becomes a WorkOS-backed login flow + `/exchange` endpoint. Your frontend code doesn't change much — you swap the dev `/dev/mint` call for a real OIDC redirect, and store the resulting access token the same way.

## 4. Make your first API call

```ts
const res = await fetch('http://localhost:8081/api/v1/identity', {
  headers: { Authorization: `Bearer ${token}` },
})
const identity = await res.json()
// { orchestrator: {...}, builder: {...}, product_name: "..." }
```

Other endpoints worth poking at first:

| Endpoint | What |
|----------|------|
| `GET /api/v1/identity` | Orchestrator/builder display names |
| `GET /api/v1/agents` | All agents available in this tenant |
| `GET /api/v1/processes` | Running + recent processes |
| `GET /api/v1/channels` | Channels (group conversations) |
| `GET /api/v1/inbox` | Inbox items waiting for review |
| `POST /api/v1/agents/{name}/chat` | Send a DM to an agent (returns the response) |

The full surface is documented in `docs/openapi.yaml` and `docs/API.md`.

## 5. Listen to the SSE event stream

`EventSource` can't send a bearer header, so the access token rides as a query param (RFC 6750 §2.3):

```ts
const url = `http://localhost:8081/api/v1/events?access_token=${encodeURIComponent(token)}`
const es = new EventSource(url)

const eventTypes = [
  'process.started', 'process.completed', 'process.failed',
  'workflow.completed', 'workflow.failed',
  'agent.created', 'agent.deleted',
  'chat.update', 'chat.event',
  'channel.message', 'channel.thread_reply',
] as const

for (const t of eventTypes) {
  es.addEventListener(t, (e) => {
    const event = JSON.parse((e as MessageEvent).data)
    // dispatch to your store / queries
  })
}
```

Per-agent live progress (token deltas, tool calls) is on a separate stream:
```
GET  /api/v1/agents/{name}/chat/stream    (reconnect — replay buffered + live)
POST /api/v1/agents/{name}/chat/stream    (send + read response stream)
```

Channels have an analogous pair:
```
GET  /api/v1/channels/{name}/stream
POST /api/v1/channels/{name}/stream
```

## 6. Type-safe with `@vega/api-types`

Generated from `docs/openapi.yaml`. Until it's published to npm, vendor or path-import:

```ts
import type { components, paths } from '@vega/api-types'
import type { VegaEvent, ChatStreamEvent } from '@vega/api-types/events'

type IdentityResponse = components['schemas']['IdentityResponse']
```

For fully type-inferred client calls, pair with [`openapi-fetch`](https://openapi-ts.dev/openapi-fetch/):

```ts
import createClient from 'openapi-fetch'
import type { paths } from '@vega/api-types'

const client = createClient<paths>({ baseUrl: 'http://localhost:8081' })
const { data, error } = await client.GET('/api/v1/identity', {
  headers: { Authorization: `Bearer ${token}` },
})
// data is fully typed; error too
```

Regenerate types when the API changes:
```sh
cd govega && make types
```

CI guard against drift:
```sh
make types-verify   # exits non-zero if api.ts is stale vs openapi.yaml
```

See `govega/types/README.md` for full details.

## 7. Auth contract (what your frontend needs to know)

| | Value |
|---|---|
| Token format | JWT, RS256, signed by control plane |
| Validation | Tenant backend validates locally via JWKS (cached hourly) |
| Header | `Authorization: Bearer <token>` for fetch |
| SSE | `?access_token=<token>` query param (header alternative not available) |
| Required claims | `iss` (control plane URL), `aud` (this tenant id), `sub` (user id), `exp` |
| Lifetime | 15 minutes default — refresh before expiry (Phase 2C.2 will provide `/exchange`) |
| 401 response | Token missing/expired/invalid → tell user to re-login |
| 403 response | Token valid but for a different tenant — wrong subdomain/instance |

Self-hosted mode (no `APEX_TENANT_ID` on the backend) skips auth entirely; your frontend can detect this by checking whether unauthenticated requests succeed and adapt UX accordingly.

### Authenticating without WorkOS

Some products front Vega with their own session-bearing edge — a Next.js app with its own auth, an internal service that has already proven user identity, etc. — and prefer not to introduce a separate JWT issuer. Those products run a **trusted reverse-proxy** in front of Vega and inject identity directly into the request context:

```go
// In your product's middleware, after you've authenticated the user
// (session cookie, API key, mTLS — whatever you trust):
ctx := serve.WithClaims(r.Context(), serve.AuthClaims{
    UserID:   "user_abc123",     // your product's stable user id
    TenantID: "your-product",     // bookkeeping; informational here
})
next.ServeHTTP(w, r.WithContext(ctx))
```

Downstream handlers — including `handleChat`, `handleChatStream`, and the channel runner — call `serve.ClaimsFrom(ctx)` and use `claims.UserID` for per-user memory namespacing. If no claims are present, they fall back to `"default"` (preserving self-hosted single-user behavior).

This pairs with leaving `APEX_TENANT_ID` unset so the bundled JWT middleware is a no-op and only your proxy's claims flow through. Lock down the deploy so nothing but your proxy can route to Vega (private network, shared-secret header, or origin allowlist) — `WithClaims` is a trust seam, not a validation seam. See `apexvega/docs/phase-2-auth-rfc.md` Decision 9 for the canonical claims-based identity model.

## 8. Workspace files (live preview)

If your frontend renders agent-generated artifacts (a quick HTML site, an SVG, a markdown doc), those are served directly at `/workspace/*` on the tenant backend (no auth — public static files). Agents publish to that workspace via the runtime.

## 9. CORS notes

Tenant backend default-denies cross-origin. Set `APEX_ALLOWED_ORIGINS` on the backend to permit your frontend's origin during dev:
```sh
APEX_ALLOWED_ORIGINS=http://localhost:5173 make run
```

In production this is mechanical: every tenant's frontend lives at `https://app.apex.io` (or wherever Apex is hosted) and the allowlist is constant.

## 10. Where to look next

| For | Read |
|-----|------|
| Endpoint reference (human-readable) | `docs/API.md` |
| Endpoint reference (machine-readable) | `docs/openapi.yaml` |
| Architectural rationale (Phase 2 design) | `apexvega/docs/phase-2-auth-rfc.md` |
| Multi-tenant migration plan | (same RFC, sections 6–8) |
| TS types package | `govega/types/README.md` |
| Govega framework internals (rare) | `docs/ARCHITECTURE.md`, `docs/BUILDING_APPS.md` |

## 11. Common gotchas

- **"My SPA loads but every fetch returns 401."** Check the `Authorization` header is being sent (browser devtools → Network → Request Headers). Common cause: same-origin requests don't preflight, but cross-origin POSTs do, and CORS preflight without `Authorization` in `Access-Control-Allow-Headers` will block. The Apex backend already advertises `Authorization` when an origin is allowlisted.
- **"SSE works locally but breaks behind my reverse proxy."** Disable response buffering and increase read timeout for `/api/v1/events`. Cloudflare needs `cache-control: no-store` (already set by the backend).
- **"401 on `/api/v1/events` despite a valid token."** EventSource doesn't send headers; use `?access_token=` instead.
- **"Wrong tenant — getting 403."** Your token's `aud` claim doesn't match `APEX_TENANT_ID` on the backend. In dev, mint a fresh token with the right tenant; in production, the user is hitting the wrong subdomain.
- **"My types are stale."** `make types` regenerates `types/api.ts` from `docs/openapi.yaml`. Pair with `make types-verify` in CI to catch drift.
