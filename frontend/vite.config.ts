import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    rollupOptions: {
      output: {
        // 手动分包：框架代码（vue/vue-router/pinia）内容稳定、缓存周期长，打为 vendor；
        // axios 与业务无关且体积独立，单独成 chunk，业务代码变动时这两块都能命中浏览器缓存。
        manualChunks: {
          vendor: ['vue', 'vue-router', 'pinia'],
          axios: ['axios'],
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws':  { target: 'ws://localhost:8081',  ws: true },
    },
  },
})