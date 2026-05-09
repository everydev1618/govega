# Vega API Reference

Base URL: `https://your-vega.example.com`

All endpoints return JSON. Errors use `{"error": "message"}`.

Multi-user support: pass `X-Auth-User: <user-id>` header to scope chat history and memory per user.

---

## Chat

### Send a message (non-streaming)

```
POST /api/v1/agents/{name}/chat
```

```bash
curl -X POST https://your-vega.example.com/api/v1/agents/iris/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "What can you help me with?"}'
```

**Request body:**

| Field     | Type   | Required | Description    |
|-----------|--------|----------|----------------|
| `message` | string | yes      | User message   |

**Response:** `{"response": "I can help you with..."}`

---

### Send a message (streaming)

```
POST /api/v1/agents/{name}/chat/stream
```

Returns a Server-Sent Events stream. Each event has a `type` and JSON `data`.

```bash
curl -N -X POST https://your-vega.example.com/api/v1/agents/iris/chat/stream \
  -H "Content-Type: application/json" \
  -d '{"message": "Build me a landing page"}'
```

**SSE event types:**

| Event         | Key fields                                    | Description                      |
|---------------|-----------------------------------------------|----------------------------------|
| `text_delta`  | `delta`                                       | Incremental text chunk           |
| `tool_start`  | `tool_name`, `tool_call_id`, `arguments`      | Tool invocation started          |
| `tool_end`    | `tool_name`, `tool_call_id`, `result`, `duration_ms` | Tool completed            |
| `error`       | `error`                                       | Error message                    |
| `done`        | `metrics.input_tokens`, `metrics.output_tokens`, `metrics.cost_usd`, `metrics.duration_ms` | Stream finished |

---

### Reconnect to an active stream

```
GET /api/v1/agents/{name}/chat/stream
```

Replays all buffered events, then continues with live events. Returns `{"streaming": false}` if no active stream.

---

### Check stream status

```
GET /api/v1/agents/{name}/chat/status
```

**Response:** `{"streaming": true}`

---

### Get chat history

```
GET /api/v1/agents/{name}/chat
```

**Response:** Array of `{"role": "user"|"assistant", "content": "..."}`

---

### Clear chat history

```
DELETE /api/v1/agents/{name}/chat
```

Clears persisted messages and resets the agent's in-memory process.

---

## Agents

### List agents

```
GET /api/v1/agents
```

```bash
curl https://your-vega.example.com/api/v1/agents
```

**Response:** Array of agent objects:

```json
[
  {
    "name": "iris",
    "display_name": "Iris",
    "model": "claude-sonnet-4-6",
    "tools": ["remember", "recall", "delegate"],
    "process_id": "proc_abc123",
    "process_status": "running",
    "source": "composed"
  }
]
```

---

### Create an agent

```
POST /api/v1/agents
```

```bash
curl -X POST https://your-vega.example.com/api/v1/agents \
  -H "Content-Type: application/json" \
  -d '{
    "name": "writer",
    "model": "claude-sonnet-4-6",
    "system": "You are a creative writing assistant.",
    "skills": ["web-search"],
    "team": ["researcher"]
  }'
```

**Request body:**

| Field         | Type     | Required | Description                                |
|---------------|----------|----------|--------------------------------------------|
| `name`        | string   | yes      | Unique agent name                          |
| `model`       | string   | yes      | LLM model identifier                      |
| `system`      | string   | no       | System prompt (overrides persona)          |
| `persona`     | string   | no       | Population persona name                    |
| `skills`      | string[] | no       | Population skills to install               |
| `team`        | string[] | no       | Agent names this agent can delegate to     |
| `temperature` | number   | no       | Sampling temperature                       |

**Response (201):**

```json
{
  "name": "writer",
  "model": "claude-sonnet-4-6",
  "tools": ["web_search", "delegate"],
  "process_id": "proc_xyz789"
}
```

---

### Update an agent

```
PUT /api/v1/agents/{name}
```

All fields optional. Only provided fields are updated.

| Field         | Type     | Description              |
|---------------|----------|--------------------------|
| `name`        | string   | Rename the agent         |
| `model`       | string   | Change model             |
| `system`      | string   | Change system prompt     |
| `team`        | string[] | Change team members      |
| `temperature` | number   | Change temperature       |

