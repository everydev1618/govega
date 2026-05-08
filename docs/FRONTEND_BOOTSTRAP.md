# Frontend Bootstrap

Concrete setup for someone (Cody, you, future-you) starting a new frontend that consumes the Vega API. Companion to `BUILDING_ON_VEGA.md` — that one is about the API; this one is about the project skeleton.

> **Audience:** building Apex's real frontend, or any other product on Vega.
> **Time to running app:** ~30 minutes from `npm create` to "I see real data."

---

## 1. Where the repo lives

**Recommendation: separate sibling repo.**

```
~/Code/vega/
├── govega/              # framework (Go) + types package + control plane
├── apexvega/            # Apex tenant backend (Go) + legacy web/frontend (v0 SPA)
└── apex-frontend/       # ← Cody's repo. Independent deploy.
```

**Why separate:**
- Vega's hosted/cloud architecture (Phase 2 RFC) already assumes the SPA is CDN-hosted at `app.apex.io`, separate from the tenant backend. Separate repo matches deploy reality.
- Frontend deploys are decoupled from backend deploys — push to main, CDN cache invalidates, all tenants see the new SPA without touching any Go binary.
- The legacy `apexvega/web/frontend` v0 stays as reference code; Cody doesn't have to either preserve or delete it.
- If a second product is ever built on Vega (or the API gets a public partner client), you're already set up for that.

**Why not the workspace alternatives:**
- *`apexvega/web/frontend` (replace existing):* couples Apex-frontend lifecycle to Apex-backend. Frontend changes mean rebuilding the Go binary's embed.
- *`apexvega/web/frontend-v2/`:* same coupling, plus two SPAs floating around in the same repo.

The legacy SPA continues to embed in the apex Go binary for **self-hosted single-binary deploys** (where the operator wants `./bin/apex` to serve a UI). Cody's repo doesn't need to support that mode initially.

## 2. Stack

| Layer | Choice | Why |
|-------|--------|-----|
| Build tool | **Vite** | Same as legacy SPA, fast HMR, no SSR complexity until you need it |
| Framework | **React 19** + TypeScript | Same as legacy; broad ecosystem; Cody can carry over patterns |
| Routing | **react-router 7** | Same as legacy SPA |
| Data fetching | **TanStack Query (v5)** | Caching, retries, invalidation, typed queries via openapi-fetch |
| Typed client | **openapi-fetch** + `@vega/api-types` | Path/method/response inference from the OpenAPI spec |
| Styling | **Tailwind 4** | Same as legacy; or any preference — Tailwind isn't load-bearing |
| Auth state | React context (custom) | Simple; the legacy `AuthProvider` pattern works |
| State | TanStack Query for server state, React state for UI | No global store needed initially |

This list is opinionated; deviate freely. The only hard requirement is **TypeScript** so you can use the generated types.

## 3. Bootstrap

```sh
cd ~/Code/vega
npm create vite@latest apex-frontend -- --template react-ts
cd apex-frontend
npm install
npm install @tanstack/react-query openapi-fetch react-router-dom
npm install -D tailwindcss @tailwindcss/vite

# Vendor the Vega types until they're on npm (file: dep keeps them path-linked).
npm install ../govega/types --save
```

Update `tsconfig.app.json` to add a path alias if you want `@/` imports (matches legacy SPA convention):

```jsonc
{
  "compilerOptions": {
    "paths": { "@/*": ["./src/*"] },
    "baseUrl": "."
  }
}
```

Update `vite.config.ts`:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  // BASE URL is read at build time; left empty for self-hosted dev (proxy).
  server: { port: 5173 },
})
```

## 4. Auth context (copy + adapt)

`src/auth.ts`:

```ts
const TOKEN_KEY = 'apex.access_token'

let currentToken: string | null = sessionStorage.getItem(TOKEN_KEY)
const subscribers = new Set<(t: string | null) => void>()

export function getToken(): string | null {
  return currentToken
}

export function setToken(t: string | null) {
  currentToken = t
  if (t) sessionStorage.setItem(TOKEN_KEY, t)
  else sessionStorage.removeItem(TOKEN_KEY)
  subscribers.forEach((cb) => cb(t))
}

export function onTokenChange(cb: (t: string | null) => void): () => void {
  subscribers.add(cb)
  return () => subscribers.delete(cb)
}
```

`src/api.ts`:

```ts
import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from '@vega/api-types'
import { getToken, setToken } from './auth'

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

const authMiddleware: Middleware = {
  onRequest({ request }) {
    const t = getToken()
    if (t) request.headers.set('Authorization', `Bearer ${t}`)
    return request
  },
  onResponse({ response }) {
    if (response.status === 401) setToken(null) // triggers re-login UI
    return response
  },
}

