---
name: design-impeccable
description: Design vocabulary, anti-patterns, and discipline for agents that design or build app UI, websites, or landing pages
tags: [design, ui, frontend, web, brand]
triggers:
  - type: keyword
    keywords:
      - design
      - UI
      - UX
      - frontend
      - landing page
      - website
      - build app
      - app design
      - visual
      - typography
      - color palette
      - brand
  - type: pattern
    pattern: "(design|build|ship) (a|the|this) (app|page|site|landing|dashboard|UI|interface)"
---

# Design Discipline (Impeccable)

You are designing or building something visual. Before you reach for defaults, do the work. Most AI design output looks identical because every model trained on the same pile of templates — Inter on a purple gradient, glassmorphic cards, "Boost your productivity" copy. Reject the first instinct. Name three options, pick the one that actually fits.

Distilled from impeccable.style. Apply this any time you generate HTML/CSS/JSX, choose colors or fonts, write copy for a hero section, or critique visual work.

## 1. Know the register before you touch a pixel

Every screen is either **Brand mode** or **Product mode**. The rules differ.

- **Brand mode** — design IS the product. Marketing sites, portfolios, editorial pages, hero sections. Expressive typography is fair game. Bold color is fair game. Hierarchy can be dramatic. Storytelling > consistency.
- **Product mode** — design SERVES the product. App UI, dashboards, internal tools, settings screens. Use a fixed type scale, not fluid. Color encodes function (state, severity, role) — not vibes. Consistency > expression. Clarity > drama.

Before generating, state which mode the screen lives in. If you can't tell, ask.

## 2. The 29 Anti-Patterns — refuse these

These are the tells of generic AI-generated design. If you catch yourself reaching for one, stop and pick something else.

**Color & contrast**
- Purple gradients as the default aesthetic. Especially indigo→violet→pink.
- Low-contrast body text on tinted backgrounds.
- Monochromatic "AI palettes" — six shades of one hue with no accent.
- Gradient text effects on headings as a substitute for hierarchy.

**Typography**
- Reflexively reaching for Inter, Geist, Mona Sans, Plus Jakarta Sans, or Space Grotesk. These fonts are fine — but if you didn't choose them deliberately, you didn't choose them.
- Italic display serifs as hero type (Fraunces, Recoleta, Playfair, Cormorant) because "elegant."
- Oversized body text that runs edge-to-edge with no horizontal padding.
- Mixing more than two type families without a reason.

**Layout & components**
- Nested cards. "Cardocalypse" — cards inside cards inside cards. Each nesting level needs to justify itself.
- Side-stripe / thick-left-border cards. Recognized AI tell.
- Uppercase eyebrow chips above every hero heading. Pick one signal, not three.
- Glassmorphism (frosted blur surfaces) as the default container treatment.
- Generic three-column "Features / Benefits / Pricing" template layouts.
- Hero sections where the headline, subhead, CTA, and image all carry equal weight. Pick a star.

**Copy & motion**
- "Boost your productivity." "Supercharge your workflow." "Unlock the power of X." Delete. Write what the product actually does.
- Float-up-on-scroll animations applied indiscriminately. Motion needs a reason.

## 3. Color: reach past the obvious

- Use **OKLCH** notation, not HEX or HSL. OKLCH is perceptually uniform — equal lightness values look equally bright, which makes contrast and accessibility math correct by default.
- In Brand mode, **don't pull the palette from the category stereotype**. Fintech doesn't have to be blue. Wellness doesn't have to be sage. Let cultural reading come from typography, imagery, and copy.
- In Product mode, color encodes function: neutral surfaces, one brand accent, and a small semantic set (success/warning/danger/info). Anything more is decoration.
- Verify contrast: body text ≥ 4.5:1, large text ≥ 3:1, non-text UI elements ≥ 3:1.

## 4. Typography: discipline first, expression second

- **Pick deliberately.** Before committing to a font, name your first three instincts and reject them. The font you choose should fit the brief, not the trend.
- **App UIs use fixed type scales** (e.g. 12 / 14 / 16 / 20 / 24 / 32 / 48). Fluid typography (`clamp()`) belongs in Brand mode, not in a settings screen.
- One display family + one text family is plenty. A third needs a specific job (e.g. monospace for code).
- Set body text with horizontal padding. No edge-to-edge prose at any viewport.
- Line length: 50–75 characters for prose. Wider than that = a wall.
- Line height: 1.4–1.6 for body, tighter for display.

## 5. Layout & spacing

- Use an 8px (or 4px) spacing scale. Don't invent one-off gaps.
- Whitespace is intentional — not leftover. If two elements aren't related, give them room. If they are, don't.
- Establish a clear hierarchy: one primary CTA per screen, one hero element, one star.
- Cards justify themselves or disappear. A card around content that doesn't need separation just adds a stroke for no reason.

## 6. Motion

- Motion communicates causality (this opened from that), state change (this got selected), or continuity (you're still on the same page). If it doesn't do one of those, cut it.
- Default durations: 150–250ms for UI feedback, 300–500ms for transitions. Anything slower needs a reason.
- Easing: `ease-out` for entrances, `ease-in` for exits, custom cubic-bezier only when you've thought about it.
- Respect `prefers-reduced-motion`.

## 7. Workflow

1. **State the register.** Brand or Product. Write it down.
2. **State the audience.** Who reads this? What do they already know? What do they need to do?
3. **Reject the first three instincts.** Especially for font and palette. Name the obvious choice, then name a better one.
4. **Build in the codebase, not in a mock.** Edit real files, render in a real browser, iterate on production output. Canvas handoffs are over.
5. **Audit before shipping.** Run through the 29 anti-patterns. If you find one, fix it or justify it.

## 8. Audit checklist (use on every screen before declaring done)

- [ ] Register is named and consistent.
- [ ] No purple gradient, no glassmorphism-by-default, no nested cards.
- [ ] Color in OKLCH; contrast ratios verified.
- [ ] Type families chosen deliberately; fixed scale in Product mode.
- [ ] Body text has horizontal padding; line length 50–75 chars.
- [ ] One primary CTA per screen, one clear hero.
- [ ] Motion serves a purpose; `prefers-reduced-motion` honored.
- [ ] Copy says what the product does — no "boost / supercharge / unlock."

If you can tick every box, ship it. If you can't, fix the gap or name the deliberate exception.
