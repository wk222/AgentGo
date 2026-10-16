import { defineConfig, type Plugin } from 'vite'
import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import vue from '@vitejs/plugin-vue'
import vueJsx from '@vitejs/plugin-vue-jsx'

// dist/ is a build product and is not tracked; dist/.gitkeep only keeps the
// directory present so `//go:embed dist/*` compiles before the first build.
// emptyOutDir wipes it on every build, so put it back afterwards.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'agentgo-keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(resolve(__dirname, 'dist', '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [vue(), vueJsx(), keepDistPlaceholder()],
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
