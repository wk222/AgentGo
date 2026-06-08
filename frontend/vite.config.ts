import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueJsx from '@vitejs/plugin-vue-jsx'

export default defineConfig({
  plugins: [vue(), vueJsx()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      external: ['/wails/runtime.js'],
    },
  },
  server: {
    port: 9245,
    host: '0.0.0.0',
    allowedHosts: 'all',
  },
})
