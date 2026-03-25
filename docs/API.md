# Vega API Reference

Base URL: `https://synkedup.v3ga.dev`

All endpoints return JSON. Errors use `{"error": "message"}`.

Multi-user support: pass `X-Auth-User: <user-id>` header to scope chat history and memory per user.

---

## Chat

### Send a message (non-streaming)

```
POST /api/agents/{name}/chat
```

```bash
curl -X POST https://synkedup.v3ga.dev/api/agents/hermes/chat \
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
POST /api/agents/{name}/chat/stream
```

Returns a Server-Sent Events stream. Each event has a `type` and JSON `data`.

```bash
curl -N -X POST https://synkedup.v3ga.dev/api/agents/hermes/chat/stream \
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
GET /api/agents/{name}/chat/stream
```

Replays all buffered events, then continues with live events. Returns `{"streaming": false}` if no active stream.

---

### Check stream status

```
GET /api/agents/{name}/chat/status
```

**Response:** `{"streaming": true}`

---

### Get chat history

```
GET /api/agents/{name}/chat
```

**Response:** Array of `{"role": "user"|"assistant", "content": "..."}`

---

### Clear chat history

```
DELETE /api/agents/{name}/chat
```

Clears persisted messages and resets the agent's in-memory process.

---

## Agents

### List agents

```
GET /api/agents
```

```bash
curl https://synkedup.v3ga.dev/api/agents
```

**Response:** Array of agent objects:

```json
[
  {
    "name": "hermes",
    "display_name": "Hermes",
    "model": "claude-sonnet-4-20250514",
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
POST /api/agents
```

```bash
curl -X POST https://synkedup.v3ga.dev/api/agents \
  -H "Content-Type: application/json" \
  -d '{
    "name": "writer",
    "model": "claude-sonnet-4-20250514",
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
  "model": "claude-sonnet-4-20250514",
  "tools": ["web_search", "delegate"],
  "process_id": "proc_xyz789"
}
```

---

### Update an agent

```
PUT /api/agents/{name}
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
DELETE /api/agents/{name}
```

---

### Export agent as template

```
GET /api/agents/{name}/template
```

Returns a portable JSON template that can be imported on another instance.

---

### Import agent from template

```
POST /api/agents/import
```

Body is the template JSON from the export endpoint. Required fields: `name`, `model`, `system`.

---

## Channels

Slack-style group conversations where multiple agents collaborate.

### List channels

```
GET /api/channels
```

---

### Create a channel

```
POST /api/channels
```

| Field         | Type     | Required | Description          |
|---------------|----------|----------|----------------------|
| `name`        | string   | yes      | Channel name         |
| `description` | string   | no       | Channel description  |
| `team`        | string[] | no       | Agent names on team  |

---

### Get a channel

```
GET /api/channels/{name}
```

---

### Delete a channel

```
DELETE /api/channels/{name}
```

---

### Update channel team

```
PUT /api/channels/{name}/team
```

Body: `{"team": ["agent1", "agent2"]}`

---

### List channel messages

```
GET /api/channels/{name}/messages?limit=100
```

---

### List thread replies

```
GET /api/channels/{name}/messages/{id}/thread
```

---

### Post to a channel (non-streaming)

```
POST /api/channels/{name}/messages
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
POST /api/channels/{name}/stream
```

Same request body as non-streaming. Returns SSE with channel events:
`channel.message`, `channel.typing`, `channel.text_delta`, `channel.tool_start`, `channel.tool_end`, `channel.thread_reply`, `channel.error`, `channel.done`.

---

### Reconnect to channel stream

```
GET /api/channels/{name}/stream
```

---

## Processes

### List all processes

```
GET /api/processes
```

---

### Get process detail

```
GET /api/processes/{id}
```

Includes full conversation `messages` array.

---

### Kill a process

```
DELETE /api/processes/{id}
```

---

## Workflows

### List workflows

```
GET /api/workflows
```

---

### Run a workflow

```
POST /api/workflows/{name}/run
```

Body: `{"inputs": {"key": "value"}}`

Returns `202 Accepted` with `{"run_id": "abc12345", "status": "running"}`. Execution is async.

---

## Memory

### Get agent memory

```
GET /api/agents/{name}/memory?user=default
```

Returns memory layers (profile, topics, notes) for the given user-agent pair.

---

### Delete agent memory

```
DELETE /api/agents/{name}/memory?user=default
```

---

## MCP Servers

### List connected servers

