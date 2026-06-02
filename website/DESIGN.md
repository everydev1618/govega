# DESIGN.md — v3ga.dev

The reference scene that drives every choice:

> *A Go infra engineer who's spent 10 years on distributed systems is open to one new AI framework this month, on a 32" monitor at 4pm, already dismissed three AI-agent sites today because they smelled like marketing.*

That reader needs to feel the system is real before reading a feature word.

## Color

**Strategy: Committed.** Oxblood on warm cream. The committed color carries identity.

Inverts the entire AI-tool category reflex (dark + indigo/violet). The red is semantic — failure / restart is the supervision signal — not decorative.

```
--paper       oklch(96.4% 0.012 60)   warm cream     #FAF6EE
--paper-deep  oklch(93.2% 0.018 55)   block fill     ~#F1EAD7
--rule        oklch(85.5% 0.022 50)   hairline       ~#D9CFBC
--ink         oklch(22%   0.018 40)   body type      ~#1E1A17
--ink-soft    oklch(45%   0.020 45)   secondary      ~#5B524A
--ink-faint   oklch(60%   0.018 45)   tertiary       ~#857C73
--brand       oklch(40%   0.135 30)   oxblood        ~#8B2A1A
--brand-deep  oklch(32%   0.130 28)   hover/pressed  ~#6D1F11
--signal      oklch(56%   0.190 30)   failure        ~#C84A2E
--signal-soft oklch(72%   0.090 35)   warning glow   ~#D69077
--running     oklch(48%   0.090 145)  calm forest    ~#4F7A5E
```

All neutrals tinted toward the brand hue (chroma 0.012–0.022). No `#000`, no `#fff`. No gradients. No glows.

## Typography

**Single sans, committed.** Weight and size carry hierarchy. No second family for "elegance."

- **Switzer** (Fontshare, free) — body, headlines, labels.
- **Geist Mono** (Google Fonts, free) — code blocks, system labels, version chips, annotations, nav.

Inter and JetBrains Mono are banned (reflex-reject list).

### Scale

```
display-hero     clamp(2.8rem, 6.5vw, 5.4rem)   weight 700  tracking -0.025em  leading 1.02
display-section  clamp(1.75rem, 3.5vw, 2.6rem)  weight 700  tracking -0.025em  leading 1.02
lede             clamp(1.05rem, 1.35vw, 1.18rem) weight 400 leading 1.6  max 60ch
body             17px                            weight 400  leading 1.55
anno             0.7rem mono uppercase           weight 500  tracking 0.06em  color brand
chip             0.72rem mono uppercase          weight 500  tracking 0.04em
code             0.86rem mono                    weight 400  leading 1.62
```

Tabular numerics (`.tnum`) for any numeric column or status indicator.

## Layout

**Engineering-manual structure.** Numbered §-sections, reserved left margin for §-number plus sidenotes (Tufte-style), asymmetric hero.

Page frame: `.frame` — `max-width: 84rem`, fluid gutter `clamp(1.25rem, 4vw, 3rem)`.

Section: `.section` — two-column grid `12rem | 1fr` at ≥880px. The 12rem column carries the §-number and any sidenote annotations. The right column carries content with `max-width: 68ch`.

Hairline horizontal rules (`<hr class="rule">`) between sections — structural device. No section dividers via background color or padding alone.

## Component conventions

- **No cards by default.** Use bordered rectangles only when they're truly the best affordance (the three strategy diagrams in §2; the three surfaces in §3). Never with rounded-2xl corners (`border-radius: 2px` everywhere — utilitarian, not soft).
- **No icons.** Diagrams instead. Real annotated SVG.
- **No kicker labels** above section headings as a repeated pattern. The §-number + display heading is the section grammar; the `.anno` class is used sparingly as a fig-style label only.
- **No glow, no gradient borders, no gradient text.**
- **No mac-window dots** above code snippets.

## Motion

One animation only: the supervision tree's fail-and-restart cycle on first paint of the hero. ~3 seconds total. Phases: `ok → fail → restart → ok`. Reduced-motion users get the static `ok` state immediately.

Easing: `cubic-bezier(0.22, 1, 0.36, 1)` (ease-out-quart-ish). No bounce, no elastic.

## Voice in copy

- Direct sentences. Subject-verb-object.
- No em dashes. Periods, commas, colons.
- No marketing softeners ("seamlessly," "effortlessly," "powerful").
- Headlines: a claim, not a feature label. "Agents that don't die quietly." not "Reliable AI agents."
- Annotations: one sentence, declarative, factual.

## What to refuse

- Adding a "trusted by" logo bar.
- Adding stat counters in the hero.
- Adding lifestyle photography.
- Switching to dark mode "for developers."
- Adding more colors. The palette is closed.
- Replacing Switzer with Inter "for familiarity."
- Adding icons next to every heading.
