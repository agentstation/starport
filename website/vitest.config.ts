import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    include: ['test/**/*.test.ts'],
    // The build test runs `next build` once, which takes about one minute.
    testTimeout: 10_000,
    hookTimeout: 300_000,
  },
});
