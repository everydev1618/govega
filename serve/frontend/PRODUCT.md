# PRODUCT.md — Vega dashboard

## Product

The embedded React dashboard inside the Vega Go binary. When you `vega serve`, this is what loads at `localhost:8080`. It's where the user talks to agents (Iris, Hera, specialists), watches processes, monitors channels, manages workflows and schedules, and explores the supervision tree.

## Register

**Product.** Design serves the task. Familiar dashboard patterns (top-bar, side-nav, list rows) are features, not constraints to escape. The job is to disappear into the work.

## Users

Same audience as v3ga.dev — skeptical infrastructure-leaning engineers — but in a different mode. Here they're not deciding whether to try Vega; they're using it. Long sessions, frequent switching between dashboard and editor, lots of code/log reading. Need density, not narrative.

## Voice (three words)

**Calm. Direct. Familiar.**

Same engineering taste as the brand, expressed differently. Where the marketing site is *mechanical, methodical, opinionated*, the product is the quiet version of those: the same person, focused on the task, not the pitch.

## Anti-references

- **shadcn-template dashboards** — soft rounded cards on dark zinc, primary blue everywhere, decorative motion. The previous version of this dashboard was textbook.
- **Linear/Notion-inspired colorlessness** — the cream/oxblood identity is the point; don't drift to neutral gray.
- **Slack-clone affordances pushed too far** — we keep the structural pattern (DMs / channels / activity), not the visual chrome.

## Strategic principles

1. **Brand-product coherence.** Same palette and type as v3ga.dev. Rare and credible.
2. **Density over breathing room.** Product needs more rows on screen than the marketing site needs.
3. **Restrained color.** Oxblood is for primary actions, current selection, and state indicators only. Never decorative.
4. **State, not decoration.** Every animation must convey a state change. No constellations, no breathing glows, no orbital busy indicators.
5. **Mono for system data.** PIDs, timestamps, costs, IDs, log lines — Geist Mono. Everything else — Switzer.

## Surfaces (current scope of Phase 1)

- `Layout.tsx` — sidebar + main shell + Visualize FAB
- `Chat.tsx` + `components/chat/*` — DM with an agent (the most-used surface)
- `Overview.tsx` — landing page with stats + getting-started

## Inheriting Phase 1 palette (polish in later rounds)

InboxView, Tasks, Memory, Files, Population, Workflows, Schedules, Events, ProcessExplorer, SpawnTree, Visualize, Connections, Costs, Settings, MCPServers, ChannelView, WorkflowLauncher, AgentRegistry.

## Tech

- React 19 + Vite 6 + TypeScript
- Tailwind 3.4 (semantic tokens preserved from shadcn baseline, retuned to cream/oxblood)
- d3 for graphs (SpawnTree, Visualize, AgentOrgChart)
- react-router-dom 7
- Embedded into the Go binary via `serve/embed.go`