---

### Delete an agent

```
DELETE /api/v1/agents/{name}
```

---

### Export agent as template

```
GET /api/v1/agents/{name}/template
```

Returns a portable JSON template that can be imported on another instance.

---

### Import agent from template

```
POST /api/v1/agents/import
```

Body is the template JSON from the export endpoint. Required fields: `name`, `model`, `system`.

---

## Channels

Slack-style group conversations where multiple agents collaborate.

### List channels

```
GET /api/v1/channels
```

---

### Create a channel

```
POST /api/v1/channels
```

| Field         | Type     | Required | Description          |
|---------------|----------|----------|----------------------|
| `name`        | string   | yes      | Channel name         |
| `description` | string   | no       | Channel description  |
| `team`        | string[] | no       | Agent names on team  |

---

### Get a channel

```
GET /api/v1/channels/{name}
```

---

### Delete a channel

```
DELETE /api/v1/channels/{name}
```

---

### Update channel team

```
PUT /api/v1/channels/{name}/team
```

Body: `{"team": ["agent1", "agent2"]}`

---

### List channel messages

```
GET /api/v1/channels/{name}/messages?limit=100
```

---

### List thread replies

```
GET /api/v1/channels/{name}/messages/{id}/thread
```

---

### Post to a channel (non-streaming)

```
POST /api/v1/channels/{name}/messages
```

| Field       | Type   | Required | Description                        |
|-------------|--------|----------|------------------------------------|
| `message`   | string | yes      | Message content                    |
| `thread_id` | int64  | no       | Reply in thread (parent message ID)|
| `agent`     | string | no       | Target specific agent              |

Agent response is async. Returns `{"message_id": 1, "thread_id": 1}`.

---

### Post to a channel (streaming)

```
POST /api/v1/channels/{name}/stream
```

Same request body as non-streaming. Returns SSE with channel events:
`channel.message`, `channel.typing`, `channel.text_delta`, `channel.tool_start`, `channel.tool_end`, `channel.thread_reply`, `channel.error`, `channel.done`.

---

### Reconnect to channel stream

```
GET /api/v1/channels/{name}/stream
```

---

## Processes

### List all processes

```
GET /api/v1/processes
```

---

### Get process detail

```
GET /api/v1/processes/{id}
```

Includes full conversation `messages` array.

---

### Kill a process

```
DELETE /api/v1/processes/{id}
```

---

## Workflows

### List workflows

```
GET /api/v1/workflows
```

---

### Run a workflow

```
POST /api/v1/workflows/{name}/run
```

Body: `{"inputs": {"key": "value"}}`

Returns `202 Accepted` with `{"run_id": "abc12345", "status": "running"}`. Execution is async.

---

## Memory

### Get agent memory

```
GET /api/v1/agents/{name}/memory?user=default
```

Returns memory layers (profile, topics, notes) for the given user-agent pair.

---

### Delete agent memory

```
DELETE /api/v1/agents/{name}/memory?user=default
```

---

## MCP Servers

### List connected servers

```
GET /api/v1/mcp/servers
```

---

### List MCP registry

```
GET /api/v1/mcp/registry
```

Returns available integrations with required/optional env keys and connection status.

---

### Connect a server

```
POST /api/v1/mcp/servers
```

| Field       | Type              | Required | Description                    |
|-------------|-------------------|----------|--------------------------------|
| `name`      | string            | yes      | Server name (or registry name) |
| `env`       | map[string]string | no       | Environment variables          |
| `transport` | string            | no       | `stdio`, `http`, or `sse`     |
| `command`   | string            | no       | Command for stdio transport    |
| `args`      | string[]          | no       | Command arguments              |
| `url`       | string            | no       | URL for http/sse transport     |
| `headers`   | map[string]string | no       | HTTP headers                   |
| `timeout`   | integer           | no       | Timeout in seconds             |

---

### Get server config

```
GET /api/v1/mcp/servers/{name}/config
```

---

### Update a server

```
PUT /api/v1/mcp/servers/{name}
```

Same body as connect. Disconnects, applies changes, reconnects.

---

