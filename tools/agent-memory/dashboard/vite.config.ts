import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  base: '/dashboard/',
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:3210',
        changeOrigin: true,
      },
      '/health': {
        target: 'http://localhost:3210',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../../../internal/api/dashboard',
    emptyOutDir: true,
    sourcemap: false,
    minify: false,
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
