import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

export default defineConfig(({ mode }) => {
  const root = fileURLToPath(new URL('..', import.meta.url))
  const env = loadEnv(mode, root, ['APP_PORT', 'FRONTEND_PORT'])
  const appPort = env.APP_PORT || '8080'
  const frontendPort = Number(env.FRONTEND_PORT) || 5173
  const target = `http://127.0.0.1:${appPort}`

  return {
    plugins: [react()],
    server: {
      port: frontendPort,
      proxy: {
        '/api/': {
          target,
          rewrite: path => path.slice('/api'.length),
        },
      },
    },
  }
})
