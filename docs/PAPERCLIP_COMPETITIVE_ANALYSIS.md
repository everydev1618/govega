# Paperclip vs Vega: Competitive Analysis

> Analysis date: 2026-04-02
> Source: https://github.com/paperclipai/paperclip (v0.3.1, MIT license)

## Overview

| | **Vega** | **Paperclip** |
|---|---|---|
| **Core metaphor** | Erlang/OTP — agents as processes with supervision trees | Company — agents as employees with org charts |
| **Language** | Go (core) + TypeScript (apps) | TypeScript (everything) |
| **DB** | SQLite (embedded) | PostgreSQL (embedded or external) |
| **Frontend** | React + Vite (embedded in Go binary) | React + Vite (Radix UI, rich components) |
| **Agent model** | YAML DSL or Go library | Pluggable adapters (Claude, Codex, Cursor, etc.) |
| **License** | Proprietary | MIT (open source) |

---

## Where Paperclip Has the Edge

### 1. Multi-Agent Adapter System (Bring-Your-Own-Agent)

Paperclip supports 7+ agent adapters out of the box — Claude Code, OpenAI Codex, Cursor, Gemini, OpenClaw, OpenCode, Pi. Agents aren't tied to a single LLM provider. Vega supports Anthropic and OpenAI-compatible endpoints at the LLM level, but doesn't have the concept of wrapping external agent tools (like Cursor or Codex CLI) as first-class runtimes.

**Consideration:** Add an adapter layer that lets agents delegate to external coding tools (Claude Code, Codex CLI, Cursor) rather than only raw LLM calls.

### 2. Plugin System with SDK

Paperclip has a full plugin architecture — worker processes, UI slot registration (dashboards, tabs, sidebars), event subscriptions, scheduled jobs, agent tool registration, and SSE streaming. There's an official SDK (`packages/plugins/sdk`) and a `create-plugin` scaffolder.

**Consideration:** Vega has MCP integration and skills, but no formal plugin system with UI extensibility. A plugin SDK would let the community extend Vega without forking.

### 3. Company-Centric Domain Model

Paperclip models entire autonomous organizations — companies have org charts, reporting lines, goals that cascade down to projects and issues. This is a richer organizational metaphor than Vega's process-tree model.

**Consideration:** The Hellotron C-suite already has personas, but lacks a formal org-chart/goal-hierarchy model. Adding structured goal alignment (company -> project -> task) would strengthen the product story.

### 4. Atomic Task Checkout & Issue Tracking

Paperclip has built-in issue/task management with atomic checkout (only one agent can work a task at a time), comments, attachments, work products, and status tracking. This prevents duplicate work in multi-agent scenarios.

**Consideration:** Vega's blackboard provides shared state, but there's no built-in task/issue system. Consider a lightweight task registry with atomic assignment.

### 5. Budget Enforcement at Runtime

Paperclip does atomic budget checking — budget is verified and deducted before each agent run. There are monthly per-agent budgets, cost event tracking, and throttling when over budget.

**Consideration:** Vega has per-agent budgets and cost tracking, but Paperclip's atomic enforcement (check-and-deduct in one transaction) is more robust for production multi-tenant scenarios.

### 6. Execution Workspace Isolation

Paperclip supports git worktree provisioning per agent run — each task gets an isolated branch/workspace. Automatic lifecycle management (create, use, cleanup).

**Consideration:** Vega has container support but not per-task git worktree isolation. This would be valuable for code-generation agents working on parallel tasks.

### 7. Session Persistence Across Heartbeats

Agents in Paperclip maintain context across scheduled wakeups. They don't start from scratch each time — file handles, session state, and conversation context persist.

**Consideration:** Vega has conversation history and memory tools, but the "heartbeat with session persistence" pattern is worth adopting for long-running autonomous agents.

### 8. Company Import/Export (Portability)

Paperclip can export an entire org (agents, skills, config) with secret scrubbing, and import it elsewhere. This enables templates and backup/restore.

**Consideration:** `vega-population` handles persona sharing, but there's no equivalent for exporting/importing a full multi-agent setup. Consider a `vega export/import` command.

### 9. Frontend Richness

Paperclip has 90+ component directories, MDX editing, Mermaid diagrams, drag-and-drop, command palette (cmdk), and richer UI patterns. The UI is more of a full project-management tool.

**Consideration:** Vega's dashboard is functional but more operational (process explorer, event stream). Paperclip's richer UI reflects a more complete product experience.

### 10. Approval/Governance Workflows

Paperclip has a formal approval system — board members can approve/reject agent hires, strategies, and major decisions. This is governance for autonomous systems.

