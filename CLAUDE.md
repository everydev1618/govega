# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
go build ./...                          # Compile all packages (quick check)
make build                              # Full build: frontend + Go binary → bin/vega
go test ./...                           # Run all tests
go test ./dsl -v                        # Test a specific package
go test -run TestInterpreter ./dsl      # Run a single test
```

The frontend (React 19 + Vite + Tailwind) is embedded via `//go:embed` in `serve/embed.go`. To build it separately: `make frontend-build`.

## Architecture

Vega is an AI agent orchestration framework inspired by Erlang's supervision trees. It has two faces: a **Go library** (root package `vega`) and a **YAML DSL** (`dsl/` package) that non-programmers can use.

### Agent → Process model

An **Agent** (`agent.go`) is an immutable blueprint (model, system prompt, tools, budget, retry policy). A **Process** (`process.go`) is a running instance with state, messages, and metrics. One agent can spawn many processes. The **Orchestrator** (`orchestrator.go`) is the process registry and lifecycle manager.

Every spawned process must be completed (`proc.Complete()`) or failed (`proc.Fail()`) — leaking processes is a bug.

### Package map

| Package | Role |
|---------|------|
| root (`vega`) | Core types: Agent, Process, Orchestrator, Supervisor, ProcessGroup, EventBus |
| `dsl/` | YAML parser (`parser.go`) → AST (`types.go`) → Interpreter (`interpreter.go`). Also hosts meta-agents Mother and Hermes |
| `llm/` | LLM interface + Anthropic backend with streaming. `types.go` defines the interface, `anthropic.go` implements it |
| `tools/` | Tool registry (`tools.go`), built-ins (`builtin.go`), MCP integration (`mcp.go`), dynamic YAML tools (`dynamic.go`) |
| `serve/` | HTTP server, REST API, SSE streaming, SQLite persistence, Telegram bot, cron scheduler, memory system, embedded React frontend |
| `mcp/` | Model Context Protocol client (stdio + HTTP transports) |
| `memory/` | Token budget and sliding window context management |
| `cmd/vega/` | CLI entry point: `run`, `validate`, `repl`, `serve`, `version` |

### Meta-agents (in `dsl/`)

- **Mother** (`mother.go`): Creates/updates/deletes agents at runtime via chat. Accepts extra tools via `InjectMother(interp, callbacks, extraTools...)`.
- **Hermes** (`hermes.go`): Cross-agent orchestrator that routes goals to the right agent. Accepts extra tools via `InjectHermes(interp, extraTools...)`. Has memory tools (`remember`, `recall`, `forget`).

### Tool registration pattern

Tools use `tools.ToolDef` with a `ToolFunc` signature `func(ctx context.Context, params map[string]any) (string, error)`. Register on the interpreter's global `Tools()` collection. Context carries process info (`ContextWithProcess`) and memory info (`ContextWithMemory`).

### Memory system (`serve/`)

Two paths feed into `memory_items` table:
- **Active**: Agents call `remember`/`recall`/`forget` tools during conversation (defined in `memory_tools.go`)
- **Passive**: After each exchange, `extractMemory` (`memory_extract.go`) runs an async LLM call to extract profile updates, topic updates, and notes

Memory is injected into agent system prompts via `formatMemoryForInjection()`. The `user_memory` table stores summary layers (profile, topics, notes); `memory_items` stores granular entries.

### Serve package flow

`server.go:Start()` → init SQLite → restore composed agents → register memory tools → inject Mother → inject Hermes → start scheduler → start Telegram bot → wire orchestrator callbacks → HTTP server.

Chat handlers (`handlers_api.go`) load memory, inject it via `proc.SetExtraSystem()`, add `ContextWithMemory` to ctx, then call `SendToAgent`/`StreamToAgent`. After response, async memory extraction runs.

### Error classification (`errors.go`)

Seven categories: RateLimit, Overloaded, Timeout, Temporary → retry; Authentication, InvalidRequest, BudgetExceeded → no retry. The classification drives automatic retry decisions with configurable backoff.

### Persistence

SQLite via `modernc.org/sqlite` (pure Go, no CGO). Tables: events, process_snapshots, workflow_runs, composed_agents, chat_messages, user_memory, memory_items, scheduled_jobs.
