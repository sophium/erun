import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// Builds the harness demo app only; the kit itself is consumed as source via `@kit/*`.
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
