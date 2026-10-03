# Changelog

Notable changes to govega. Newest first. Versions ship to v39a tenants via the
pipeline in `docs/` — each govega `vX.Y.Z` is paired with a v39avega release and
an `apps.version` migration (see the v39avega-image-pipeline notes).

## v0.9.4 — Generate streams; the repo gets a CI (2026-10-03)

An audit of what the public repo actually offers the world. Source and
`go install` were fine; everything downstream of a tag had been broken since
the repo was recreated on 2026-09-08 without its Actions secrets, so v0.9.2 and
v0.9.3 produced no binaries at all. Brew users were still on v0.9.1 — missing
the prompt-cache fix that shipped in v0.9.3.

- **`Generate` fetches over the streaming endpoint.** It built its request with
  `stream=false`, so a whole generation was bounded by the sync client's
  timeout, which spans the entire response body: a long answer is killed rather
  than waited for. The new `reassembleSSE` rebuilds an `anthropicResponse` from
  the event stream — text, thinking with its signature (so turn replay keeps
  working), `tool_use` with accumulated `input_json` deltas, usage, cache tokens
  and stop reason — preserving block order by stream index, then feeds the same
  `parseResponse` the sync path used. Cost accounting and typed blocks are
  unchanged. A stream ending before `message_stop` is now an error rather than a
  partial answer returned as if complete. This was specified by two tests that
  arrived already-failing in v0.8.9 and had never passed.
- **The API key no longer lands in the log on a failed request.** Both non-200
  paths logged the whole outbound header map, `X-Api-Key` included, so any 401,
  429 or transient 500 wrote the caller's key out in full — a customer
  credential in a shared log on a multi-tenant host. Values for `X-Api-Key`,
  `Authorization`, `Proxy-Authorization`, `Cookie` and `X-Auth-Token` are now
  masked; the header names survive so a missing key is still diagnosable.
- **Supervision no longer copies a lock.** `WithSupervision(s Supervision)` took
  its config by value while the struct held a `sync.Mutex`, which `go vet`
  flagged at five sites. The mutable half (mutex, failures, restarts, backoff)
  moved behind an unexported pointer created on first use, so a zero-value
  `Supervision` still works and the public API is unchanged.
- **CI exists.** `go build`, `go test ./...`, `gofmt -l .` and `go vet ./...` now
  run on every push and pull request. Nothing had guarded `main`, which is why
  two red tests sat unnoticed for two and a half months.
- **`go install` builds report their version.** Only GoReleaser stamped
  `-X main.version`, so `vega version` from a source install said `dev`. It now
  falls back to the module version from `debug.ReadBuildInfo()`.
- **Files can be dropped into the UI.** Nothing in the dashboard accepted a
  drag: the chat composer took images only (base64, inline in the turn) and the
  Files page was read-only, so there was no way to hand an agent a document at
  all. `POST /api/v1/files/upload` writes into the workspace — filename reduced
  to its base name, `dir` refused rather than clamped when it escapes, existing
  files never overwritten (`report.md` → `report-1.md`), 10 MB cap matching what
  `/files/read` can read back — and records the write in `workspace_files` as
  the user's. The composers in `#channels`, DMs and threads, plus the Files
  page, now accept drops, paste and a file picker; images keep the inline vision
  path, everything else uploads and the message carries the workspace path with
  an instruction to open it with `read_file`.
- Repo housekeeping: the README release badge pointed at `govega/releases`,
  which is empty — releases live in `vega-releases` — so it publicly read "no
  releases or repo not found". Added `CONTRIBUTING.md` and `SECURITY.md`,
  enabled private vulnerability reporting, secret scanning and push protection,
  set the repo topics and homepage, and formatted the 24 files that predated the
  gofmt gate.

## v0.9.3 — prompt caching actually caches (2026-09-20)

Two bugs meant agents wrote a cache entry on every request and read almost
none of them. The `cache_control` breakpoints were all in place; the content
behind them just never repeated byte-for-byte.

- **`tools.Schema()` no longer shuffles.** It iterated `tools map[string]*tool`
  directly, and Go randomizes map iteration — so the tools array came out in a
  different order on every LLM turn. Tools render first in an Anthropic cache
  prefix, so a reshuffle invalidated system and messages behind it and no
  request ever hit cache. `Tools` now tracks registration order and reports it;
  `Filter`/`FilterMCP` views inherit the parent's order.
- **Per-process system content moved out of the cached block.**
  `SetExtraSystem` content (user identity, memory, project context) was
  concatenated onto the shared persona, making the cached system block
  byte-unique per process. It now travels in the new `llm.Message.Volatile`
  field, which the Anthropic backend renders as a second, **uncached** system
  block behind the cached prefix — so every process shares one cached
  tools+system prefix. The OpenAI backend appends it (no breakpoints to place).
  Breakpoint count is unchanged at three: static system, last tool, trailing
  message.
- `llm.Message.SystemText()` returns both halves for callers to whom the split
  is an implementation detail (assertions, logging).

Ordering note for existing `SetExtraSystem` users: the rendered prompt is now
persona → date → extra, where it was persona → extra → date. Same content.

## v0.8.13 — voice input + richer URL reading (2026-07-05)

- **Discord voice notes**: audio attachments are downloaded and transcribed
  (shared Whisper transcriber) like Telegram already did. Also fixed a latent
  bug where attachment-only messages (image or voice, no text) were dropped
  before reaching the handler.
- **Web composer mic**: `POST /api/v1/transcribe` + a record button
  (MediaRecorder) that drops the transcript into the message box to edit before
  sending. (Voice on any surface needs a transcription key — `OPENAI_API_KEY`;
  Claude can't transcribe.)
- **fetch reads more than HTML**: image URLs return an honest note (upload for
  vision instead of dumping bytes); PDF URLs are text-extracted (rsc.io/pdf,
  bounded, panic-safe). HTML/text unchanged.

## v0.8.12 — image input + browser-UA fetch (2026-07-05)

- **Vision/image input** end to end: `llm.BlockImage` (base64 + media type) →
  Anthropic vision API; `StreamToAgentWithImages` carries a multimodal user
  turn (history keeps a `[📎 image]` placeholder — bytes passed to the model,
  not persisted). Wired into **Discord** (attachments), **Telegram** (photos +
  image documents), and the **dashboard composer** (attach button + paste +
  thumbnails). Bounded by size/count; unsupported types dropped.
- **Fetch tool** now sends a real desktop-Chrome `User-Agent` + `Accept-Language`
  (the old `Vega/1.0` UA got 403'd by anti-bot layers), 20s timeout, and turns
  401/403/429 into an honest "blocked automated access — try a different
  source" message.
- Note: frontend changes require rebuilding + committing `serve/frontend/dist`
  (embedded via `//go:embed`; the v39avega image uses the committed dist). This
  release also lands the v0.8.11 collapsed-tool-pill UI whose dist rebuild was
  missed.

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
