# Vega Peering — Orchestrator-to-Orchestrator Federation over AIRE

> Status: **Design draft** · 2026-05-14 · Author: et + Claude
> Scope: Implementation plan for trusted orchestrator-to-orchestrator agent communication in Vega, built on the AIRE protocol.
> Audience: Vega contributors. Non-normative w.r.t. AIRE.

## 1. Goals and non-goals

### Goals (v1)
1. A Vega orchestrator can **invoke an agent that lives on another Vega orchestrator** I trust, and stream the response back.
2. A Vega orchestrator can **accept invocations** from orchestrators I have explicitly added as peers, scoped to agents I have explicitly exposed.
3. Every cross-orchestrator op is **audited** — peer identity, agent, op id, tokens, cost, status, denial reason.
4. The local user has a **visual cue** for federation activity (connected peers count, per-message origin badge, live ops view).
5. Peering ships in the standard `vega serve` binary, **independent of v39a**. Any Vega anywhere — laptop, Fly machine, raspi — can enable it via config.

### Non-goals (v1)
- **Public federation.** No discovery, no "find an agent that can do X". Trust is a manually curated friends-list. Public discovery is a phase 3 concern when AIRE v0.2 lands DIDs and handle resolution.
- **Sandboxing remote tool execution.** The local agent fully owns what it does with an inbound message. The peer cannot reach past the agent boundary.
- **Cross-orchestrator memory sharing.** Memory writes triggered by a remote invocation go to the local agent's normal memory store — they do not cross orchestrators.
- **Replacing MCP.** MCP remains the right tool for "agent uses a service". AIRE is for "orchestrator talks to orchestrator".
- **Blocking on AIRE ≥ v0.2.** v1 ships on AIRE v0.1 with a custom HELLO capability for auth.

## 2. Architecture

### 2.1 Package layout

```
govega/
  serve/peering/         ← new; ONLY package allowed to import aire-go
    node.go              ← wraps aire.Node, owns lifecycle
    inbound.go           ← aire.Agent impl → Interpreter.StreamToAgent
    outbound.go          ← Dial peer, expose remote agents as tools
    auth.go              ← vega.shared-secret/1 capability handshake
    acl.go               ← peer + grant lookups, per-op authorization
    audit.go             ← writes to events + peer_audit_log
    config.go            ← peers + grants from settings table
    types.go             ← internal types; no public AIRE leakage
    *_test.go
```

**Hard rule (mirrors the AIRE-side rule):** outside `serve/peering/`, no other govega file imports `github.com/aire-protocol/aire-go`. This keeps the adapter swappable and the spec dependency localized.

### 2.2 Boot order in `serve/server.go:Start()`

Insert peering between MCP auto-connect and Iris injection:

```
[existing]
  1. Init store (SQLite or Postgres)
  2. Auto-connect MCP servers
  3. Restore composed agents
  4. Register memory tools
  5. Register channel tools
[NEW]
  5b. peering.Init(ctx, interp, store) — if VEGA_PEERING_ENABLED=1
        - Load peers + grants from settings table
        - Start aire.Node listener on VEGA_PEERING_ADDR (default :4433/udp)
        - Register inbound aire.Agent handler
        - For each known peer + granted remote agent, register
          tool "peer__<handle>__<agent>" in interp.Tools()
[existing]
  6. Inject Hera
  7. Inject Iris (now also gets a `send_to_remote_agent` tool)
  8. Mira, scheduler, telegram, http server
```

### 2.3 Outbound flow (Vega → peer)

```
Iris invokes tool: send_to_remote_agent(peer="@etienne@nous", agent="researcher", message=...)
  ↓
serve/peering/outbound.go:
  1. Resolve peer handle → peer_orchestrators row → NodeID + endpoint
  2. Get-or-dial cached aire.Conn
     - On first dial: TLS + QUIC + HELLO with vega.shared-secret/1 capability
     - HMAC(shared_secret, local_node_id || nonce || timestamp)
     - Peer verifies + responds with their HMAC; we verify theirs.
  3. ACL check: do we trust this peer to receive a message containing our user's request?
  4. conn.Invoke(ctx, agent, "send", argsJSON) → returns *aire.Operation
  5. Read STREAM frames in a goroutine, feed each chunk into the local inbox as
     a streaming completion (mirrors DispatchToAgent's inbox pattern).
  6. On final frame or ERROR frame: write peer_audit_log row, close op.
  7. Return immediately to Iris with "dispatched to @peer/agent" (non-blocking,
     same UX as local DispatchToAgent).
```

### 2.4 Inbound flow (peer → Vega)

