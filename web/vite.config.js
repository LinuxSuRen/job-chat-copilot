import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// base './'：构建产物由 Go 服务 embed，需支持任意子路径静态托管
export default defineConfig({
  base: './',
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:7422',
    },
  },
})
