# Hermes vs Vega

Reference: Hermes Harness Architecture post by Aparna Dhinakaran (2026-05).
Hermes = Nous Research's open-source agent harness.

## Slack message draft (for ADHD CEO)

Read the Hermes architecture post. Good harness. Real systems work. But the comparison to Vega is interesting and I want to flag it.

**Where Hermes beats Vega today:**
- Multi-provider (Anthropic, OpenAI, Bedrock, Codex) — we're Anthropic-only
- Explicit 3-tier prompt assembly (stable/context/volatile) — we do this implicitly
- Tool registration vs exposure separation — ours is coarser
- Plugin lifecycle hooks (pre/post-tool, approval flows) — we don't have this surface
- FTS5 session search exposed as a model tool

All fixable. None are deep.

**Where Vega already beats Hermes:**
The post ends with the author saying Hermes' next big step is *"moving from strong delegation to first-class orchestration — run IDs, lifecycle management, external steering, cleanup that survives parent completion."*

That's literally Vega. Supervisor, ProcessGroup, EventBus, supervision trees, process snapshots. OTP-style. We built the substrate Hermes is reaching for.

We also have things Hermes doesn't:
- Runtime agent authoring (Hera/Iris) — theirs is config-time
- YAML DSL for non-programmers
- Peering between instances
- Passive memory extraction + wiki distillation

**Net:** Hermes is a better single-user coding harness. Vega is a better long-lived multi-agent runtime. Different shape of bet. The gaps on our side are 2-4 weeks of focused work; the gap on their side is an architectural rewrite.

Happy to walk through it.

---

## Full 9-part frame mapping

| # | Component | Hermes | Vega |
|---|---|---|---|
| 1 | Outer loop | Streaming, multi-provider (Anthropic Messages, OpenAI chat, Codex Responses, Bedrock) | Streaming, Anthropic-only. `llm/` interface exists, no other adapters. |
| 2 | Context / compression | Aux-model summary, head/tail protection, prune old tool outputs, 20%/2k-12k budget, **SQLite session lineage chain** | `memory/` sliding-window + token budget; `compact.go` + `serve/memory_wiki_*` distills old turns into wiki notes. No lineage table — compaction rewrites same transcript. |
| 3 | Tools — register vs expose | Central registry + run-time resolver filtered by platform/scenario/profile/delegation | Global registry; per-agent tool list at spawn. No run-time resolver layer. |
| 4 | Subagent management | `delegate_task` with depth cap, but parent owns child lifecycle. Author flags as next gap. | **Vega's signature strength.** Process, ProcessGroup, Supervisor, EventBus, spawntree, process_link. OTP-style. |
| 5 | Built-in skills | SKILL.md index in stable prompt tier | `skills.go` + `examples/skills/*.skill.md` + `internal/skills` loader. Comparable. |
| 6 | Session persistence | SQLite + FTS5 + WAL, parent-child lineage, `session_search` exposed as a tool | SQLite (modernc) + optional Postgres. No FTS5, no lineage table, no model-facing search. |
| 7 | System prompt assembly | Explicit 3 tiers (stable/context/volatile), prompt-injection scan on cwd files | `SetExtraSystem` + `formatMemoryForInjection` + `ExtraSystemProvider`. Tiers not explicit in code. No injection scan. |
| 8 | Lifecycle hooks | Two surfaces: in-process plugin hooks + filesystem shell/python hooks | `wireCallbacks` (orchestrator), `routeHook` (HTTP). No pre/post-tool plugin surface. Real gap. |
| 9 | Permissions / safety | Allow/deny rules, approval flow, dangerous-cmd deny in delegated runs | `agent_ratelimit.go`, budget enforcement, per-agent MCP gating. No approval flow. Real gap. |

## Beyond the 9-part frame

- **Messaging gateway.** Hermes: Telegram/Discord/Slack/WhatsApp unified session model. Vega: Telegram + REST + SSE, less unified.
- **Profile system.** Hermes: isolated agent roots. Vega: composed agents + Iris share host state. Gap.
- **Cron.** Both have first-class cron with per-job gating. Vega: `scheduler.go` + `scheduled_jobs` table.

## What Vega has that Hermes doesn't

- Supervision trees as a first-class primitive (`supervisor.go`, `supervision.go`, `process_link.go`)
- Hera / Iris — runtime agent authoring via chat
- YAML DSL for non-programmers
- Peering (`serve/peering_*`) — trusted federation
- Active + passive memory with wiki distillation

## Suggested Vega next steps (low-hanging)

1. **Explicit prompt tiering** — refactor prompt assembly into named stable/context/volatile tiers. Cache-friendly. Cheap.
2. **Tool registration vs exposure** — add a resolver layer that filters tools by scenario/delegation, not just per-agent list.
3. **Plugin lifecycle hooks** — pre/post tool, approval flow surface.
4. **Approval gating** on tool calls (unlocked by the hooks above).
5. **Session lineage on compaction** — fork child chat_messages session on `memory_wiki_compact`, link parent→child.
6. **Multi-provider transport adapters** in `llm/` — OpenAI / Bedrock. Interface already exists.
7. **FTS5 + `session_search` as a model-facing tool** — complement existing `recall`.

## Architectural asymmetry

Hermes treats **sessions** as runtime infrastructure (routing plane).
Vega treats **processes** as runtime infrastructure (supervision plane).
Different sides of the long-lived-agent problem. Synthesis = use Vega's process plane as a session plane — roughly what Iris + peering is reaching toward.
