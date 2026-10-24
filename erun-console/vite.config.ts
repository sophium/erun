/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

// Dev-only same-origin proxy: the API sets no CORS headers by design, so the
// browser must never call it cross-origin. Production serves the console
// same-origin behind the API edge, so no proxy is needed there.
const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:17033';

// Dev mirror of the entrypoint's brand injection (05-platform-brand.sh), so
// `yarn dev` paints the configured brand first too.
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
    // Excludes the Playwright specs under playwright/.
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    // Raised from 5s for gate CPU contention; a 60s run is a real hang.
    testTimeout: 60000,
  },
});
