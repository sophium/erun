/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Dev-only same-origin proxy: the API sets no CORS headers by design, so the
// browser must never call it cross-origin. Production serves the console
// same-origin behind the API edge, so no proxy is needed there.
const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:17033';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': new URL('./src', import.meta.url).pathname,
      '@kit': new URL('../erun-kit/src', import.meta.url).pathname,
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
    // Scope vitest to the app's own tests. The `playwright/` package holds the
    // OIDC sign-in e2e (`*.spec.ts`), which is a Playwright test, not a vitest
    // one — the default glob would otherwise try to collect it.
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
  },
});