export const api = createClient<paths>({ baseUrl: BASE })
api.use(authMiddleware)
```

Now every API call is fully typed:

```ts
const { data, error } = await api.GET('/api/v1/identity')
// data is IdentityResponse | undefined; error is the typed error response
```

## 5. SSE subscriber

`src/sse.ts`:

```ts
import type { VegaEvent } from '@vega/api-types/events'
import { getToken } from './auth'

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

const ALL_EVENT_TYPES = [
  'process.started', 'process.completed', 'process.failed',
  'workflow.completed', 'workflow.failed',
  'agent.created', 'agent.deleted',
  'chat.update', 'chat.event',
  'channel.message', 'channel.thread_reply',
] as const

export function subscribeEvents(handler: (e: VegaEvent) => void): () => void {
  const t = getToken()
  if (!t) return () => {}
  const url = `${BASE}/api/v1/events?access_token=${encodeURIComponent(t)}`
  const es = new EventSource(url)
  const wrap = (msg: MessageEvent) => {
    try {
      handler(JSON.parse(msg.data) as VegaEvent)
    } catch { /* ignore parse errors */ }
  }
  for (const t of ALL_EVENT_TYPES) es.addEventListener(t, wrap)
  return () => es.close()
}
```

Use it inside a TanStack Query setup to invalidate caches when relevant events fire:

```tsx
import { QueryClient } from '@tanstack/react-query'
import { subscribeEvents } from './sse'

export function wireQueryInvalidation(qc: QueryClient) {
  return subscribeEvents((ev) => {
    switch (ev.type) {
      case 'agent.created':
      case 'agent.deleted':
        qc.invalidateQueries({ queryKey: ['agents'] })
        break
      case 'chat.update':
        qc.invalidateQueries({ queryKey: ['chat', ev.agent] })
        break
      case 'process.started':
      case 'process.completed':
      case 'process.failed':
        qc.invalidateQueries({ queryKey: ['processes'] })
        break
    }
  })
}
```

## 6. Login page (dev mode, ~30 lines)

In dev, mint a token via the control plane's `/dev/mint`. The legacy SPA's `pages/Login.tsx` is a working reference — same form, different storage.

In production this becomes a redirect to WorkOS hosted login (Phase 2C.2 lands `/exchange`). Frontend code: same shape, different endpoint.

## 7. Env vars

`apex-frontend/.env.development`:
```sh
VITE_API_BASE_URL=http://localhost:8081
VITE_CONTROL_PLANE_URL=http://localhost:9001
VITE_DEFAULT_TENANT=acme
VITE_DEV_SECRET=shh
```

`apex-frontend/.env.production` (set during deploy, not committed):
```sh
VITE_API_BASE_URL=https://acme.apex.io     # or whatever subdomain Apex deploys to
# VITE_CONTROL_PLANE_URL/SECRET unset — production uses real OIDC, not /dev/mint
```

## 8. CI / deploy

| Stage | What |
|-------|------|
| PR | `tsc --noEmit` + `eslint` + `vitest` (when you add tests) |
| Build | `npm run build` produces `dist/` |
| Deploy | Push `dist/` to a CDN (Vercel, Cloudflare Pages, S3+CloudFront) |
| Cache invalidation | Vite emits hashed asset names; only `index.html` needs cache busting |
| Type drift | Run `cd ../govega && make types-verify` in CI to catch openapi.yaml changes that haven't been regenerated |

## 9. The first thing to render

Suggested first task to validate the whole stack works end-to-end:

1. Login page that posts to `/dev/mint` and stores the token.
2. After login, fetch `GET /api/v1/identity` and show the orchestrator's display name in a header.
3. Below the header, fetch `GET /api/v1/agents` and render a list with names + models.
4. Subscribe to SSE; when `agent.created` fires, refresh the list automatically.

If all four work, you have working auth, fetch, types, and SSE — every Vega API capability is exercised. From there it's "build the actual product."

## 10. Pitfalls

- **`openapi-fetch` returns `data | error`, not throws.** Don't write `try/catch` around it; check `error` instead.
- **`fetch` and `EventSource` see different origins.** When the Vite dev server proxies `/api/*`, `fetch('/api/v1/x')` is same-origin (proxy) but `new EventSource('http://localhost:8081/...')` is cross-origin. Either both go through the proxy or both go direct.
- **`paths['/api/v1/x']['post']` types include the `requestBody` shape.** Pass it via `body:` to `client.POST`. openapi-fetch validates it at compile time.
- **TanStack Query's `queryKey` is opaque to types.** Use stable string arrays (`['agents']`, `['chat', name]`) and keep keys consistent across queries + invalidations.
- **Self-hosted mode unauth.** When `VITE_API_BASE_URL` is empty, the Vega backend is probably running with no `APEX_TENANT_ID`. Your `Authorization` header is harmless — the middleware ignores it — but your login UI shouldn't *require* a token to proceed in that mode.
