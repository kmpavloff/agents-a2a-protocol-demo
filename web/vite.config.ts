import {defineConfig} from 'vite';

export default defineConfig({
  build: {outDir: '../internal/webui/dist', emptyOutDir: true},
  server: {
    proxy: {
      '/invoke': 'http://localhost:8080',
      '/.well-known': 'http://localhost:8080',
    },
  },
});
