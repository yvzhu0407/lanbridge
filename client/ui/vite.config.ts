import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { viteSingleFile } from 'vite-plugin-singlefile'

export default defineConfig({
  plugins: [vue(), viteSingleFile()],
  base: './',
  server: {
    proxy: { '/api': 'http://127.0.0.1:8899' },
  },
  build: {
    target: ['chrome111', 'safari15'], // 对齐 macOS 11.0 最低支持
    outDir: '../static',
    emptyOutDir: true,
    cssCodeSplit: false,
    assetsInlineLimit: 100 * 1024 * 1024,
  },
})
