/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  // Repo-relative so the erun-devops Dockerfile's cache mount covers it.
  cacheDir: '../../.cache/frontend-vite/erun-ui-frontend',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': new URL('./src', import.meta.url).pathname,
      '@kit': new URL('../../erun-kit/src', import.meta.url).pathname,
    },
  },
  test: {
    // No component renders in these tests, so no jsdom.
    environment: 'node',
    // The gate's shared, cgroup-unaware container can trip the 5s default.
    testTimeout: 60000,
  },
});
