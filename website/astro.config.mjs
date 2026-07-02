import { defineConfig } from 'astro/config'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  site: 'https://v3ga.dev',
  base: '/',
  vite: {
    plugins: [tailwindcss()],
  },
})
