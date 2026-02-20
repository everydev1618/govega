import type { Config } from 'tailwindcss'
import typography from '@tailwindcss/typography'

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        border: 'hsl(240 3.7% 15.9%)',
        background: 'hsl(240 10% 3.9%)',
        foreground: 'hsl(0 0% 98%)',
        card: 'hsl(240 10% 3.9%)',
        'card-foreground': 'hsl(0 0% 98%)',
        muted: 'hsl(240 3.7% 15.9%)',
        'muted-foreground': 'hsl(240 5% 64.9%)',
        accent: 'hsl(240 3.7% 15.9%)',
        'accent-foreground': 'hsl(0 0% 98%)',
        destructive: 'hsl(0 62.8% 30.6%)',
        primary: 'hsl(224 76% 48%)',
        'primary-foreground': 'hsl(0 0% 98%)',
      },
      animation: {
        'pulse-subtle': 'pulse-subtle 2s ease-in-out infinite',
      },
      keyframes: {
        'pulse-subtle': {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.8' },
        },
      },
      borderRadius: {
        lg: '0.5rem',
        md: 'calc(0.5rem - 2px)',
        sm: 'calc(0.5rem - 4px)',
      },
    },
  },
  plugins: [typography],
} satisfies Config
