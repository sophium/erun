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
});