```
aire.Node.Listen accepts QUIC conn:
  1. Handshake — verify vega.shared-secret/1 HMAC against peer_orchestrators
     - Bind peer NodeID to this conn. Reject if not in peer list.
  2. Per Invoke frame:
       a. ACL check: peer_agent_grants(peer_node_id, agent) exists + active?
       b. Rate-limit check: ops/hour for this (peer, agent)?
       c. Budget reservation: max_tokens_per_op for this grant
       d. Authorization audit row written BEFORE dispatch (so denials are
          visible too — denials never enter step e)
       e. Dispatch: Interpreter.StreamToAgent(ctx, agent, args.message)
          where ctx carries WithPeerCaller(peer_node_id) — the local agent's
          memory + tools can see "I was called by a remote peer X"
       f. Stream ChatEvent → STREAM frames → peer
       g. On agent completion: write final audit row (tokens, cost, status)
       h. Close operation
  3. Per Cancel frame (v0.1: connection close; v0.3: CANCEL frame):
       - Cancel the context driving step e. Local agent observes cancel.
```

### 2.5 Mapping AIRE primitives to Vega concepts

| AIRE | Vega |
|---|---|
| `NodeID` | `peer_orchestrators.node_id` (v0.1: opaque `vega:<uuid>`; v0.2: did:web) |
| Custom capability `vega.shared-secret/1` | HELLO-time auth (v0.1 only; replaced by DID signing in v0.2) |
| `Invoke{AgentID, Operation, Args}` | One peer-initiated `StreamToAgent` call |
| `STREAM` frames | `ChatEvent` chunks (tokens, tool calls) → JSON-encoded payload |
| `ERROR` frame | Denial, budget exceeded, agent failure |
| `CANCEL` frame (v0.3) | `context.CancelFunc` driving the StreamToAgent |
| `BUDGET` frame (v0.3) | Per-op token cap (until v0.3 lands, we enforce in Vega) |

## 3. Identity and trust (v0.1)

**Identity:**
- Each Vega instance generates a stable `NodeID = "vega:" + uuidv4()` on first boot.
- Persisted in settings table key `peering.local_node_id`.
- Surfaced in UI and CLI (`vega node-id`) for sharing with peers.

**Auth handshake (revised):** AIRE's `Capability` carries only `{Name, Version, Required}` — no extension payload — so the HMAC exchange cannot ride inside the HELLO frame itself. The capability `vega.shared-secret/1` is the *interop signal* both sides commit to do shared-secret auth (mandatory; absent → `MISSING_REQUIRED_CAPABILITY`). The HMAC exchange happens immediately after `Handshake()` returns, as a dedicated AIRE Operation on a well-known agent ID `_aire/auth` with op = `challenge`. The flow is symmetric: each side invokes the other; both must succeed before any application Invokes are accepted. The protocol (nonce, claim shape, HMAC inputs) lives in `serve/peering/auth.go`.

**Trust establishment (manual, by design):**
1. Out-of-band: I send you my NodeID + endpoint + shared secret (e.g., via Signal).
2. You add me to `peer_orchestrators` with `trust_level = scoped`.
3. We both add each other. Symmetric.
4. Now you can grant individual agents to my NodeID via `peer_agent_grants`.

**Why pre-shared secret and not TLS client certs in v1?**
Lower UX cost. Sharing a 32-byte secret over an out-of-band channel is one copy-paste. Client cert provisioning is a project. The shared-secret capability is explicitly a v0.1 stopgap and is replaced wholesale by AIRE v0.2 DID verification — the rest of the stack (ACLs, grants, audit) doesn't change.

## 4. Permissions model

Three SQLite tables (Postgres mirrors exist via the existing migration pattern):

### 4.1 `peer_orchestrators`
```sql
node_id        TEXT PRIMARY KEY    -- "vega:<uuid>" or did:web in v0.2
handle         TEXT UNIQUE         -- human-friendly: "@etienne@nous"
endpoint       TEXT NOT NULL       -- "quic://host:4433"
shared_secret  TEXT NOT NULL       -- 32-byte hex; rotated by re-adding peer
trust_level    TEXT NOT NULL       -- 'trusted' | 'scoped' | 'paused'
added_by       TEXT                -- user_id who added this peer
added_at       TIMESTAMP
last_seen_at   TIMESTAMP
notes          TEXT
```

### 4.2 `peer_agent_grants`
```sql
id                   INTEGER PRIMARY KEY
peer_node_id         TEXT REFERENCES peer_orchestrators(node_id)
local_agent_name     TEXT NOT NULL    -- canonical (lowercase, no :pid suffix)
max_tokens_per_op    INTEGER NOT NULL DEFAULT 8000
max_ops_per_hour     INTEGER NOT NULL DEFAULT 30
active               BOOLEAN NOT NULL DEFAULT 1
created_at           TIMESTAMP
UNIQUE(peer_node_id, local_agent_name)
```

