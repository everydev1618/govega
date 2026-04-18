/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./src/**/*.{astro,html,js,ts}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['JetBrains Mono', 'Fira Code', 'Cascadia Code', 'monospace'],
      },
      colors: {
        space: {
          950: '#09090b',
          900: '#0a0a0f',
          800: '#18181b',
          700: '#27272a',
        },
      },
      animation: {
        'float': 'float 6s ease-in-out infinite',
      },
    },
  },
}
