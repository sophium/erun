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
    // The gate's own test stage runs on a shared, cgroup-unaware container
    // (nothing caps its CPU/memory here yet), so a component test that
    // renders and settles comfortably inside the 5s default on a dev machine
    // can outright exceed it under real contention with nothing wrong in the
    // test itself. Reproduced locally by saturating every core with busy
    // loops: the whole suite still passes at 60s, where the 5s default fails
    // dozens of specs. A test that still can't finish in 60s is a real hang,
    // not gate contention, and should fail loudly.
    testTimeout: 60000,
  },
});
