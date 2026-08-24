import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The harness (harness/) is the kit's own demo app: it renders every widget in
// every state so a reviewer can check the design language without booting the
// desktop or the console. `yarn build` here builds the harness bundle; the kit
// itself is consumed as source by other workspace packages via the `@kit/*`
// alias they register for it, never via this build output.
export default defineConfig({
  root: 'harness',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@kit': new URL('./src', import.meta.url).pathname,
    },
  },
  build: {
    outDir: '../dist',
    emptyOutDir: true,
  },
});