**Consideration:** Vega's supervision handles fault tolerance but not human-in-the-loop governance. Adding approval gates for high-stakes agent actions would be valuable.

---

## Where Vega Has the Edge

### 1. Erlang-Style Fault Tolerance

Vega's supervision trees, process linking, monitors, and restart strategies (Restart, Stop, Escalate, RestartAll) are deeply inspired by Erlang/OTP. This is production-grade fault tolerance that Paperclip doesn't have — if a Paperclip agent crashes, there's no automatic restart with backoff or cascading failure handling.

### 2. Go Performance & Single Binary

Vega compiles to a single Go binary with the React frontend embedded. Zero runtime dependencies — no Node.js, no npm, no pnpm. This makes deployment trivially simple compared to Paperclip's Node.js + PostgreSQL stack.

### 3. YAML DSL for Non-Programmers

Vega's YAML DSL with control flow (if/then/else, for-each, parallel, try/catch), variable interpolation, and composable agent definitions lets non-programmers define multi-agent workflows. Paperclip requires TypeScript/UI configuration.

### 4. Streaming Architecture (SSE)

Vega has first-class streaming — token-by-token SSE with inline tool call panels, real-time event streams, and the full LLM execution loop visible in the dashboard. Paperclip has WebSocket events but not the same granularity of streaming LLM output.

### 5. MCP (Model Context Protocol) Integration

Vega has native MCP support with auto-download of MCP server binaries from GitHub Releases, stdio and HTTP transports, and full tool discovery. Paperclip relies on its adapter pattern which is less standardized.

### 6. Error Classification & Smart Retry

Vega classifies errors into 7 categories (RateLimit, Overloaded, Timeout, Temporary, InvalidRequest, Auth, BudgetExceeded) with automatic retry decisions, exponential backoff, and circuit breakers. This is more sophisticated than Paperclip's error handling.

### 7. Multi-Channel Deployment

Vega agents can interact via Slack, Telegram, Voice (VAPI), HTTP API, and web dashboard out of the box. The Hellotron personas each have their own Slack apps. Paperclip is primarily dashboard-driven.

### 8. Team Delegation & Blackboard

Vega's delegation system (agents spawning and delegating to teammates with context enrichment) and blackboard (shared structured state with `bb_read`, `bb_write`, `bb_list`) enable sophisticated multi-agent coordination patterns that Paperclip handles through its issue/comment system instead.

### 9. Memory System

Vega has both active memory (agents call remember/recall/forget) and passive memory (LLM-powered extraction of key facts from conversations). This gives agents persistent knowledge across sessions without manual configuration.

### 10. Operational Simplicity

SQLite + single binary = no PostgreSQL to manage, no migrations to run manually, no pnpm workspaces. Vega can run on a $5 VPS. Paperclip needs a full Node.js stack with PostgreSQL.

---

## Prioritized Recommendations for Vega

| Priority | Feature | Why |
|---|---|---|
| **High** | **Plugin SDK with UI slots** | Lets community extend Vega without forking. Paperclip's model (worker processes + UI slots + event subscriptions) is well-designed. |
| **High** | **Agent adapter abstraction** | Wrapping external tools (Claude Code CLI, Codex, Cursor) as first-class agent runtimes would massively expand what Vega agents can do. |
| **High** | **Approval/governance gates** | Human-in-the-loop for high-stakes autonomous decisions. Critical for enterprise trust. |
| **Medium** | **Git worktree per task** | Isolated execution workspaces for code-generation agents working in parallel. |
| **Medium** | **Atomic task checkout** | Prevents duplicate work when multiple agents are running. Simple but powerful. |
| **Medium** | **Company import/export** | Export a full Vega setup (agents, skills, config) as a portable bundle. |
| **Medium** | **Goal hierarchy** | Company -> Project -> Task alignment gives structure to autonomous work. |
| **Low** | **Heartbeat scheduling with session persistence** | Vega has schedulers already, but persistent sessions across wakeups would help long-running agents. |
| **Low** | **Richer frontend components** | Command palette, drag-and-drop, MDX editing would elevate the dashboard UX. |

---

## Summary

**Paperclip** excels at the **organizational layer** — it's a project management tool for AI companies with rich governance, task management, budget enforcement, and extensibility via plugins. It's broader in scope.

**Vega** excels at the **runtime layer** — it's an agent execution engine with superior fault tolerance, streaming, error handling, multi-channel delivery, and operational simplicity. It's deeper in its core competency.

The biggest opportunity for Vega is to layer Paperclip-style organizational primitives (goals, tasks, approvals, plugins) on top of its already-superior agent runtime — combining the best of both worlds.
