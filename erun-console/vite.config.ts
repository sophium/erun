/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// VITE_API_PROXY_TARGET points `yarn dev` at a running erun-backend-api so the
// console can be driven against a REAL API in local dev / e2e (issue #674). The
// console fetches `/v1/...` same-origin and Vite proxies it server-side, so
// there is no browser CORS preflight (the API sets no CORS headers, by design).
// Unset → defaults to the API's default bind (127.0.0.1:17033). In production
// the console is served same-origin behind the API edge, so no proxy is needed.
const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:17033';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': new URL('./src', import.meta.url).pathname,
    },
  },
  server: {
    proxy: {
      '/v1': { target: apiProxyTarget, changeOrigin: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
});