```
GET /api/mcp/servers
```

---

### List MCP registry

```
GET /api/mcp/registry
```

Returns available integrations with required/optional env keys and connection status.

---

### Connect a server

```
POST /api/mcp/servers
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
GET /api/mcp/servers/{name}/config
```

---

### Update a server

```
PUT /api/mcp/servers/{name}
```

Same body as connect. Disconnects, applies changes, reconnects.

---

### Refresh a server

```
POST /api/mcp/servers/{name}/refresh
```

Disconnects and reconnects using persisted config.

---

### Duplicate a server

```
POST /api/mcp/servers/{name}/duplicate
```

Body: `{"new_name": "my-copy"}`

---

### Enable/disable a server

```
PUT /api/mcp/servers/{name}/disable
```

Body: `{"disabled": true}` or `{"disabled": false}`

---

### Disconnect a server

```
DELETE /api/mcp/servers/{name}
```

---

## Files

### List directory

```
GET /api/files?path=subdir
```

Returns array of `FileEntry` objects. Omit `path` for workspace root.

---

### Read file

```
GET /api/files/read?path=report.md
```

Returns content as UTF-8 text or base64 (for binary). Max 10 MB.

---

### Delete file

```
DELETE /api/files?path=old-file.txt
```

---

### List file metadata

```
GET /api/files/metadata?agent=writer
```

Returns files written by agents, with the list of distinct agent names.

---

## Schedules

### List schedules

```
GET /api/schedules
```

---

### Delete a schedule

```
DELETE /api/schedules/{name}
```

---

### Toggle a schedule

```
PUT /api/schedules/{name}
```

Body: `{"enabled": true}`

---

## Inbox

Agent-posted messages to Hermes's inbox.

### List inbox items

```
GET /api/inbox?status=pending&limit=50
```

---

### Clear resolved items

```
DELETE /api/inbox/resolved
```

Returns `{"deleted": 5}`.

---

## Settings

Key-value configuration store. Sensitive values are masked in list responses.

### List settings

```
GET /api/settings
```

---

### Create/update a setting

```
PUT /api/settings
```

Body: `{"key": "OPENAI_API_KEY", "value": "sk-...", "sensitive": true}`

---

### Delete a setting

```
DELETE /api/settings/{key}
```

---

## Prompt History

Prompts sent to Hermes, preserved across resets.

### List prompt history

```
GET /api/prompt-history?limit=100
```

---

### Search prompt history

```
GET /api/prompt-history/search?q=landing+page&limit=50
```

---

### Delete a prompt history entry

```
DELETE /api/prompt-history/{id}
```

---

## System

### Get company info

```
GET /api/company
```

---

### Get system stats

```
GET /api/stats
```

Returns aggregate token counts, costs, process counts, and uptime.

---

### Get spawn tree

```
GET /api/spawn-tree
```

Hierarchical view of parent-child process relationships.

---

### Global SSE event stream

```
GET /api/events
```

Real-time Server-Sent Events for process lifecycle, agent status, and workflow completions. Heartbeat every 30 seconds.

---

### Full system reset

```
POST /api/reset
```

Kills all processes, disconnects MCP servers, clears database (except prompt history), and removes workspace files.

---

## Population

Registry of installable personas, skills, and profiles.

### Search population

```
GET /api/population/search?q=writer&kind=persona
```

---

### Get population item info

```
GET /api/population/info/{kind}/{name}
```

Kind: `persona`, `skill`, or `profile`.

---

### Install a population item

```
POST /api/population/install
```

Body: `{"name": "@writer"}` (personas prefixed with `@`, profiles with `+`).

---

### List installed items

```
GET /api/population/installed?kind=persona
```

---

## Configuration

### Get current config

```
GET /api/config
```

Returns the running configuration: team name, all agents (with source: `yaml`, `composed`, or `builtin`), connected MCP servers, and settings.

---

### Upload a team YAML

```
POST /api/config/upload
```

Upload a `.vega.yaml` file to create or update agents and connect MCP servers at runtime. Changes persist across restarts.

```bash
curl -X POST https://synkedup.v3ga.dev/api/config/upload \
  -F "file=@landscaping-team.vega.yaml"
```

**Response:**

```json
{
  "name": "Landscaping Backoffice",
  "agents_created": ["estimator", "scheduler"],
  "agents_updated": ["bookkeeper"],
  "agents_skipped": [],
  "mcp_connected": ["synkedup"],
  "errors": []
}
```
