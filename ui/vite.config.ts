import { defineConfig } from 'vite';

export default defineConfig({
  root: '.',
  build: {
    outDir: '../ui-dist',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        main: 'index.html',
        auth: 'auth.html'
      }
    }
  },
  server: {
    proxy: {
      '/rpc': 'http://localhost:8080',
      '/metrics': 'http://localhost:8080',
      '/status': {
        target: 'ws://localhost:8080',
        ws: true
      }
    }
  }
});
