// vite.config.ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      // Dev proxy — forwards /operate/* to Go backend
      '/operate': {
        target:    'http://localhost:8080',
        changeOrigin: true,
        ws: true,   // WebSocket proxying
      },
    },
  },
})
