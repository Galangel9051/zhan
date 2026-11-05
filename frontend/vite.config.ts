import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
export default defineConfig({
 plugins: [react(), tailwindcss()],
 server: { host: '127.0.0.1', port: 5173, strictPort: true, proxy: { '/api': 'http://127.0.0.1:12345' } },
 preview: { host: '127.0.0.1', port: 4173, proxy: { '/api': 'http://127.0.0.1:12345' } },
})