### Refresh a server

```
POST /api/v1/mcp/servers/{name}/refresh
```

Disconnects and reconnects using persisted config.

---

### Duplicate a server

```
POST /api/v1/mcp/servers/{name}/duplicate
```

Body: `{"new_name": "my-copy"}`

---

### Enable/disable a server

```
PUT /api/v1/mcp/servers/{name}/disable
```

Body: `{"disabled": true}` or `{"disabled": false}`

---

### Disconnect a server

```
DELETE /api/v1/mcp/servers/{name}
```

---

## Files

### List directory

```
GET /api/v1/files?path=subdir
```

Returns array of `FileEntry` objects. Omit `path` for workspace root.

---

### Read file

```
GET /api/v1/files/read?path=report.md
```

Returns content as UTF-8 text or base64 (for binary). Max 10 MB.

---

### Delete file

```
DELETE /api/v1/files?path=old-file.txt
```

---

### List file metadata

```
GET /api/v1/files/metadata?agent=writer
```

Returns files written by agents, with the list of distinct agent names.

---

## Schedules

### List schedules

```
GET /api/v1/schedules
```

---

### Delete a schedule

```
DELETE /api/v1/schedules/{name}
```

---

### Toggle a schedule

```
PUT /api/v1/schedules/{name}
```

Body: `{"enabled": true}`

---

## Inbox

Agent-posted messages to Iris's inbox.

### List inbox items

```
GET /api/v1/inbox?status=pending&limit=50
```

---

### Clear resolved items

```
DELETE /api/v1/inbox/resolved
```

Returns `{"deleted": 5}`.

---

## Tasks

Kanban-style work items that live independently of any single agent run. A task has a status (`todo` / `doing` / `blocked` / `done` / `canceled`), a priority, an optional assignee, and an activity feed of comments. One task can be fulfilled by zero or many processes (linked via `task_processes`). Tasks are how humans (and Iris) track work that spans more than one immediate exchange.

**Inbox vs Tasks:** Inbox items are push-from-agent (questions, completion pings) that Iris triages and resolves. Tasks are persistent work units that move through columns. They're orthogonal — an inbox triage may file a task; a task in progress may post questions to inbox.

### Task lifecycle

```
                       human creates                  agent claims
[unassigned: todo]  ─────────────────►  [assigned: todo]  ─────────►  [doing]  ──►  [done]
        ▲                                      │                        │
        │ orchestrator                         │ blocker                │
        └──── assign_task ───  Iris  ────►     ▼                        │
                                          [blocked] ──── unblocked ─────┘
```

### List tasks

```
GET /api/v1/tasks
```

Optional query params:

- `status=todo,doing,blocked` — CSV of statuses to include
- `assignee=drew,blake` — CSV of agent names
- `tag=ui,backend` — matches tasks with any of these tags
- `limit`, `offset` — pagination

Returns an array of `Task`, newest-updated first.

### Create a task

```
POST /api/v1/tasks
```

```json
{
  "title": "Ship the Q3 report",
  "description": "Compile data from all three product lines.",
  "priority": "high",
  "assignee": "blake",
  "due_at": "2026-05-20T17:00:00Z"
}
```

`title` is the only required field. Status defaults to `todo`, priority to `normal`. Leave `assignee` empty (or omit) to put the task in Iris's routing queue. Returns the created `Task` with server-assigned `id`, `created_at`, `updated_at`.

### Get a task (with comments and linked processes)

```
GET /api/v1/tasks/{id}
```

Returns a `TaskDetail`: the task plus its `comments` array (chronological) and `processes` array (process IDs linked to this task — oldest-linked first). Use the linked process IDs against `GET /api/v1/processes/{id}` to inspect what each run actually did.

### Update a task (partial)

```
PATCH /api/v1/tasks/{id}
```

```json
{ "status": "doing" }
```

Any combination of fields: `title`, `description`, `status`, `priority`, `assignee`, `tags`, `due_at`. Status must be one of the enum values; an unknown status returns `400`. `updated_at` advances on every successful update.

### Delete a task

```
DELETE /api/v1/tasks/{id}
```

Cascades: comments and process links for this task are removed in the same transaction.

### Add a comment

