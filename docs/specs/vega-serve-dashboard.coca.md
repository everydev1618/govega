# `vega serve` — Web Dashboard & API — COCA Spec

## Context

- **Govega** is a fault-tolerant AI agent orchestration framework in Go with Erlang-style supervision trees
- CLI today has `run`, `validate`, `repl` commands; all configuration via `.vega.yaml` files
- The orchestrator tracks rich runtime state internally — processes, spawn trees, metrics, cost, supervision health, events — but none of it is observable outside of Go code
- Event bus infrastructure exists (file-based and HTTP callback publishing) but has no consumer today
- No HTTP API or web server exists in the project
- Primary users: **operators** running agent workflows in production, and **developers** building on top of Vega
- Single-machine deployment for now
- No observability workarounds exist — operators are flying blind once agents are running

## Outcome

`vega serve` starts an HTTP server that provides both a web dashboard and a REST API. It loads the same `.vega.yaml` as other commands and runs a live orchestrator.

### What an operator sees

- **Overview page** — at-a-glance health: how many agents defined, processes running/completed/failed, total cost burned, active supervision issues. The "is everything okay?" screen.
- **Process explorer** — live table of all processes with status, agent type, cost, tokens, duration. Click into any process to see its conversation history, tool calls, and metrics.
- **Spawn tree view** — visual tree of parent/child process relationships. See the full hierarchy, color-coded by status. Understand how work cascaded through the system.
- **Event stream** — real-time feed of system events (started, progress, completed, failed, heartbeat). Filterable. The "what just happened?" view.
- **Agent registry** — browse all defined agents, their configs, tools, supervision strategies, budgets. The "what can this system do?" reference.
- **MCP servers** — see connected MCP servers, their status, and the tools they provide.
- **Workflow launcher** — pick a workflow, fill in inputs (form auto-generated from the workflow's input schema), hit run, watch it execute.
- **Cost dashboard** — budget usage per agent, per process, aggregate. Burn rate over time.

### What a developer gets from the API

- REST endpoints mirroring the above: list processes, get process detail, spawn, send messages, view events via SSE, list agents, etc.
- Not required for library users — it's an optional layer on top of the same Go API they already use.

### What success looks like

An operator can run `vega serve team.vega.yaml`, open a browser, and within seconds understand what their agent system is doing, what it has done, what it costs, and intervene if needed. A developer can hit the API to build custom integrations without writing Go.

## Constraints

- **New subcommand**: `vega serve` added alongside existing `run`, `validate`, `repl` in `cmd/vega/main.go`
- **Frontend**: React + Tailwind + shadcn/ui, compiled to static assets, embedded in Go binary via `go:embed`
- **Real-time**: SSE (Server-Sent Events) for live process updates and event streaming
- **Persistence**: SQLite (pure Go driver, `modernc.org/sqlite`) for process history, events, metrics. Survives restarts.
- **Auth**: None. Localhost only for v1.
- **Scale target**: ~10 agents, ~50 concurrent processes. Not optimized beyond that.
- **Single binary**: `go build` produces one binary with frontend assets included. No external dependencies to deploy.
- **API style**: REST + SSE. JSON request/response bodies.
- **Non-goals**:
  - Multi-machine / distributed orchestrator support
  - User management / multi-tenancy
  - Visual drag-and-drop workflow editor
  - Mobile-responsive design

## Assertions

### Startup & Basics

- When I run `vega serve team.vega.yaml`, the server starts and prints the URL (e.g. `http://localhost:3000`)
- When I open that URL in a browser, I see the overview dashboard within 2 seconds
- When I run `vega serve` with `--port 8080`, it uses that port
- When I run `vega serve` with an invalid `.vega.yaml`, it exits with a clear error before starting the server

### Overview Page

- When agents are defined in the YAML, the overview shows the count and a summary
- When processes are running, I see active count, total cost, and any supervision alerts
- When a process fails, the overview reflects the failure within 1 second

### Process Explorer

- When I view the process list, I see status, agent name, cost, tokens, duration for each
- When I click a process, I see its full conversation history (user messages, assistant responses, tool calls and results)
- When a process is running, its row updates live without refreshing the page
- When I click "kill" on a running process, it terminates

### Spawn Tree

- When a workflow spawns child processes, I see the tree structure with parent-child edges
- When a process completes or fails, its node color updates in real time
- When I click a node, it navigates to that process's detail view

### Event Stream

- When events fire, they appear in the stream within 1 second
- When I filter by event type (e.g. only "Failed"), only matching events show
- When I scroll up, the stream pauses auto-scroll so I can read history

### Workflow Launcher

- When I select a workflow, I see a form with inputs matching the workflow's input schema
- When I fill in inputs and hit "Run", the workflow starts and I can watch it in the process explorer
- When a required input is missing, the form shows a validation error before submission

### Agent Registry

- When I view an agent, I see its model, system prompt, tools, budget, supervision config, retry policy
- When an agent has MCP tools, I see which MCP server they come from

### MCP Servers

- When MCP servers are configured, I see their name, transport type, and connection status
- When I view an MCP server, I see the list of tools it provides

### Cost Dashboard

- When processes have run, I see cost broken down per agent and per process
- When a budget threshold is approached (>80%), it's visually flagged

### API

- `GET /api/processes` — returns JSON listing all processes
- `GET /api/processes/:id` — returns full process detail including conversation history
- `GET /api/events` (with `Accept: text/event-stream`) — returns SSE stream of live events
- `POST /api/workflows/:name/run` (with JSON inputs) — starts a workflow, returns process ID
- `GET /api/agents` — returns all agent definitions
- `GET /api/mcp/servers` — returns MCP server status and tools
- `GET /api/stats` — returns aggregate cost, token, and process counts

### Persistence

- When I stop and restart `vega serve`, previous process history and events are still visible
- When I query completed processes from a previous session, conversation history is intact

### Should NOT

- Should NOT require any external dependencies to run (no separate database, no Node.js runtime)
- Should NOT block or slow down agent execution (dashboard is observational, not in the hot path)
- Should NOT expose the server beyond localhost by default
