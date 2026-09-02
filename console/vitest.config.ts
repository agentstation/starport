import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  // A test reads the Go usage record source to hold the TypeScript record
  // type to its JSON tags, so the runner may serve the repository root.
  server: {
    fs: { allow: [fileURLToPath(new URL("..", import.meta.url))] },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/test/setup.ts"],
  },
});
