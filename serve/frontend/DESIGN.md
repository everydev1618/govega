# DESIGN.md — Vega dashboard

Inherits palette and type from `website/DESIGN.md` (v3ga.dev). Differences below are product-register adaptations.

## Color

**Strategy: Restrained.** Same OKLCH palette as the website; oxblood used only for primary action / current selection / state indicators. Never decorative.

```
--paper        oklch(96.4% 0.012 60)    warm cream      background
--paper-deep   oklch(93.2% 0.018 55)    block fill      sidebar, panels, code surfaces
--rule         oklch(85.5% 0.022 50)    hairline        all dividers and borders
--ink          oklch(22%   0.018 40)    body type
--ink-soft     oklch(45%   0.020 45)    secondary text
--ink-faint    oklch(60%   0.018 45)    tertiary, idle indicators
--brand        oklch(40%   0.135 30)    oxblood         primary actions, selected, focus ring
--brand-deep   oklch(32%   0.130 28)    hover / pressed
--signal       oklch(56%   0.190 30)    failure / destructive
--signal-soft  oklch(72%   0.090 35)    warning
--running      oklch(48%   0.090 145)   forest green    "live" / running processes
```

Tailwind semantic tokens (`bg-card`, `border-border`, `text-muted-foreground`, etc.) are preserved from the shadcn baseline; their underlying values are rebound to these.

## Typography

**Single sans (Switzer) + Geist Mono for system data.**

```
Heading L1 (page title)   1.5rem    weight 600    leading 2rem      tracking -0.005em
Heading L2                1.25rem   weight 600    leading 1.7rem
Heading L3                1.0625rem weight 600    leading 1.55rem
Body                      0.9375rem weight 400    leading 1.45rem   max 75ch for prose
UI default                0.8125rem weight 400    leading 1.2rem
Metadata                  0.75rem   weight 400    leading 1.1rem    color ink-faint
Anno / chip               0.6875rem weight 500    mono uppercase    tracking 0.06em color brand
Code                      0.8125rem weight 400    mono              leading 1.55
```

Fixed rem scale (no `clamp()`). 1.18 ratio between steps. Tabular numerics (`.tnum`) on any numeric column.

## Layout

- Sidebar 14rem (224px), paper-deep background, hairline right border
- Main content: paper background, fluid padding 1.25rem mobile → 2rem desktop
- No nested cards. Use hairline rules to separate sections inside a panel.
- Border-radius dropped from 0.5rem → 2-4px (utilitarian, not soft)
- Top-bar (when present) is 48px high with a hairline bottom

## Components

- **Nav item**: hover = `bg-paper-deep` + `text-ink`. Selected = same bg + `text-ink font-medium` + a 2px oxblood left-rail (`::before`)
- **Buttons**: `btn-primary` is solid charcoal on paper, hover → brand-deep. `btn-secondary` is paper with hairline border. `btn-ghost` is naked text.
- **Status dots**: 8×8px filled circle. running-green / signal-red / ink-faint(idle)
- **Live indicator** for actively-inferring agents: a 1.6s opacity pulse via `.live-pulse` (replaces orbital `agent-orbit-dot`)
- **Code blocks / log surfaces**: paper-deep ground, Geist Mono, no syntax highlighting unless semantic (oxblood = key, running = string, signal = error)
- **Avatars**: existing `AgentAvatar` recolored to oxblood/cream stages

## Motion

- 160ms ease-out default for all transitions (configured as `transitionDuration.DEFAULT`)
- Motion conveys state, never decoration
- `prefers-reduced-motion` honored globally — animation-duration drops to 0.01ms

## Killed outright (from prior version)

- `constellation-fade-in`, `constellation-glow`, `constellation-line-draw`, `constellation-orbit`, `constellation-activity-in`
- `agent-orbit-dot` (replaced with `.live-pulse`)
- `visualize-glow` breathing ring
- `pulse-subtle`
- Red unread pills (replaced with oxblood ink count)
- Decorative ping rings on running indicators
- Soft "shadow-lg" on FAB / floating buttons (replaced with hairline border)

## Iconography

- Heroicons stroke 1.5 throughout; no mixing weights
- Status indicators are filled shapes (dots, bars), not stroke icons

## What to refuse

- Going back to dark mode without an explicit theme task
- Adding a second accent color
- Decorative animations
- Side-stripe colored borders > 1px (except the 2px oxblood selection rail)
- Nested cards
