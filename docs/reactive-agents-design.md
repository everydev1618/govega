# Reactive Agents — Event-Triggered Cognition with Per-Agent Memory

> Status: **Design draft** · 2026-07-01 · Author: et + Claude
> Scope: Implementation plan for letting agents *react* to events — waking into a thinking cycle, filtered by salience, with their own memory attached.
> Audience: Vega contributors.

## 0. Motivation

> *"When I walk through a house I am triggered by events that send me into thinking patterns that rely on my memory."*

An agent should have three things that make it feel like a *someone*: a **personality**, a **memory**, and a set of **events it reacts to**. Two of the three already exist in Vega as first-class, per-agent concepts:

| Faculty | Existing mechanism | Status |
|---|---|---|
| Personality | `Agent.System` prompt, composed at creation, persisted in `composed_agents` | ✅ per-agent |
| Memory | `memory_items` / `memory_pages` keyed by `(user_id, agent)`, injected via `Process.SetExtraSystem` | ✅ read side; ⚠️ write-back on short reactive wakes missing (§3) |
| **Reactivity** | `EventBus` (worker telemetry) + `EventBroker` (SSE fan-out) — **neither feeds cognition** | ❌ **the gap** |

Today an agent only "thinks" when explicitly called: **time** (`Scheduler` → `SendToAgent`), **a message** (Telegram/HTTP → `StreamToAgent`), or **another agent** (`send_to_agent` tool → `DispatchToAgent`). There is no listener that turns an event into a new thinking cycle. This doc specifies that listener — **and** the second half people forget: the wake has to *change* the memory (self-learning, §3), or the agent reacts without ever learning from reacting.

## 1. Goals and non-goals

### Goals (v1)
1. An agent can **declare which events it reacts to** (`triggers`), persisted alongside the rest of its blueprint.
2. When a matching event fires, the agent **wakes into a normal cognition cycle** (`SendToAgent`) with its per-agent memory already injected — continuity of self across ephemeral processes.
3. A **salience gate** stands between "event fired" and "spend tokens," so not every creak in the house runs full cognition.
4. Reactive runs are **supervised**: budget-bounded, circuit-broken, and loop-guarded, so a reactive fleet cannot run away.
5. Ships in the standard `vega serve` binary; no external dependency. First event sources are *internal* (agents reacting to each other and to the scheduler) before any external sensors.
6. **Self-learning: a reactive wake consolidates into memory when it ends** (§3), so the agent accumulates experience *across* wakes, not just within a single conversation. An agent that reacts but never remembers reacting is out of scope for "done."

### Non-goals (v1)
- **External-world sensors** (filesystem watchers, inbound email/webhooks as event sources). The bus is designed to accept them; wiring them is phase 2+.
- **Cross-agent / cross-orchestrator event propagation.** Events stay within one orchestrator. (Peering is a separate axis — see `peering-design.md`.)
- **A rules DSL of its own.** v1 gate matching is deliberately small (see §5). A richer predicate language is a later concern.
- **Replacing the scheduler.** Cron stays. A cron fire simply becomes *one kind of event* on the same bus.

## 1.5 Architectural decisions

These were chosen on architectural merit, explicitly *not* to minimize the diff. An earlier pass proposed keeping core untouched (registry-side triggers, `Data["origin"]`, piggyback on the SSE broker); each was reversed because the additive version was the worse design.

