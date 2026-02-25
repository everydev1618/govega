# Unified MCP Connections Page — COCA Spec

## Context

Vega's web dashboard currently has two separate pages for MCP configuration:

1. **MCP Servers page** (`/mcp`) — Read-only. Shows connected servers, their transport, and discovered tools. No ability to add, remove, or reconnect servers from the UI.

2. **Settings page** (`/settings`) — A raw key-value store for injecting values into tool templates via `{{.KEY}}`. Users must manually know which keys an MCP server needs (e.g. `POSTGRES_CONNECTION_STRING`) and create them here separately.

The disconnect between these two pages is the core UX problem. To add a Postgres MCP server today, a user must: (1) edit the `.vega.yaml` file to add the server definition, (2) go to Settings and manually create `POSTGRES_CONNECTION_STRING` with the right key name, (3) restart the server, (4) go to the MCP page to verify it connected. There's no guided flow, no discoverability, and no feedback loop.

**What exists on the backend:**
- `mcp.DefaultRegistry` — 10 well-known servers (filesystem, memory, github, postgres, sqlite, slack, brave-search, fetch, puppeteer, sequential-thinking), each with name, description, command, args, required/optional env vars.
- `tools.ConnectMCPServer(ctx, config)` — Already supports runtime connection without restart. Returns number of discovered tools.
- `tools.MCPServerConnected(name)` — Checks if a server is already connected.
- `tools.MCPServerStatuses()` — Returns status of all connected servers.
- Settings CRUD in SQLite with `refreshToolSettings()` to push changes to tools.

**What's missing on the backend:**
- No `POST /api/mcp/servers` endpoint to connect a server from the UI.
- No `GET /api/mcp/registry` endpoint to expose the catalog of known servers.
- No `DELETE /api/mcp/servers/{name}` to disconnect a server.
- Settings and MCP connections are not linked — no way to know which settings belong to which MCP server.

**Stack:** Go backend (net/http), React 19 + Vite + Tailwind frontend, SQLite storage.

## Outcome

A single **Connections** page (replacing both `/mcp` and `/settings`) where users can:

1. **Browse a catalog** of known MCP servers (from the registry) and see what each one does, what credentials it needs, and whether it's already connected.

2. **Connect a server in one flow**: pick from catalog (or enter custom server details) → fill in required credentials/env vars in labeled form fields → hit Connect → see it go live with discovered tools, all without editing YAML or restarting.

3. **See all connected servers** with their status, transport info, and discovered tools — the current MCP page info, but richer.

4. **Disconnect a server** from the UI.

5. **Manage settings** (the raw key-value store) in an "Advanced" collapsible section at the bottom for power users who need direct access to template variables.

**Who benefits:** Non-technical users setting up Vega for the first time. They should be able to connect a Postgres database or GitHub repo without reading docs or editing YAML.

**Success:** A user who has never seen Vega before can connect a Postgres MCP server by clicking "Postgres" from the catalog, pasting their connection string into a labeled field, clicking "Connect", and immediately seeing `postgres__query` appear in the discovered tools list.

## Constraints

- **No new dependencies** — use existing React + Tailwind component patterns from the codebase.
- **Backward compatible** — existing YAML-configured MCP servers still work exactly as before. The UI is additive, showing both YAML-configured and runtime-connected servers.
- **Settings stay useful** — the raw settings key-value store still works for dynamic tool templates (`{{.KEY}}`). It just moves to an advanced section rather than being the primary interface.
- **Registry is the source of truth for known servers** — don't duplicate server metadata in the frontend. Expose it via API.
- **Runtime connections are ephemeral** — connecting via UI calls `ConnectMCPServer` which works for the current session. Persisting across restarts is a future concern (out of scope). The UI should make this clear.
- **Non-goal: editing YAML from the UI** — we're not building a YAML editor. The catalog flow is for quick runtime connections.
- **Non-goal: per-agent tool assignment** — this page manages server connections, not which agents get which tools.
- **Navigation** — Replace the two sidebar entries ("MCP" and "Settings") with a single "Connections" entry. Add a second entry "Settings" that links to the advanced settings section (or keep it as a subsection/tab).

## Assertions

### Catalog browsing
- When the page loads, the user sees a grid of available MCP servers from the registry with name, description, and a visual indicator of whether each is already connected.
- When no servers are connected yet, the catalog section is prominent with a clear call-to-action.

### Connect flow — registry server
- When the user clicks a registry server (e.g. "Postgres"), a setup form appears showing labeled fields for each required env var (e.g. "Connection String" for `POSTGRES_CONNECTION_STRING`) and optional env vars.
- When the user fills in required fields and clicks "Connect", a `POST /api/mcp/servers` call is made with the server name and env values.
- When connection succeeds, the server appears in the "Connected" section with a green status badge and its discovered tools listed.
- When connection succeeds, the env var values are automatically saved as Settings (marked sensitive) so they persist and are available to `{{.KEY}}` templates.
- When connection fails (e.g. bad credentials, server not found), a clear error message is shown inline without losing the form state.

### Connect flow — custom server
- When the user clicks "Add Custom Server", they get a form with: name, transport (stdio/http/sse), command + args (for stdio), URL (for http/sse), headers, env vars, and timeout.
- When the user fills the form and clicks "Connect", the custom server is connected at runtime.

### Connected servers
- When a server is connected, its card shows: name, status badge (connected/disconnected), transport type, and a collapsible list of discovered tools with descriptions.
- When the user clicks "Disconnect" on a connected server, it is removed from the active connections.

### Settings (advanced)
- The raw key-value settings table remains accessible in a collapsible "Advanced Settings" section at the bottom.
- When the user creates/edits/deletes a setting, the behavior is identical to today (saves to SQLite, refreshes tool settings).

### API endpoints
- `GET /api/mcp/registry` returns the list of known servers with name, description, required env, optional env, and connected status.
- `POST /api/mcp/servers` accepts `{name, env, transport?, command?, args?, url?, headers?, timeout?}`. For registry servers, only `name` + `env` are needed. Connects the server and returns the list of discovered tools.
- `DELETE /api/mcp/servers/{name}` disconnects a connected server.

### Edge cases
- When a registry server requires env vars that are already saved as Settings, the form pre-fills those values.
- When a server with the same name is already connected, the Connect button is disabled or shows "Already Connected".
- Should NOT allow connecting two servers with the same name.
- Should NOT break existing YAML-configured servers — they appear in the connected list alongside runtime-connected ones.
