import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig, loadEnv } from 'vite'
import vuetify from 'vite-plugin-vuetify'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  // Адрес бэкенда для dev-прокси; порт по умолчанию совпадает с HOST_HTTP_PORT из .env.example.
  const target = env.VITE_API_TARGET || 'http://localhost:8090'

  return {
    plugins: [vue(), vuetify({ autoImport: true })],
    resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
    server: {
      port: 5173,
      proxy: {
        // Запросы идут на тот же origin, поэтому CORS на бэкенде не нужен.
        '/api': {
          target,
          changeOrigin: true,
          configure: (proxy) => {
            // Иначе браузер покажет собственное окно Basic Auth на каждый 401.
            proxy.on('proxyRes', (res) => {
              delete res.headers['www-authenticate']
            })
          },
        },
      },
    },
  }
})
