import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const apiTarget = process.env.VITE_API_TARGET || 'http://localhost:3210'

export default defineConfig({
  base: './',
  plugins: [react()],
  optimizeDeps: {
    // Mermaid pulls in dayjs and lazy-loaded diagram modules in dev.
    // Force Mermaid through Vite's optimizer and enable CJS interop for dayjs
    // so the browser doesn't request raw dayjs files without a synthetic default.
    include: ['mermaid', 'dayjs'],
    needsInterop: ['dayjs'],
  },
  server: {
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
      '/v1': {
        target: apiTarget,
        // Preserve the browser-facing Host so API same-origin checks compare
        // localhost:3100 Origin and Host consistently during hot reload.
        changeOrigin: false,
      },
      '/health': {
        target: apiTarget,
        changeOrigin: true,
      },
      '/dashboard/runtime.json': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    minify: 'esbuild',
    rollupOptions: {
      output: {
        entryFileNames: 'assets/app.js',
        chunkFileNames: 'assets/chunk-[name].js',
        assetFileNames: (assetInfo: { name?: string }) => {
          const name = assetInfo.name ?? ''
          if (name.endsWith('.css')) return 'assets/app.css'
          return 'assets/[name][extname]'
        },
      },
    },
  },
})
