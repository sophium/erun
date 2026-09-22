/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

// Dev-only same-origin proxy: the API sets no CORS headers by design, so the
// browser must never call it cross-origin. Production serves the console
// same-origin behind the API edge, so no proxy is needed there.
const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:17033';

// One console build serves any instance, so an instance's brand cannot be baked
// in: production rewrites the served page from platform.brand at container start
// (erun-devops/docker/erun-console/docker-entrypoint.d/05-platform-brand.sh), and
// the bundle reads the injected window value for its pre-resolution paint
// (src/shell/brand.ts). `yarn dev` has no entrypoint, so mirror that injection
// here from the same environment variable — otherwise the dev server would show
// the bundled default and then flip, the very thing the entrypoint prevents.
// Unset is a no-op: the bundled default stays, and an instance that configures
// no brand is served no brand by GET /v1/platform either.
function platformBrandPlugin(): Plugin {
  return {
    name: 'erun-platform-brand',
    transformIndexHtml(html) {
      const brand = (process.env.ERUN_PLATFORM_BRAND ?? '').trim();
      if (brand.length === 0) {
        return html;
      }
      const label = `${brand} console`;
      return {
        html: html
          .replace(/<title>[^<]*<\/title>/, `<title>${label}</title>`)
          .replace(/content="[^"]*"( property="og:title")/, `content="${label}"$1`)
          .replace(/content="[^"]*"( name="twitter:title")/, `content="${label}"$1`),
        tags: [
          {
            tag: 'script',
            children: `window.__ERUN_PLATFORM_BRAND__=${JSON.stringify(brand)};`,
            injectTo: 'head-prepend',
          },
        ],
      };
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), platformBrandPlugin()],
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
    //
    // This bounds the whole test only. findBy*/waitFor use a separate timeout
    // owned by @testing-library/dom (`asyncUtilTimeout`, default 1000ms),
    // raised independently in src/test/setup.ts -- see the comment there for
    // why and how it was measured. Read the two together: this is the outer
    // bound a genuine hang trips, the other is the inner bound an async query
    // waits against gate contention.
    testTimeout: 60000,
  },
});
