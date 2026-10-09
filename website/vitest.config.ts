import { fileURLToPath } from 'node:url';

import { defineConfig } from 'vitest/config';

export default defineConfig({
  // The same `@/` path as tsconfig.json, so a test can import the client
  // modules that use it.
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    environment: 'node',
    include: ['test/**/*.test.ts'],
    // The build test runs `next build` once, which takes about one minute.
    testTimeout: 10_000,
    hookTimeout: 300_000,
  },
});
