# App Hosting Design — provider-neutral deployment for Vega-built apps

Status: **Phases 1–2 + Phase 3 core shipped** (govega v0.8.9 / v39avega v0.2.10);
Phase 3 edge-routing + App Contract and Phase 4 deferred (see §9 and CHANGELOG /
issue #116). Draft 2026-07-04 · Supersedes standalone "capability-token
workspace links" work (they merged — see §7).

Shipped: `tools.AppHost` interface + `deploy_app` (tools/apphost.go),
`serve.LocalAppHost` default (serve/apphost_local.go), capability-token gating
(serve/capability.go), v39a's `FlyAppHost` (v39avega apphost/fly.go). Deferred:
edge subdomain routing `{app}.{slug}.v39a.com`, App Contract data access,
per-app Public/Private visibility, spawn_app deprecation alias.

## 1. Problem

When an agent builds something the user opens in a browser, Vega has three
mechanisms today and none covers the common case:

| Mechanism | Runs where | Public URL | Tenant data? | Vendor |
|---|---|---|---|---|
| `/workspace/` static | tenant process serves the file | `{PUBLIC_URL}/workspace/…` | yes (they *are* the tenant's files) | none |
| `start_service` | subprocess on the tenant machine | **none** — binds localhost, not routed | yes | none |
| `spawn_app` | isolated Fly machine | `https://<app>.fly.dev` | **no** — isolated, files pushed in | **Fly (hardcoded)** |

Gaps:

1. **No "dynamic app that also needs the tenant's data."** Static can't run a
   backend; `start_service` runs one but nothing routes to it; `spawn_app` runs
   one but is isolated from data. This is what made the orchestrator flail:
   `start_service` a server (unreachable) → try to tunnel it (no mechanism).
2. **`spawn_app` is vendor-locked to Fly** (`api.machines.dev`, `*.fly.dev`,
   Fly GraphQL IP allocation) and lives in core `tools/sandbox.go`. A
   self-hoster who isn't on Fly gets nothing.
3. **`spawn_app` URLs bypass auth** — a raw `*.fly.dev` app is public to anyone
   with the link, the same over-exposure problem as unauthenticated `/workspace/`.

**Tunneling is a non-answer for hosted Vega.** A tunnel (Cloudflare Tunnel,
ngrok) exposes something *not* publicly reachable. Fly apps and v39a tenants
are already public. The only place a tunnel earns its keep is a **self-hosted
laptop** — and even there the cleaner fix is "serve the app through the Vega
process the user already reaches," not a separate tunnel daemon. So this design
is about **routing + a pluggable host abstraction**, not connectivity.

## 2. The layering questions, answered directly

- **Is it a Vega tool?** Yes — the agent-facing surface is one provider-agnostic
  tool (`deploy_app`). The agent says "host this; give me a URL"; it must **not**
  know whether the backend is Fly, a local subprocess, or Cloudflare.
- **Does it already exist?** Partially — `spawn_app` is the Fly-specific ancestor.
  This design generalizes it behind an interface and adds a vendor-neutral default.
- **Core or v39a-vega?** Split by vendor-neutrality:
  - **Native to core (govega):** the `AppHost` interface, the agent tool, and the
    **`LocalAppHost` default** (subprocess + reverse-proxy route on the Vega
    server itself). Core must be able to host apps standalone, with zero external
    vendor.
  - **Contributed by the host layer:** vendor providers (Fly, Cloudflare, Docker)
    register via a hook — the exact pattern `vega-tools` already uses
    (`vapi.Register(srv)` → `srv.RegisterRoute(...)`). v39a-vega wires the Fly
    provider + edge routing; it does not define the abstraction.
- **Vendor lock?** Eliminated by making the neutral `LocalAppHost` the default and
  every vendor an optional, swappable adapter behind `AppHost`. See §4/§5.

## 3. Design: the `AppHost` provider interface (core)

```go
// AppHost hosts an agent-built app and returns a reachable URL. Implementations
// range from an in-process subprocess+proxy (LocalAppHost, the default) to an
// isolated cloud machine (FlyAppHost). The agent never sees which.
type AppHost interface {
    // Deploy makes app source (a workspace subdir or a built artifact) reachable.
    // Returns a URL and an opaque handle for later teardown.
    Deploy(ctx context.Context, spec AppSpec) (AppDeployment, error)
    Destroy(ctx context.Context, id string) error
    List(ctx context.Context) ([]AppDeployment, error)
}

type AppSpec struct {
    Name       string            // friendly name, e.g. "pacman"
    Source     string            // workspace-relative dir the app lives in
    Command    []string          // "" ⇒ static file serving; else run this
    Port       int               // internal port for dynamic apps
    Visibility Visibility         // Public | PortalGated (default) | Private
    DataAccess DataAccess         // None | TenantAPI (App Contract)
}

type AppDeployment struct {
    ID  string
    URL string
    // ...status, provider, createdAt
}
```

The provider is selected by the operator (config/env), registered on the server:

```go
serve.RegisterAppHost(srv, fly.NewAppHost(...))   // v39a wires this
// self-hosted with nothing wired ⇒ core's LocalAppHost is the default
```

`deploy_app` is a normal `tools.ToolDef` in core that calls the wired `AppHost`.
Meta-agents stay denied it (delegation tenet); workers build and deploy.

## 4. How each deployment mode works (the vendor-neutral story)

The universal baseline is **`LocalAppHost`**: run the app as a subprocess on the
Vega machine (reusing `start_service` mechanics) and reverse-proxy it on the Vega
server via `RegisterRoute("/apps/<name>/", …)`. **If the Vega server is reachable,
so is the app it hosts** — no vendor, no tunnel.

| Mode | Provider | App URL | Reachable because |
|---|---|---|---|
| **Local dev** (`vega serve` on a laptop) | LocalAppHost | `http://localhost:PORT/apps/<name>/` | you're on the same box; localhost is fine |
| **Self-hosted remote** (Vega on a VPS + domain) | LocalAppHost | `https://their-domain/apps/<name>/` | the Vega server is already public; the app rides its router |
| **v39a** | LocalAppHost *or* FlyAppHost | `https://<app>.<slug>.v39a.com` via the edge | edge routes to the tenant (local) or the sandbox machine (Fly) |

So the answer to "what about remote / v39a?" is: **the same LocalAppHost works
for local and self-hosted-remote with no vendor.** v39a may *additionally* choose
`FlyAppHost` when it wants stronger isolation or heavier runtimes — an upgrade,
not a requirement. Fly stops being the only way apps exist.

## 5. Data access (answers the "sidecar?" question)

**No shared-volume sidecar** — Fly volumes are single-attach (one machine mounts
a volume), so you cannot co-locate an app machine that reads the tenant's `/data`
directly; and it would collapse the isolation boundary anyway. Data access is a
property of the provider:

- **LocalAppHost** → app runs on the Vega machine, so it reaches tenant data/API
  over localhost trivially. `DataAccess: TenantAPI` just hands it the base URL +
  identity.
- **FlyAppHost** (isolated) → app calls back to the tenant API over HTTPS with
  the injected `X-V39A-*` identity (the **App Contract** already used by
  vega-tools). Isolation preserved, no shared disk.
- **Shared managed store** (Postgres/S3/LiteFS) when an app genuinely needs its
  own datastore.

## 6. Routing & auth

Every hosted app gets a URL whose auth is decided by `Visibility`, reusing **one**
edge-auth layer (not per-provider):

- `Public` — anyone with the link (today's `*.fly.dev` behavior, but now opt-in).
- `PortalGated` (default) — portal cookie **or** a capability token (`?sig=<hmac>`
  scoped to the app), verified at the edge / Vega server. Works over
  Discord/Telegram with no cookie.
- `Private` — portal session required.

## 7. Convergence with capability-token workspace links

The signed-URL / portal-gate layer this needs is the *same* primitive as the
approved "capability-token workspace links" work. Build it once as an **edge
auth + routing layer** with two consumers: `/workspace/` deliverables and
`/apps/<name>/` hosted apps. They are one project, not two.

## 8. Migration

1. Introduce `AppHost` + `LocalAppHost` + `deploy_app` tool in core; keep
   `spawn_app` working.
2. Refactor the Fly logic in `tools/sandbox.go` into a `FlyAppHost` provider
   registered by the host layer (v39a-vega), the way `vapi.Register` works.
   Core stops importing Fly.
3. Add the edge auth+routing layer (`Visibility`, capability tokens); route apps
   at `{app}.{slug}.v39a.com`; retire raw `*.fly.dev` hand-offs.
4. Deprecate `spawn_app` in favor of `deploy_app`; keep an alias for a release.

## 9. Open decisions

- **Where does `FlyAppHost` physically live** — an optional core subpackage
  (`apphost/fly`, config-gated like today's `FLY_SANDBOX_TOKEN`) vs. contributed
  entirely by v39a-vega? Recommendation: **v39a-vega**, so govega core carries no
  vendor code and stays provably unlocked. Trade-off: a Fly self-hoster not using
  v39a would need to import the provider.
- **Subdomain vs path routing** (`{app}.{slug}.v39a.com` vs
  `{slug}.v39a.com/apps/<name>/`) — subdomain isolates cookies/origin better;
  path is simpler and needs no wildcard cert wiring. Recommendation: path for
  LocalAppHost, subdomain for isolated FlyAppHost.
- **Self-hosted-local exposure** — offer an *optional* Cloudflare Tunnel provider
  for the laptop case, or leave local as localhost-only? Recommendation: leave it
  out of the hosted path; add a tunnel provider later only if demand appears.
```