```
POST /api/v1/tasks/{id}/comments
```

```json
{ "content": "Halfway done — waiting on legal", "author": "blake" }
```

`content` is required. `author` defaults to `"user"` if omitted. Bumps the task's `updated_at` so the activity reorders the list. Returns the created `TaskComment`.

### Link a process to a task

```
POST /api/v1/tasks/{id}/processes
```

```json
{ "process_id": "abc12345" }
```

Idempotent — re-linking the same process is a no-op, not an error. Used to attach an agent run to the task it was fulfilling. **Most agents don't need to call this directly** — the `claim_task` agent tool auto-links the calling process. This endpoint is for external integrations or manual stitching.

### Working with tasks from inside an agent

In addition to the REST endpoints above, agents can interact with the kanban via these registered tools (defined in `dsl/task_tools.go`):

- `list_my_tasks(status?, limit?)` — see what's assigned to me
- `list_unassigned_tasks(limit?)` — orchestrator's routing queue
- `create_task(title, description?, priority?, assignee?)` — file new work
- `assign_task(id, assignee)` — orchestrator routes to a specialist
- `claim_task(id)` — set assignee=me, status=doing, **and auto-link my running process to the task**
- `update_task_status(id, status)` — move across columns
- `comment_on_task(id, content)` — post to the activity feed (author = calling agent)

By default, only Iris is taught (in her system prompt) to actively triage `list_unassigned_tasks` on heartbeat. Worker agents have the tools available but use them when their prompt directs (typically `claim_task` → work → `comment_on_task` → `update_task_status('done')`).

---

## Settings

Key-value configuration store. Sensitive values are masked in list responses.

### List settings

```
GET /api/v1/settings
```

---

### Create/update a setting

```
PUT /api/v1/settings
```

Body: `{"key": "OPENAI_API_KEY", "value": "sk-...", "sensitive": true}`

---

### Delete a setting

```
DELETE /api/v1/settings/{key}
```

---

## Prompt History

Prompts sent to Iris, preserved across resets.

### List prompt history

```
GET /api/v1/prompt-history?limit=100
```

---

### Search prompt history

```
GET /api/v1/prompt-history/search?q=landing+page&limit=50
```

---

### Delete a prompt history entry

```
DELETE /api/v1/prompt-history/{id}
```

---

## System

### Get company info

```
GET /api/v1/company
```

---

### Get system stats

```
GET /api/v1/stats
```

Returns aggregate token counts, costs, process counts, and uptime.

---

### Get spawn tree

```
GET /api/v1/spawn-tree
```

Hierarchical view of parent-child process relationships.

---

### Global SSE event stream

```
GET /api/v1/events
```

Real-time Server-Sent Events for process lifecycle, agent status, and workflow completions. Heartbeat every 30 seconds.

---

### Full system reset

```
POST /api/v1/reset
```

Kills all processes, disconnects MCP servers, clears database (except prompt history), and removes workspace files.

---

## Population

Registry of installable personas, skills, and profiles.

### Search population

```
GET /api/v1/population/search?q=writer&kind=persona
```

---

### Get population item info

```
GET /api/v1/population/info/{kind}/{name}
```

Kind: `persona`, `skill`, or `profile`.

---

### Install a population item

```
POST /api/v1/population/install
```

Body: `{"name": "@writer"}` (personas prefixed with `@`, profiles with `+`).

---

### List installed items

```
GET /api/v1/population/installed?kind=persona
```

---

## Configuration

### Get current config

```
GET /api/v1/config
```

Returns the running configuration: team name, all agents (with source: `yaml`, `composed`, or `builtin`), connected MCP servers, and settings.

---

### Upload a team YAML

```
POST /api/v1/config/upload
```

Upload a `.vega.yaml` file to create or update agents and connect MCP servers at runtime. Changes persist across restarts.

```bash
curl -X POST https://your-vega.example.com/api/v1/config/upload \
  -F "file=@my-team.vega.yaml"
```

**Response:**

```json
{
  "name": "My Team",
  "agents_created": ["researcher", "writer"],
  "agents_updated": ["editor"],
  "agents_skipped": [],
  "mcp_connected": ["gmail"],
  "errors": []
}
```
