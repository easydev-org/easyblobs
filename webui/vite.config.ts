import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'
import path from 'node:path'

// The SPA is embedded in the artifact binary and served by the "system" admin
// adapter, whose mount is /artifacts/system. So the page lives at
// /artifacts/system/ui/ — `base` must match for asset URLs to resolve; the data
// endpoint (/artifacts/system/downloads) is absolute and same-origin.
export default defineConfig({
  base: '/artifacts/system/ui/',
  plugins: [tailwindcss(), svelte()],
  resolve: { alias: { $lib: path.resolve('./src/lib') } },
  build: {
    // Build straight into the go:embed directory.
    outDir: '../system/ui/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
})