Default-deny: no row, no access. Iris exposes `grant_peer_access` and `revoke_peer_access` tools.

### 4.3 `peer_audit_log`
```sql
id              INTEGER PRIMARY KEY
ts              TIMESTAMP NOT NULL
direction       TEXT NOT NULL       -- 'inbound' | 'outbound'
peer_node_id    TEXT NOT NULL
peer_handle     TEXT
agent           TEXT NOT NULL
op_id           INTEGER NOT NULL
tokens_in       INTEGER
tokens_out      INTEGER
cost_usd        REAL
status          TEXT NOT NULL       -- 'started' | 'ok' | 'denied' | 'error' | 'cancelled'
denial_reason   TEXT
duration_ms     INTEGER
```

Also emitted to the existing `events` table with `kind='peer_op'` so it shows up in the existing event stream / SSE feed for free.

## 5. Frontend additions

(Following the modal-not-sidebar rule from feedback memory.)

### 5.1 Header pill
- Shows `🌐 N` where N = currently-connected peers (live count from `peering.Node.PeerCount()`, polled via existing SSE event stream).
- Click → opens the Federation modal.
- Hidden entirely if `VEGA_PEERING_ENABLED != 1`.

### 5.2 Per-message badge
- Any chat message whose origin is a remote peer renders a small inline badge: `via @etienne@nous/researcher`.
- Implemented as a field on the existing `ChatMessage` shape: `peer_origin?: { handle, agent }`.

### 5.3 Federation modal (`components/PeeringModal.tsx`)
Tabs:
1. **Peers** — list of `peer_orchestrators`. Add / pause / remove. Shows last-seen timestamp.
2. **Grants** — table of `peer_agent_grants`. Toggle active, edit limits.
3. **Live ops** — current inbound + outbound ops with peer, agent, elapsed, tokens-so-far, cancel button.
4. **Audit** — paginated `peer_audit_log` with filters.

No new routes — modal opened from header pill, like the existing detail modals.

## 6. Iris integration

Two new tools registered in `dsl/iris.go` alongside `send_to_agent`:

- `send_to_remote_agent(peer, agent, message)` — outbound dispatch. Non-blocking, same inbox-on-completion UX as `send_to_agent`. Failure modes (peer unreachable, denied, budget exceeded) are explicit in the returned acknowledgment string.
- `list_peers()` — returns JSON of `[{handle, node_id, granted_agents, last_seen_at}]` so Iris can answer "who can I talk to".

Peer management (`add_peer`, `grant_peer_access`, `revoke_peer_access`) lives behind a permission gate — only callable by the canonical user, never by remote-invoked agents. Enforced by checking `PeerCallerFrom(ctx)` and rejecting if present.

## 7. Migration path to AIRE v0.2+

When AIRE v0.2 lands (DIDs + handles):

1. `peer_orchestrators.node_id` accepts `did:web:...` values. Old `vega:<uuid>` entries continue to work in parallel; treat them as a deprecated alias namespace.
2. Replace `vega.shared-secret/1` capability with the v0.2 DID-signing handshake. ACL tables don't change.
3. Add a `discover_peer(handle)` Iris tool that resolves `@handle@domain` → DID → endpoints. Trust still requires explicit user confirmation — discovery ≠ trust.

When AIRE v0.3 lands:

4. Wire `CANCEL` frame propagation through `Iris` so cancelling a delegated process upstream cancels the remote op (currently: cancellation works but propagates via stream close, which is best-effort).
5. Wire `BUDGET` frames into the per-op token cap; today we hard-cut the stream from the Vega side.

When AIRE v0.4 lands:

6. Resumability across orchestrator restart. The audit log already has the op_id; we add `resumption_token` to a new `peer_active_ops` row.

## 8. Risks and mitigations

