/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  // Repo-relative so the erun-devops Dockerfile's cache mount at
  // /src/.cache/frontend-vite (WORKDIR /src) covers it without any env-var
  // plumbing; resolves under the repo root locally too, same convention as
  // the tsc build-info path in tsconfig.json and the eslint/prettier caches
  // under .cache/frontend-lint.
  cacheDir: '../../.cache/frontend-vite/erun-ui-frontend',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': new URL('./src', import.meta.url).pathname,
      '@kit': new URL('../../erun-kit/src', import.meta.url).pathname,
    },
  },
  test: {
    // No component ever renders in these tests (the few that touch
    // window/document stub them by hand), so the default 'node' environment
    // -- not jsdom -- matches what the suite already exercised under
    // `node --test`.
    environment: 'node',
    // Same rationale as erun-console's vite.config.ts: the gate's test stage
    // runs on a shared, cgroup-unaware container, so the 5s default can trip
    // under contention with nothing wrong in the test itself.
    testTimeout: 60000,
  },
});
