# Changelog

Notable changes to govega. Newest first. Versions ship to v39a tenants via the
pipeline in `docs/` — each govega `vX.Y.Z` is paired with a v39avega release and
an `apps.version` migration (see the v39avega-image-pipeline notes).

## v0.8.11 — comms visibility, Layer 1 (2026-07-05)

Surfacing the multi-agent system through linear chat bridges (Discord, Telegram).

- **Visibility commands** in the shared bot core: `agents` / `channels` /
  `status` / `help` (with or without a leading slash, so a plain `agents` works
  on any surface). They answer instantly from roster/channel state and don't
  pollute chat history. Both Discord and Telegram get them for free.
- **Streamed dispatch progress**: bridges now stream the turn instead of
  relaying only the final text — when the orchestrator hands work to a
  sub-agent (or hosts an app) the user sees a short interim line
  (`→ handing this to sage…`). Routine tools stay silent; honest by
  construction (reports the dispatch that fired, never claims live monitoring).

Deferred (Layer 2): mirroring Vega channels → Discord/Slack channels + rendering
sub-agents as distinct webhook senders; Slack/WhatsApp bridges.

## v0.8.10 — agents share exact signed deliverable URLs (2026-07-05)

- The "Delivering work product" prompt taught agents to hand-build
  `{baseURL}/workspace/...` URLs, which drop the capability token and 401 on
  gated instances (Tony shared a dead link on the et tenant). Both prompt
  variants now instruct agents to report the exact `Accessible at:` URL that
  `write_file`/`deploy_app` returns and never reconstruct one. Worker guidance
  points browser apps at `deploy_app` instead of an unreachable localhost
  `start_service`.

## v0.8.9 — vendor-neutral core (2026-07-04)

Provider-neutral app hosting, Phase 3 (issue #116).

- **Removed all Fly vendor code from core.** Deleted `tools/sandbox.go` (the Fly
  Machines client + `spawn_app`/`run_in_app`/`write_file_to_app`/`destroy_app`
  tools). govega core no longer imports or references Fly.
- The Fly backend now lives in the host layer: **v39a-vega's `apphost/fly.go`**
  implements `tools.AppHost` and is wired via `serve.RegisterAppHost` when
  `FLY_SANDBOX_TOKEN` is set. Absent that, `deploy_app` uses the local default.
- `deploy_app` fully replaces `spawn_app` (removed). Workers get it via
  `DefaultNonMetaToolNames`; meta-agents are stripped of it.

## v0.8.8 — provider-neutral app hosting + capability-token deliverables (2026-07-04)

Issue #116, Phases 1–2.

- **`AppHost` interface + `deploy_app` tool** (`tools/apphost.go`): the agent
  says "host this dir, give me a URL" and never learns the backend — no vendor
  lock at the tool layer. `Visibility` defaults to `portal_gated`.
- **`LocalAppHost`** (`serve/apphost_local.go`): the vendor-neutral default —
  static apps served from the workspace, dynamic apps run as a subprocess, both
  reverse-proxied under `/apps/<name>/` on the Vega server. Works local /
  self-hosted-remote / (via edge) v39a with zero external vendor.
- **Capability-token gating** (`serve/capability.go`): `/workspace/` and
  `/apps/` are gated by path-scoped HMAC tokens keyed by
  `VEGA_WORKSPACE_SIGNING_KEY`. Unset ⇒ open mode (self-hosted/local). A valid
  `?sig=` sets a scoped cookie so a deliverable's relative assets stay
  authorized; portal sessions bypass. `write_file` and `deploy_app` hand back
  signed URLs.
- Static HTML apps get `<base href>` injected (subpath asset resolution); agents
  are steered to relative asset paths. Design: `docs/app-hosting-design.md`.

## v0.8.7 — runaway-loop fixes (2026-07-04)

Response to the TonyVega meltdown (orchestrator flooded a turn with 70+ `exec`
calls building/hosting instead of delegating).

- **Meta-agents lose shell/build tools.** Any `IsMeta` agent (orchestrator,
  builder) is stripped of `exec`/`start_service`/`stop_service`/`list_services`/
  `service_logs` + the deploy/sandbox tools. Routers dispatch; they keep
  `read_file` + `fetch__fetch` for read-only verification. Capped at 20 tool
  iterations/turn (workers stay at 100).
- **Tool-loop circuit breaker** (`process_llm.go`): 4 identical `(tool, args,
  result)` signatures in a turn trips it — the turn ends with an honest message
  naming the stuck tool instead of grinding to the iteration cap. Erlang's
  max-restart-intensity applied to the tool loop.
- **UI**: consecutive identical tool-call pills collapse into one counted chip
  (`exec ×72`), amber when looping.
- **exec path-corruption fix**: removed `rewriteCommandPaths`, a blunt regex that
  rewrote every `/`-token in an exec command string — corrupting files written
  via heredoc (`</canvas>` → `</data/workspace/canvas>`). It was never a real
  sandbox boundary. See the exec-not-a-sandbox-boundary note.

## v0.8.6 — meta-agents route build work (2026-07-04)

- `IsMeta` agents excluded from the sandbox always-bucket and given a delegation
  variant of the "Delivering work product" prompt: dispatch build work to
  specialists, relay the deliverable URL — don't build it themselves.

## v0.8.5 — reachable deliverable links + sandbox cleanup (2026-07-04)

- `serve` falls back to the `PUBLIC_URL` env var when `Config.PublicURL` is empty,
  so embedders on hosted instances stop reporting `localhost` deliverable links.
- `spawn_app` tears down the Fly app when a spawn fails partway (no more leaked
  machineless apps). (Superseded by the v0.8.9 refactor.)

## Deferred / in flight (issue #116)

- **Edge subdomain routing** `{app}.{slug}.v39a.com` for Fly-hosted apps + retire
  raw `*.fly.dev` hand-offs — needs wildcard TLS + control-plane routing.
  Until then `FlyAppHost` returns the app's `*.fly.dev` URL (unauthenticated).
- **App Contract data access** for Fly-hosted apps (inject tenant API base +
  `X-V39A-*` identity into the spawned app).
- **Per-app `Visibility` branching** (`Public`/`Private` vs the current
  `PortalGated` default).
- **Phase 4**: formally deprecate/alias `spawn_app`, update docs + website.
