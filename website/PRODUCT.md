# PRODUCT.md — v3ga.dev

## Product

**Vega** is an AI agent orchestration runtime. A single Go binary plus a YAML DSL. It applies Erlang's supervision-tree model to autonomous LLM agents: spawn, supervise, restart on failure, classify errors, isolate blast radius. SQLite persistence. MCP-native tool integration. REST + SSE API. Embedded React dashboard.

Open source. MIT. Built primarily by one engineer.

## Register

**Brand.** v3ga.dev is the marketing surface. Design IS the product here. The site itself must demonstrate engineering taste, not just describe it.

## Users

Skeptical infrastructure-leaning engineers — Go, Rust, Erlang, Elixir, distributed-systems people. They've already dismissed three AI-agent frameworks this month because the websites felt like marketing. They want to know: is the supervision claim real, or is "Erlang-style" a sticker?

They are NOT:
- product managers comparison-shopping for "AI tools"
- vibe-coders looking for a no-code agent builder
- enterprise buyers expecting a SOC 2 badge

## Voice (three words)

**Mechanical. Methodical. Opinionated.**

Not "elegant," not "warm," not "approachable." The voice is an engineer who's serious about correctness and has built a thing they believe in. Direct sentences. No marketing softeners. Confident enough to be specific.

## Anti-references

What this site is explicitly NOT trying to look like:

- **Vercel-template AI tool** — dark zinc background, indigo/violet gradient text, soft glows, grid backgrounds, Inter + JetBrains Mono. This is the first-order category reflex and the trap every AI-tool landing page falls into. The previous version of this site was textbook of this category.
- **SaaS hero-metric template** — "10x faster / 99.9% uptime / 850+ integrations" stats bar. Banned.
- **Editorial-magazine landing** — display serif italic + tracked uppercase labels + magazine grid. Currently saturated; second-order reflex.
- **Mac-window faux UI** — three gray dots above a code snippet pretending to be a terminal window. Costume.

## Strategic principles

1. **Lead with the system, not the pitch.** The hero is a real supervision tree, not a tagline.
2. **Earn the Erlang claim.** Show actual supervision strategies as annotated diagrams, not as a marketing word.
3. **Vary the section grammar.** No repeated kicker labels above every section. Each section uses different visual logic.
4. **One animation. Once.** Restraint is the voice.
5. **Imagery is diagrams.** No icons, no stock photos, no decorative SVGs. Real annotated technical figures.

## Surfaces (current)

- `/` — the home / brand page (index.astro)
- `/getting-started` — quick-start guide (getting-started.astro)

## Tech

- Astro 5 + Tailwind 4
- Deployed to Cloudflare Pages via Wrangler (`wrangler.jsonc`)
- Self-hosted via Fontshare (Switzer) and Google Fonts (Geist Mono)
