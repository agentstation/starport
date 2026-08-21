import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The build lands inside the Go package so go:embed can reach it. The
// build script restores dist/.gitkeep afterward so the embed directive
// compiles in a checkout that has not built the console.
export default defineConfig({
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
  ],
  build: {
    outDir: "../internal/console/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/v1": "http://localhost:8080",
      "/api": "http://localhost:8080",
    },
  },
});
