import type { Config } from 'tailwindcss'
import typography from '@tailwindcss/typography'

/*
  Vega dashboard tokens.
  Palette + type align with v3ga.dev (see website/DESIGN.md).
  Product register: Restrained color, fixed rem scale, calm motion.

  Semantic names (background, card, border, primary, etc.) are preserved
  from the shadcn baseline so existing utility classes keep working;
  the values behind them are completely retuned.
*/
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Surfaces
        background:           'oklch(96.4% 0.012 60)',    // warm cream
        foreground:           'oklch(22% 0.018 40)',      // charcoal
        card:                 'oklch(96.4% 0.012 60)',    // same as background
        'card-foreground':    'oklch(22% 0.018 40)',
        muted:                'oklch(93.2% 0.018 55)',    // paper-deep (panel fill)
        'muted-foreground':   'oklch(45% 0.020 45)',      // ink-soft
        accent:               'oklch(93.2% 0.018 55)',    // paper-deep (selected bg)
        'accent-foreground':  'oklch(22% 0.018 40)',
        border:               'oklch(85.5% 0.022 50)',    // rule (hairline)
        input:                'oklch(85.5% 0.022 50)',
        ring:                 'oklch(40% 0.135 30)',      // oxblood focus ring

        primary:              'oklch(40% 0.135 30)',      // oxblood
        'primary-foreground': 'oklch(96.4% 0.012 60)',
        'primary-deep':       'oklch(32% 0.130 28)',

        destructive:          'oklch(56% 0.190 30)',      // signal
        'destructive-foreground': 'oklch(96.4% 0.012 60)',

        // Custom semantic state colors (used directly: text-running, bg-signal, etc.)
        paper:                'oklch(96.4% 0.012 60)',
        'paper-deep':         'oklch(93.2% 0.018 55)',
        rule:                 'oklch(85.5% 0.022 50)',
        ink:                  'oklch(22% 0.018 40)',
        'ink-soft':           'oklch(45% 0.020 45)',
        'ink-faint':          'oklch(60% 0.018 45)',
        brand:                'oklch(40% 0.135 30)',
        'brand-deep':         'oklch(32% 0.130 28)',
        signal:               'oklch(56% 0.190 30)',      // failure / destructive
        'signal-soft':        'oklch(72% 0.090 35)',      // warning
        running:              'oklch(48% 0.090 145)',     // calm forest — "live"

        // Legacy color names that components still reference. Mapped to the
        // new palette so existing className uses keep rendering sensibly.
        // (Audited as components are migrated; these can be removed once nothing references them.)
        amber: {
          400: 'oklch(72% 0.090 35)',                     // → signal-soft
          500: 'oklch(72% 0.090 35)',
        },
        green: {
          400: 'oklch(48% 0.090 145)',                    // → running
          500: 'oklch(48% 0.090 145)',
          900: 'oklch(35% 0.060 145)',
        },
        blue: {
          400: 'oklch(40% 0.135 30)',                     // → brand
          500: 'oklch(40% 0.135 30)',
        },
        red: {
          400: 'oklch(56% 0.190 30)',                     // → signal
          500: 'oklch(56% 0.190 30)',
          900: 'oklch(40% 0.130 30)',
        },
      },

      fontFamily: {
        sans: [
          'Switzer',
          'ui-sans-serif',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'system-ui',
          'sans-serif',
        ],
        mono: [
          'Geist Mono',
          'ui-monospace',
          'SFMono-Regular',
          'Menlo',
          'Consolas',
          'monospace',
        ],
      },

      fontSize: {
        // Product-register fixed scale. Tighter steps (~1.18 ratio).
        '2xs': ['0.6875rem', { lineHeight: '1rem' }],   // 11px — annos, chips
        xs:   ['0.75rem',   { lineHeight: '1.1rem' }],  // 12px — metadata
        sm:   ['0.8125rem', { lineHeight: '1.2rem' }],  // 13px — UI default
        base: ['0.9375rem', { lineHeight: '1.45rem' }], // 15px — body
        lg:   ['1.0625rem', { lineHeight: '1.55rem' }], // 17px — section
        xl:   ['1.25rem',   { lineHeight: '1.7rem' }],  // 20px — page H2
        '2xl':['1.5rem',    { lineHeight: '2rem' }],    // 24px — page H1
        '3xl':['1.875rem',  { lineHeight: '2.25rem' }], // 30px — rare hero
      },

      borderRadius: {
        none: '0',
        sm:   '2px',
        DEFAULT: '3px',
        md:   '4px',
        lg:   '4px',
        xl:   '6px',
        '2xl':'8px',
        full: '9999px',
      },

      ringWidth: {
        DEFAULT: '1px',
        2: '2px',
      },

      transitionTimingFunction: {
        'out-quart': 'cubic-bezier(0.22, 1, 0.36, 1)',
      },

      transitionDuration: {
        DEFAULT: '160ms',
      },
    },
  },
  plugins: [typography],
} satisfies Config
