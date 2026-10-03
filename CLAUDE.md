# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
go build ./...                          # Compile all packages (quick check)
make build                              # Full build: frontend + Go binary → bin/vega
go test ./...                           # Run all tests (unit + fast e2e)
go test -short ./serve                  # Skip the slow (~45s) restart e2e scenarios
go test ./dsl -v                        # Test a specific package
go test -run TestInterpreter ./dsl      # Run a single test
```

The frontend (React 19 + Vite + Tailwind) is embedded via `//go:embed` in `serve/embed.go`. To build it separately: `make frontend-build`.

## Running Locally

To launch and drive the real app (manual verification, demos, screenshots), follow **`docs/PLAYGROUND.md`** — a verified walkthrough: build, `./bin/vega serve --addr 127.0.0.1:PORT --db <scratch>.db` (always use an isolated `--db`; the default touches the user's real `~/.vega` state), then the tour stops covering chat/SSE, agent building, MCP connect/restart, workflow interruption, and direct REST/SSE curls. `ANTHROPIC_API_KEY` is the only required env; chat turns spend real tokens (cents).

## Testing

Three tiers. Every change gets unit tests (TDD: write the failing test first, verify it fails, implement, verify pass); the e2e tiers cover what unit tests can't.

**Unit/integration** — colocated `*_test.go` throughout. Store-layer tests that must work on both backends use `forEachStore` (`serve/store_dual_test.go`): SQLite always runs; the Postgres subtest runs only when `VEGA_TEST_POSTGRES_URL` is set, otherwise skips.

**Deterministic full-stack e2e** — `serve/e2e_test.go` boots a real `Server` over HTTP with a scripted LLM (`fullstackLLM`, routed by system-prompt markers) and a real SQLite file. Covers the whole boot path, SSE chat with tool loops (incl. typed-block replay), workflow interruption across a server restart, and a real MCP stdio subprocess (`serve/testdata/mcpecho`) reconnecting from persistence. No tokens, loopback only; the restart scenarios skip under `-short`. Extend these when a change spans layers (handler ↔ interpreter ↔ store) or alters restart/boot behavior.

**Live API smoke** — `live_smoke_test.go` (root package) runs against the real Anthropic API:

```bash
VEGA_E2E_LIVE=1 go test -run TestLive .   # needs ANTHROPIC_API_KEY; ~6¢ on claude-sonnet-4-6
```

Validates what fakes cannot: the API *accepting* our wire format — thinking-block replay with signatures during tool loops (sync + streaming), prompt-cache breakpoints, cost accounting from real usage. Run before releases and after any change to `llm/` request building, the model tables, or content-block handling. Not part of default CI.

**Known flake**: the `TestHandleCreateAgent_*` family in `serve/handlers_population_test.go` fails rarely — only in full-suite runs (never isolated), clustering on the first run after a rebuild. A bare re-run passes. Pre-existing timing sensitivity; don't chase it as a regression of your change unless it reproduces in isolation.

## Architecture

Vega is an AI agent orchestration framework inspired by Erlang's supervision trees. It has two faces: a **Go library** (root package `vega`) and a **YAML DSL** (`dsl/` package) that non-programmers can use.

### Agent → Process model

An **Agent** (`agent.go`) is an immutable blueprint (model, system prompt, tools, budget, retry policy). A **Process** (`process.go`) is a running instance with state, messages, and metrics. One agent can spawn many processes. The **Orchestrator** (`orchestrator.go`) is the process registry and lifecycle manager.

Every spawned process must be completed (`proc.Complete()`) or failed (`proc.Fail()`) — leaking processes is a bug.

### Package map

| Package | Role |
|---------|------|
| root (`vega`) | Core types: Agent, Process, Orchestrator, Supervisor, ProcessGroup, EventBus |
| `dsl/` | YAML parser (`parser.go`) → AST (`types.go`) → Interpreter (`interpreter.go`). Also hosts meta-agents Hera and Iris |
| `llm/` | LLM interface + two backends. `types.go` defines the interface; `anthropic.go` and `openai.go` implement it; `factory.go` picks one from the environment |
| `tools/` | Tool registry (`tools.go`), built-ins (`builtin.go`), MCP integration (`mcp.go`), dynamic YAML tools (`dynamic.go`) |
| `serve/` | HTTP server, REST API, SSE streaming, SQLite persistence, Telegram bot, cron scheduler, memory system, embedded React frontend |
| `mcp/` | Model Context Protocol client (stdio + HTTP transports) |
| `memory/` | Token budget and sliding window context management |
| `cmd/vega/` | CLI entry point: `run`, `validate`, `repl`, `serve`, `version` |

### Self-hosted backends (`llm/`)

`llm.New()` returns the OpenAI-compatible client when `OPENAI_BASE_URL` is
set, else the Anthropic one, and `requireAPIKey()` accepts that base URL in
place of a key (the client falls back to `sk-local`). This is the whole
self-hosting switch: LM Studio, Ollama, vLLM and LiteLLM all work unchanged.

Four contracts, each of which cost a bug:

- **The streaming client carries no overall `Timeout`.** `http.Client.Timeout`
  spans the entire response body, so a bound there does not fail a slow
  stream, it truncates a working one mid-answer. Both backends keep a separate
  `streamClient` for this reason. The sync path stays bounded
  (`DefaultOpenAITimeout` / `DefaultAnthropicTimeout`, `WithOpenAITimeout`);
  streams are cancelled by the caller's context and nothing else.
- **Model overrides are taken only when the endpoint advertises them.** Agent
  and per-step models arrive on the context (`Agent.ModelFor` →
  `llm.ContextWithOptions`), and `openai.go` honours them against the lineup
  from `/v1/models` (cached, `lineupTTL`). A document written for a hosted
  provider names models a local endpoint has never heard of, so an
  unservable override falls back to the configured model and warns once per
  name — never silently, which is how it went unnoticed, and never by sending
  a request that 404s.
- **`LLMResponse.Model` is the model that answered**, which is not always the
  one requested. Log and price *that*: `reportLLMCost` reads the vendor off
  the model-name prefix, so reporting the request billed a local call to
  Anthropic from a machine with no Anthropic key. `effectiveModel` in
  `process_llm.go` is the one place that decides.
- **Timeouts around LLM calls are hosted-API assumptions until proven
  otherwise.** A local model's prefill alone can run for minutes on a long
  prompt. The bounds that exist are generous and tunable —
  `VEGA_INTRO_TIMEOUT` (the greeting turn, `serve/agent_intro.go`) and
  `VEGA_SUMMARY_TIMEOUT` (context compaction, `memory/memory.go`) — and both
  reject unparseable or non-positive values rather than build an
  already-expired context, which fails identically to a dead backend.
  Prefer inheriting the caller's context, as `compact.go` does.

### Meta-agents (in `dsl/`)

- **Hera** (`hera.go`): Creates/updates/deletes agents at runtime via chat. Accepts extra tools via `InjectHera(interp, callbacks, extraTools...)`.
- **Iris** (`iris.go`): Cross-agent orchestrator that routes goals to the right agent. Accepts extra tools via `InjectIris(interp, extraTools...)`. Has memory tools (`remember`, `recall`, `forget`).

### Tool registration pattern

Tools use `tools.ToolDef` with a `ToolFunc` signature `func(ctx context.Context, params map[string]any) (string, error)`. Register on the interpreter's global `Tools()` collection. Context carries process info (`ContextWithProcess`) and memory info (`ContextWithMemory`).

### Memory system (`serve/`)

Two paths feed into `memory_items` table:
- **Active**: Agents call `remember`/`recall`/`forget` tools during conversation (defined in `memory_tools.go`)
- **Passive**: After each exchange, `extractMemory` (`memory_extract.go`) runs an async LLM call to extract profile updates, topic updates, and notes

Memory is injected into agent system prompts via `formatMemoryForInjection()`. The `user_memory` table stores summary layers (profile, topics, notes); `memory_items` stores granular entries.

### Serve package flow

`server.go:Start()` → init SQLite → restore composed agents → register memory tools → inject Hera → inject Iris → start scheduler → start Telegram bot → wire orchestrator callbacks → HTTP server.

Chat handlers (`handlers_api.go`) load memory, inject it via `proc.SetExtraSystem()`, add `ContextWithMemory` to ctx, then call `SendToAgent`/`StreamToAgent`. After response, async memory extraction runs.

### Error classification (`errors.go`)

Seven categories: RateLimit, Overloaded, Timeout, Temporary → retry; Authentication, InvalidRequest, BudgetExceeded → no retry. The classification drives automatic retry decisions with configurable backoff.

### Persistence

SQLite via `modernc.org/sqlite` (pure Go, no CGO). Tables: events, process_snapshots, workflow_runs, composed_agents, chat_messages, user_memory, memory_items, scheduled_jobs.