- **D1 — Triggers live on the `Agent` blueprint**, not a router-side registry. Reactivity is the third co-equal faculty alongside personality (`System`) and memory; it belongs where the other declarative faculties live. A side registry would create two sources of truth for "what is this agent." Cost: edits the core `Agent` type (backward-compatible; zero-value = today's behavior).
- **D2 — Causation is a typed `Origin` field**, not a stringly-typed map entry. The loop guard and audit trail depend on it; it is load-bearing for safety and must not be fragile.
- **D3 — One unified event spine.** The two existing ad-hoc event systems become projections of a single `events.Bus`. Cost: refactors working SSE/telemetry code. Payoff: durability, typing, and causation become properties of the spine rather than per-consumer hacks, and there is one place events live.
- **D4 — Authored personality is fixed; learning accrues to an earned-character layer.** The `System` prompt is never rewritten by experience (that drifts the voice unpredictably and is a safety hazard). Instead, self-learning consolidates into the always-injected `MEMORY.md`, a writable band that sits beside the authored spine. "Who the agent is" = authored traits + lived character; only the second learns. See §3.5.

## 2. Architecture

### 2.1 The bridge, in one line

```
                        ┌──────────────────────────────────────────┐
   emitters ───────────▶   domain-event spine  (typed · durable ·   │
   (orchestrator,      │   causation-aware)                          │
    scheduler,         └───┬───────────────┬────────────────┬───────┘
    tools, sensors)       ▼               ▼                ▼
                     SSE projection   worker telemetry   TriggerRouter
                     (UI, existing     (existing          │
                      behavior)         behavior)         ▼
                                                    [salience gate]
                                                          │
                                                          ▼
                                              SendToAgent(agent, evt)
                                                          │
                                          memory injected via ContextWithMemory (existing)
```

The event spine is now a first-class concept, not an accretion. The SSE broker and worker telemetry become *subscribers/projections* of it; reactive triggers are a third subscriber. Cognition is still reached through the unchanged `SendToAgent` + `ContextWithMemory` path.

### 2.2 Package layout

```
govega/
  events/                  ← new: the domain-event spine (root-level, no serve/dsl deps)
    event.go               ← Event type: Type, Data, Origin (typed causation), Time
    bus.go                 ← Bus: Publish + Subscribe; durable delivery (see §2.3)
    bus_test.go
  reactive/                ← new: the trigger layer (imports events + interpreter)
    router.go              ← TriggerRouter: subscribes to the bus, matches events → agents
    gate.go                ← salience gate: rules → optional cheap-model classifier
    loopguard.go           ← per-(agent,event) rate/dedup/depth guard (uses Origin)
    router_test.go
    gate_test.go
    loopguard_test.go
```

Two packages, deliberately split: `events` is the neutral spine (anyone can publish/subscribe, zero knowledge of agents); `reactive` is the opinionated consumer that turns events into cognition. `Trigger` lives on the `Agent` blueprint (§4.1), not in `reactive`, so the router reads triggers off the agent registry rather than owning a parallel one. Both packages are root-level so the library face can use them without the HTTP server.

### 2.3 The event spine and its taxonomy

Today there are **two** ad-hoc event mechanisms, and this design **consolidates them into one** (decision D3, §1.5):
- `vega.Event` (`eventbus.go`): `started / progress / completed / failed / heartbeat` — worker→orchestrator status.
- `serve.BrokerEvent` (`serve/types.go:219`): `process.started / .completed / .failed` — SSE fan-out to UI.

Both become **projections of a single `events.Bus`**. `BrokerEvent`→SSE stays as-is on the surface (UI behavior unchanged) but is fed from the spine; worker telemetry publishes onto the spine too. This is a refactor of working code, taken deliberately so "events an agent reacts to" is a first-class subsystem rather than a piggyback.

**The `events.Event` type** carries typed causation from the start:

```go
type Event struct {
    Type   string         // dotted noun.verb, e.g. "agent.completed"
    Data   map[string]any // event-specific payload
    Origin *Origin        // typed causation — what caused this event (nil = external/root)
    Time   time.Time
}

type Origin struct {
    EventID   string // the event that triggered the run that emitted this one
    AgentName string // the agent whose reactive run emitted it
    Depth     int    // reactive-chain depth; the loop guard's primary signal (§5.3)
}
```

Causation is a **typed field, not a `Data["origin"]` string** (decision D2, §1.5): the loop guard's depth check and the audit trail both depend on it, so it is load-bearing for safety and deserves to be first-class.

The v1 taxonomy (small, namespaced, `noun.verb`):

| Event type | Emitted when | source (as built) |
|---|---|---|
| `agent.completed` | an agent finishes a dispatched task, or a reactive wake finishes | dispatch-complete callback (`SetDispatchCompleteCallback`) **and** the router self-emits each wake's completion. *Not* `OnProcessComplete` — persistent chat processes rarely `Complete()`, and emitting from both would double-fire for ephemeral dispatch processes. |
| `signal.*` | emitted explicitly by an agent | `emit_event` built-in tool |
| `schedule.fired` | a cron entry fires | scheduler `makeFunc` (Phase 2) |
| `agent.said` | an agent posts to a team channel / inbox | `DispatchToAgent` result path (Phase 2) |
| `memory.wrote` | an agent writes memory (`remember`) | `serve/memory_tools.go` (Phase 2) |

**Causation is threaded (D2).** When the router fires a wake for event `E`, it stamps a child `Origin{EventID, AgentName, Depth: Depth(E)+1}` onto the wake's context and onto the `agent.completed` it emits when the wake finishes. Any dispatch made *inside* the wake reads that Origin from the context, so delegated work inherits the chain depth too. The loop guard blocks incoming events at `Depth >= MaxDepth`, so a self-referential chain (an agent reacting to its own completions) terminates rather than running forever — the depth guard has teeth.

Internal vocabulary first — agents reacting to each other, the clock, and explicit signals. External sensors (phase 3) emit into the same namespace (`email.received`, `file.changed`, …) with no router changes.

**Durability.** The spine's delivery is **not** the SSE broker's drop-on-full behavior (which is correct for a UI that can miss a frame, wrong for a trigger that must not). The `events.Bus` gives reactive subscribers durable delivery — a bounded persisted ring backed by an `events` table with replay on restart. UI/telemetry subscribers can still opt into lossy fast-path delivery. Making durability a *property of the spine* rather than a per-consumer hack is the payoff of unifying (D3).

### 2.4 Boot order in `serve/server.go:Start()`

```
[existing]
  ... init store, restore composed agents, memory tools, Hera, Iris ...
[NEW]
  X.  events.NewBus(store)                    — construct the spine; SSE broker + telemetry
                                                subscribe as projections
  Xb. reactive.NewRouter(interp, bus)         — reads Triggers off each agent in the registry;
                                                registers the emit_event tool; starts goroutine
[existing]
  ... scheduler (publishes schedule.fired onto the spine), telegram, http server ...
```

Because the router reads triggers directly off the agent registry (triggers live on the blueprint, D1), there is no separate subscription table to keep in sync — adding/updating an agent via Hera updates its triggers in the one place they exist.

## 3. Memory, personality, and the self-learning loop

Reactivity is only half of "events send me into thinking patterns that rely on my memory." The other half is that the thinking *changes* the memory — otherwise the agent is a reflex, not a self. This section makes the memory model explicit and specifies the self-learning loop that closes it.

### 3.1 The three layers (what already exists)

Your intuition — personality memory, long-term memory, and interaction-driven cognition — maps onto the cognitive stack, and Vega has a construct for each:

| Layer | Brain analog | Vega construct | Persists? | Learns today? |
|---|---|---|---|---|
| **Personality** | stable traits | `Agent.System` (composed at creation; `composed_agents.system`/`persona`) | ✅ authored | ❌ fixed |
| **Long-term memory** | semantic memory | wiki pages (`memory_pages`: `MEMORY.md`, `topics/*.md`, dated session notes), `memory_items`, `user_memory` — keyed `(user_id, agent)` | ✅ durable | ✅ grows |
| **Working memory** | prefrontal working set | `Process.messages` (ephemeral, trimmed at 100) | ❌ per-conversation | — |

At think-time all three compose into one head: `Agent.System.Prompt()` + injected memory (`SetExtraSystem`, top of prompt) + the live message thread. Personality and long-term memory are stitched together every turn — that is "personality fueled by its own memory."

**There is already a consolidation link** between working and long-term memory: `maybeCompactToWiki` (`serve/memory_wiki_compact.go`). When a conversation passes **40 messages**, it distills the older portion into a dated session note in the agent's private wiki, links it from `MEMORY.md`, and keeps the last 12 messages verbatim. Plus the agent can *actively* write memory anytime via `remember` / wiki tools. So the read side of the loop is fully there, and one write path exists.

### 3.2 The self-learning loop

Self-learning is the full cycle, not just recall. The input side (perceive → gate → act) is what §4–§6 build; the output side (consolidate → promote) is what makes it *stick*:

```
  event ─▶ [salience gate] ─▶ COGNITION ─▶ act/effects
   ▲            (§5)          (SendToAgent)     │
   │                                            ▼
   │                                   [consolidation gate]   ← the missing symmetric half
   │                                            │
   │                              distill episode → session note
   │                                            │
   │                              promote if recurring:
   │                       session note → topic page → MEMORY.md (always injected)
   │                                            │
   └──────────── next wake recalls it ◀─────────┘
```

Note the **symmetry**: a *salience* gate on the input decides what deserves cognition; a *consolidation* gate on the output decides what deserves remembering. The brain does both — attention on the way in, memory-consolidation on the way out. Vega has the first (being built) and needs the second.

### 3.3 The gap: reactive wakes are too short to consolidate

The existing consolidation trigger is **length-based (40 messages)**. But a reactive wake is a *short burst* — an event fires, the agent thinks a few turns, acts, done — almost always **under** the threshold. So a reactive agent could wake and act a hundred times and **consolidate nothing**: each burst ends below 40 messages, its working memory evaporates, and it never remembers having reacted. That is an agent that reacts but never *learns from reacting* — Groundhog Day. **This is the load-bearing gap for self-learning**, and it is why §6's earlier "continuity of self for free" was only half true (it covered read/injection, not write-back).

### 3.4 What to add: end-of-wake consolidation (self-learning)

On **reactive process completion**, run consolidation *regardless of length*, importance-gated:

- **Trigger:** `Process.Complete()` on a reactively-spawned process (tagged via `Origin`), not the 40-message counter. Hooks alongside the existing `maybeCompactToWiki` call site.
- **Reuse:** the same `proc.Compact()` → dated-session-note machinery; no new storage layer.
- **Importance gate:** a cheap classifier (or rules) decides *whether this episode is worth a note* — trivial no-op reactions ("event fired, nothing to do") are dropped so the wiki doesn't fill with noise. This mirrors the input salience gate and shares its cost profile.
- **Outcome-aware (the self-learning payload):** the note records not just *what happened* but *what the agent did and how it turned out* — action → outcome. This is what lets the next wake do better rather than merely recall. Over time these accrete into procedural knowledge ("when X fires, doing Y works / Y failed last time").

### 3.5 Earned personality vs. authored personality (decision D4)

Does personality *evolve*? Two options: rewrite the `System` prompt from experience (a coherence and safety nightmare — the authored voice drifts unpredictably), or keep `System` fixed and let learning accrue to the **always-injected `MEMORY.md`** layer, which is effectively a *second, earned* personality band that grows from consolidation. We take the latter (**D4**): authored traits stay stable; earned character accumulates in a layer that is injected every turn just like the system prompt, but is writable. An agent's "who I am" thus has two parts — the authored spine and the lived character — and only the second learns.

### 3.6 Consolidation hierarchy (how a fact becomes a disposition)

Learning deepens by *promotion*, mirroring episodic→semantic consolidation:

```
working memory  ──distill──▶  dated session note  ──recurs──▶  topic page  ──core/recurring──▶  MEMORY.md
 (this wake)                  (this episode)                   (this theme)                    (always injected)
```

A one-off stays a session note (conditionally recalled). A pattern seen repeatedly gets promoted toward `MEMORY.md`, where it is *always* injected and thus becomes part of how the agent shows up by default — the mechanism by which repeated experience becomes disposition without ever touching the authored `System`. Promotion is where "self-learning" turns into "changed behavior."

## 4. Agent-side: declaring triggers

### 4.1 Blueprint field

Add to `agent.go`:

```go
type Trigger struct {
    On    string   // event type pattern, e.g. "agent.completed", "signal.*"
    Where string   // optional cheap predicate over event Data (see §5.1)
    Gate  string   // "" (rules only) | "model" (cheap classifier) — default ""
    Prompt string  // template rendered into the wake message; {{.Data.x}} interpolation
}

type Agent struct {
    // ... existing fields ...
    Triggers []Trigger  // events this agent reacts to
}
```

### 4.2 DSL surface (`dsl/`)

```yaml
agents:
  night-watch:
    model: claude-haiku-4-5
    system: "You watch for failures and summarize them for the human each morning."
    triggers:
      - on: agent.completed
        where: "status == failed"
        prompt: "Agent {{.Data.agent}} failed: {{.Data.error}}. Note it for the morning digest."
      - on: schedule.fired
        where: "job == morning-digest"
        prompt: "Produce the overnight failure digest from your memory."
```

### 4.3 Persistence

Add a `triggers TEXT NOT NULL DEFAULT '[]'` column to `composed_agents` (JSON array of `Trigger`). `restoreComposedAgents` (`serve/handlers_population.go:591`) deserializes and re-registers with the router on boot. Hera's `create_agent` / `update_agent` (`dsl/hera.go`) gain a `triggers` param so agents can be given reactivity at runtime by chat.

## 5. The salience gate — the part not to skip

Your brain does not run full cognition on every photon walking through the house; the reticular system filters for salience first. Skipping this is how a reactive fleet becomes a runaway token bill and a feedback loop (agent acts → emits event → wakes agent → …).

The gate is a **two-tier funnel**, cheapest first:

### 5.1 Tier 1 — rules (free, synchronous)
- **Type match**: glob on `event.Type` (`agent.*`, `signal.custom`).
- **`where` predicate**: a *tiny* expression over `event.Data` — equality, `!=`, `contains`, `~=` (substring). No Turing-completeness. Rejected events never touch a model.

This tier handles the overwhelming majority. `night-watch` above only wakes on *failed* completions, not every completion.

### 5.2 Tier 2 — model gate (cheap, optional, `gate: model`)
For events where "is this worth my attention?" needs judgment, a **haiku-tier one-shot classifier** answers yes/no with a short reason before the expensive reactive run. Only reached if Tier 1 passed and the trigger opted in. Cost is one small completion; the reactive cycle it guards is many large ones.

### 5.3 Loop guard (always on)
Independent of the gate, `loopguard.go` enforces, per `(agentName, eventType)`:
- **Rate**: max N wakes / window (token-bucket).
- **Dedup**: identical event payload within T seconds → drop.
- **Depth**: `event.Origin.Depth` (the typed causation field, D2) caps reactive-chain depth — when a reactive run emits events, they inherit `Origin` with `Depth+1`. This is the direct, non-fragile defense against the act→emit→wake cycle.

## 6. Reactive dispatch — wiring to existing cognition

On a passed gate, the router calls the path the scheduler already proves works (`Scheduler.makeFunc` → `SendToAgent`, `serve/scheduler.go:176`):

```go
func (r *TriggerRouter) fire(ctx context.Context, agentName string, t Trigger, evt events.Event) {
    ctx = serve.ContextWithMemory(ctx, r.store, r.ownerUserID, agentName) // memory attached
    msg := renderPrompt(t.Prompt, evt)                                    // {{.Data.x}} interpolation
    _, _ = r.interp.SendToAgent(ctx, agentName, msg)                      // normal cognition
    // any events this run emits inherit evt.Origin with Depth+1 (loop guard, §5.3)
}
```

Because Processes are ephemeral but **memory is durable per-agent**, the agent that wakes *reads* with continuity-of-self: the "you" that reacts is the memory, not the process. But this covers only the read side — the wake must also *write back*, or the agent reacts without learning. That write-back is **end-of-wake consolidation** (§3.4): on `Process.Complete()` for a reactive process, distill the burst into memory (importance-gated), regardless of length. Read continuity is free; write continuity is the self-learning loop and is not — it is a required part of this design, not an optional enrichment.

**Single-user note.** Per `[[feedback_single_user_bot]]`, there is one user per bot, so `evt.forUserID()` resolves to the single owner; no per-user reactive fan-out.

## 7. Supervision — reactivity is a supervised child, not a loose loop

Vega's whole thesis is the supervision tree, and reactive+autonomous is exactly where that earns its keep. Reactive runs reuse the mechanisms already on `Agent`:
- **Budget** (`Agent.Budget`): a reactive process draws from the agent's budget like any other; exhaustion → no wake, logged.
- **Circuit breaker** (`Agent.CircuitBreaker`): repeated reactive failures open the breaker and suspend triggers for that agent.
- **Loop guard** (§5.3): the reactive-specific backstop.
- **Audit**: every wake (and every *gated-out* event) writes an event so the human can see what the house made each agent think about — and what it *chose to ignore*. Silent suppression is a bug; log the drop.

## 8. Open decisions

Resolved by §1.5: causation is typed (was #4); triggers live on the blueprint (was an implicit fork); the event system is unified (was the durability workaround). Remaining:

1. **Spine durability mechanism.** Persisted ring in the existing SQLite store vs. a dedicated `events` table with explicit replay cursor. Both survive restart; the question is replay granularity. Recommendation: `events` table with a per-subscriber cursor, since the unified spine now also carries telemetry worth retaining.
2. **Migration of existing SSE.** Cut the SSE broker over to the spine in one move, or run both briefly with the broker double-fed from the spine and old emitters until parity is proven. Recommendation: double-feed during Phase 1, delete the old direct path once the projection is verified.
3. **Gate model tier.** Build the interface, ship rules-only, turn on the haiku classifier behind the `gate: model` opt-in.
4. **Prompt vs. full context on wake.** v1: just the rendered `Prompt` + memory (cheap, legible). Recent-related-events context is a later enrichment.
5. **Consolidation importance gate.** Rules ("wake took ≥N turns / called a mutating tool" → keep) vs. a cheap classifier judging significance. And: does *every* reactive wake get considered for a note, or only non-trivial ones? Recommendation: rules-only in Phase 1 (keep if the wake acted; drop pure no-ops), classifier in Phase 2. This is the output-side twin of decision on the input gate tier (#3).
6. **Promotion policy (§3.6).** What counts as "recurring" enough to promote a session note → topic page → `MEMORY.md`? Frequency threshold, recency decay, or agent self-nomination via a tool. Deferred to Phase 2; Phase 1 stops at session notes.

## 9. Phasing

Unifying the spine (D3) makes Phase 1 bigger than the earlier "one bridge" framing — it now includes standing up `events.Bus` — but the payoff is that everything after is a subscriber, not a new mechanism.

- **Phase 1 — the spine + thinnest reactive slice + write-back.** Build `events.Bus` (typed `Event` + `Origin`, durable delivery) and make the SSE broker a double-fed projection of it (existing UI behavior unchanged). Then `reactive.Router` with rules-only gate + loop guard, `agent.completed` flowing on the spine, one agent reacting to it, `emit_event` tool, `Agent.Triggers` field wired from DSL. **And end-of-wake consolidation (§3.4)** — a reactive `Process.Complete()` distills the burst into a session note — because a slice that reacts without learning doesn't demonstrate the actual thesis. TDD: `events/bus_test.go`, `reactive/router_test.go`, `gate_test.go`, `loopguard_test.go`, `reactive/consolidate_test.go` first. Milestone: one agent wakes to another's completion *with its memory*, acts, and *remembers having done so* on the next wake — end to end.
- **Phase 2 — DONE so far: causation threading.** `Origin` is threaded through cognition (router context + dispatch context) and the router self-emits each wake's completion, so the loop guard's depth check terminates reactive chains (test: `TestReactiveChainTerminatesAtMaxDepth`). `agent.completed` now emits from the dispatch-complete callback, the realistic agent-to-agent trigger. **Remaining Phase 2:** rest of the taxonomy (`schedule.fired`, `memory.wrote`, `agent.said`); `composed_agents.triggers` column + Hera `triggers` param (runtime-created reactive agents); retire the direct SSE/telemetry paths; model gate tier; importance-gated + LLM-distilled consolidation and the promotion hierarchy (§3.6, outcome-aware notes rising toward `MEMORY.md`).
- **Phase 3 — the outside world.** External sensors (file/email/webhook) emitting into the namespace; audit UI ("what the house made each agent think about — and ignore").

## 10. Why this is the right shape

The metaphor is almost a spec: personality = authored blueprint + earned character (D4), memory = per-agent store (read side done), reactivity = the third faculty on the blueprint (D1), fed by a real event spine (D3) with safe causation (D2), a salience gate on the way in, and a consolidation gate on the way out (§3). It is more than "one bridge" — unifying the event system and closing the self-learning loop are deliberate investments — but it leaves Vega with one event spine instead of three ad-hoc ones, and an agent that is a complete, *learning* description of a reactive, remembering, personable entity: it is sent into thinking patterns by events, those patterns rely on its memory, and — crucially — they *leave a trace in that memory*. Without the last clause it's a reflex; with it, it's a self. The scheduler already proves stimulus→cognition; this generalizes the trigger from "the clock" to "any event on the spine," and adds the write-back that makes the loop a loop.