| Risk | Mitigation |
|---|---|
| **NAT traversal on home setups** — QUIC over UDP, many home routers do not forward UDP cleanly. | Document explicit hosted-instance recommendation for "always available" peers. Laptop peers join when online; trust that. Do NOT propose SSH tunnels (memory: feedback_no_ssh_control_plane). |
| **Shared-secret leakage** — a leaked secret = full impersonation. | Easy rotation: editing the peer row generates a new secret; old conns get torn down. v0.2 DIDs eliminate this entirely. |
| **Compromised trusted peer** — if I trust @bob and @bob is owned, attacker invokes my granted agents at @bob's allowed rate. | (a) Default budgets are low (8000 tokens/op, 30 ops/hr). (b) Audit is real-time. (c) `trust_level='paused'` is one-click in the Federation modal. |
| **Memory poisoning via remote invocation** — remote message → local agent calls `remember` → poisoned memory. | Context carries `PeerCallerFrom(ctx)`; memory tools refuse writes when present (or write to a quarantined namespace). Belt-and-braces: agent system prompts include a "remote-origin" disclaimer. |
| **Cost overrun** — remote peer spams cheap requests, our LLM bill explodes. | `peer_agent_grants` enforces both per-op and per-hour caps. Cost in audit log; UI surfaces a "this week" total. |
| **Local agent calls back to a remote agent in a loop** — federation loop. | Per-conversation depth counter in ctx; refuse if depth > 3. Same pattern as Iris already uses for local dispatch loops. |

## 9. Spec-side issues to file against `aire-protocol/aire-spec`

These come out of the design and should land as GitHub issues. They are findings, not asks — `aire-spec` decides whether/how.

1. **Non-normative auth example for v0.1.** Document the shared-secret-via-custom-capability pattern as an example of how implementers can layer authentication on top of v0.1's opaque NodeID without waiting for v0.2 DIDs.
2. **Capability naming convention.** Should `vega.shared-secret/1` follow a reverse-DNS or namespace scheme? (e.g. `vendor.thing/version`). Pin this down before more implementers ship custom capabilities.
3. **Operation-level cost reporting.** Even before BUDGET frames, would a non-normative recommendation to encode `{tokens_in, tokens_out, usd}` in the ERROR/final-STREAM payload help interop? Useful for audit logs across implementations.
4. **Handle scheme for v0.2.** Confirm that `@agent@domain` (Mastodon-style) is preferred over `agent.domain` (DNS-style). The Vega Federation modal UX hinges on this.
5. **Cancel semantics in v0.1.** Today, stream-close ≈ cancel. Worth one paragraph in §7 spelling that out as the v0.1 contract.

## 10. Phase 1 implementation checklist (TDD)

In order:

- [ ] **1.1** `serve/peering/auth_test.go` — table-driven test for HMAC capability negotiation. Fail first.
- [ ] **1.2** `serve/peering/auth.go` — implement; tests pass.
- [ ] **1.3** Store migrations: `peer_orchestrators`, `peer_agent_grants`, `peer_audit_log`. Tests against both SQLite + Postgres via existing dual-test pattern.
- [ ] **1.4** `serve/peering/acl_test.go` + `acl.go` — grant lookups, default-deny, rate-limit accounting.
- [ ] **1.5** `serve/peering/inbound_test.go` + `inbound.go` — `aire.Agent` impl, dispatch to `StreamToAgent`, audit on entry + exit. Test with a stubbed Interpreter.
- [ ] **1.6** `serve/peering/outbound_test.go` + `outbound.go` — Dial + Invoke + stream-into-inbox. Test against a local `peering.Node` instance.
- [ ] **1.7** `serve/peering/node.go` — lifecycle: Init, Shutdown. Tests verify clean shutdown drains in-flight ops.
- [ ] **1.8** Wire into `serve/server.go:Start()`. Env flag `VEGA_PEERING_ENABLED`. Integration test: two `Server` instances peer with each other in-process.
- [ ] **1.9** Iris tools: `send_to_remote_agent`, `list_peers`, `add_peer`, `grant_peer_access`, `revoke_peer_access`. Register in `irisToolNames` (dsl/iris.go).
- [ ] **1.10** Frontend: `PeeringModal.tsx`, header pill, per-message badge. Manual browser test of the round trip.
- [ ] **1.11** Documentation: short section in `docs/ARCHITECTURE.md`, and update `website/` (per memory: update-website-with-govega).
- [ ] **1.12** File the spec-side issues from §9 against `aire-protocol/aire-spec`.

## 11. Open questions

1. **Single shared-secret per peer, or per (peer, agent) pair?** Per-peer is simpler. Per-pair is more granular but pushes complexity into the trust-establishment UX. Default: per-peer.
2. **Where does the local agent know "I was called by a peer"?** Proposed: `ctx` carries it, agent system prompts get a "you may be invoked remotely; some tools are disabled" injection. Need to spec which tools auto-disable for remote callers (probably: peer management tools, and `remember`/`forget` unless explicitly opted-in).
3. **Audit retention.** Forever? Or 90-day rolling with summary? Default: forever (storage is cheap; audit is the whole point).
4. **Multi-user Vega instances.** Today Vega is one-bot-one-user (memory: feedback_single_user_bot). When a peer invokes an agent, whose memory does the agent read? The local single user's. Confirmed.
