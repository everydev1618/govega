# Playing with Vega locally

A hands-on tour of Vega on your own machine: boot the server, meet the
meta-agents, build a team, break things on purpose, and watch the
durability features catch them. Every step here is verified against the
real binary — total LLM cost for the whole tour is a few cents.

## Boot

```bash
export ANTHROPIC_API_KEY=sk-ant-...   # the only required config
go build -o bin/vega ./cmd/vega       # frontend is pre-built and embedded
./bin/vega serve --addr 127.0.0.1:4200 --db ~/vega-playground.db
```

Open **http://127.0.0.1:4200**.

Notes:

- `--db` points at an isolated playground database so you can experiment
  (and delete) freely. Omit it to use the default `~/.vega` database.
- Without `--addr`, Vega binds loopback on an auto-assigned port and
  prints it — a security default, not a bug. Use `0.0.0.0:PORT` only
  when you mean to expose it to your network.
- No YAML file means you get the default document: the **Iris**
  orchestrator (chief of staff) and **Hera** (agent builder) are
  injected automatically. Pass a YAML file to bring your own agents:
  `./bin/vega serve examples/dev-team.vega.yaml ...`
- Agents put links to their work (`…/workspace/…`, deployed apps) in
  chat, and need to know this server's **public** address to do it. On
  loopback that is just `http://localhost:PORT`. On an instance bound
  to `0.0.0.0` it is not: Vega learns the right value from the hostname
  you open the dashboard with, and asks on first run if it can't. Pin
  it with `--public-url http://vega.const` (or `PUBLIC_URL`) when the
  address people use isn't the one they'd browse to — a reverse proxy
  in front, say. See `serve/public_url.go`.

## The tour

Each stop exercises a different slice of the machinery.

### 1. Chat with Iris

Open the chat and say hello. Responses stream over SSE with live
tool-call chips — when Iris reads a file or checks status, you see the
tool start/finish in the timeline. Behind it: the typed content-block
pipeline (thinking → text → tool_use → tool_result) round-tripping to
the Anthropic API.

### 2. Have Hera build you a team

Ask Iris something like:

> Create a researcher agent and a writer agent. Then have the researcher
> gather three facts about the James Webb telescope and hand them to the
> writer for a short paragraph.

Iris routes the creation to Hera (agents persist to the database —
they'll survive restarts), then dispatches work with `send_to_agent`.
Dispatches are async: completion reports land in Iris's inbox and she
pokes you with the outcome. Each new agent gets its own chat page —
watch the delegated work happen in `/chat/<agent>`.

Two guardrails you can try to trip:

- **Delegation depth/cycles**: an agent that tries to delegate back to
  someone already in the delegation chain gets a refusal as its tool
  result (and a depth cap of 8 hops, `VEGA_MAX_DELEGATION_DEPTH` to
  change). You'll see it apologize and do the work itself.
- **Meta-agent escalation**: composed agents can't invoke Hera/Iris via
  delegation — only you can.

### 3. Connect an MCP server

Connections page → pick something from the registry (or connect a
custom stdio command). Its tools become available to all agents.
Persistence check: **restart the server** — the connection comes back
on its own (it's in the `mcp_servers` table). Disable it, restart
again — it stays down but remains listed. MCP subprocesses run with a
stripped environment: your `ANTHROPIC_API_KEY` never reaches them.

### 4. Run a workflow — then kill it

Boot with a YAML that defines workflows:

```bash
./bin/vega serve examples/dev-team.vega.yaml --addr 127.0.0.1:4200 --db ~/vega-playground.db
```

Fire a workflow from the Workflows page (or
`POST /api/v1/workflows/<name>/run`). As it executes, per-step
checkpoints are written to the run row. Now `Ctrl-C` the server mid-run
and boot it again: the orphaned run is marked **interrupted**, with the
checkpoint showing exactly which step it died in — no forever-"running"
ghosts.

### 5. Poke the REST API directly

Everything the UI does is plain HTTP:

```bash
curl -s http://127.0.0.1:4200/api/v1/agents | jq '.[].name'

curl -s -X POST http://127.0.0.1:4200/api/v1/agents/iris/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"What agents exist right now?"}' | jq -r .response

# Streaming variant (SSE):
curl -N -X POST http://127.0.0.1:4200/api/v1/agents/iris/chat/stream \
  -H 'Content-Type: application/json' \
  -d '{"message":"hello"}'
```

The full surface is in `docs/API.md` / `docs/openapi.yaml`.

## Other ways in

| Mode | Command | What it is |
|---|---|---|
| REPL | `./bin/vega repl examples/simple-agent.vega.yaml` | Terminal-only conversation with a YAML-defined agent |
| One-shot | `./bin/vega run examples/code-review.vega.yaml --workflow <name>` | Execute a workflow and exit |
| Validate | `./bin/vega validate examples/dev-team.vega.yaml` | Parse-check a YAML document |
| Go library | see README → Quick Start | Orchestrator/Agent/Process API, no server |

The `examples/` directory is a graded set: `simple-agent` →
`tools-demo` → `dev-team` → `control-flow` → `supervision-demo`.

## Optional extras

- **Telegram**: set `TELEGRAM_BOT_TOKEN` before `serve` and your
  orchestrator answers in Telegram — same agent, same memory, same
  timeline as the web chat. Restrict who may talk to it via the bot's
  `allowed_users` setting (Integrations page / bot config).
- **Memory**: tell Iris something about yourself, restart the server,
  ask her what she knows. Active (`remember`/`recall`) and passive
  (post-exchange extraction) memory both feed the same store.
- **Budgets**: set a spend cap on an agent (Agents page) and watch turns
  get refused once it trips.

## Cleaning up

```bash
kill %1                        # or the pid you launched
rm ~/vega-playground.db*       # the playground state (incl. -wal/-shm)
```

Nothing else is written outside the `--db` path.
