import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const target = env.VITE_PROXY_TARGET || 'http://localhost:8080'
  const proxied = env.VITE_USE_MOCKS !== 'true' && !env.VITE_API_BASE
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { '@': path.resolve(import.meta.dirname, './src') },
    },
    // MapLibre v6 loads its worker via new URL('./…-worker.mjs', import.meta.url);
    // pre-bundling moves the file and breaks that path.
    optimizeDeps: { exclude: ['maplibre-gl'] },
    server: {
      // Dev only: same-origin access to the team backend (no CORS / mixed content).
      proxy: proxied
        ? {
            '/api': { target, changeOrigin: true },
            '/media': { target, changeOrigin: true },
            '/healthz': { target, changeOrigin: true },
          }
        : undefined,
    },
  }
})
