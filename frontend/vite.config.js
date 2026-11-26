import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

export default defineConfig(({ mode }) => {
  const root = fileURLToPath(new URL('..', import.meta.url))
  const env = loadEnv(mode, root, ['HARROW_API_URL', 'APP_PORT', 'PORT'])
  const target = env.HARROW_API_URL || `http://127.0.0.1:${env.APP_PORT || env.PORT || '8080'}`

  return {
    plugins: [react()],
    server: {
      proxy: {
        '/api/': {
          target,
          rewrite: path => path.slice('/api'.length),
        },
      },
    },
  }
})
