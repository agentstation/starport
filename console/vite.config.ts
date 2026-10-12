import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

// The build lands inside the Go package so go:embed can reach it. The
// build script restores dist/.gitkeep afterward so the embed directive
// compiles in a checkout that has not built the console.
export default defineConfig(({ mode }) => {
  // STARPORT_CONSOLE_PROXY_TARGET names the gateway that the dev server
  // proxies to. Set it when `starport dev` binds a port other than 7827.
  // loadEnv reads the process environment, so this file needs no node types.
  const env = loadEnv(mode, new URL(".", import.meta.url).pathname, "STARPORT_CONSOLE_");
  const gateway = env.STARPORT_CONSOLE_PROXY_TARGET || "http://localhost:7827";
  return {
    plugins: [
      tanstackRouter({ target: "react", autoCodeSplitting: true }),
      react(),
      tailwindcss(),
    ],
    resolve: {
      // The tsconfig "@" alias, restated for the dev server. URL.pathname
      // keeps this file inside the browser-lib type surface (no node types).
      alias: { "@": new URL("./src", import.meta.url).pathname },
    },
    build: {
      outDir: "../internal/console/dist",
      emptyOutDir: true,
    },
    server: {
      proxy: {
        "/v1": gateway,
        "/api": gateway,
        "/health": gateway,
        "/console/session": gateway,
        "/console/identity": gateway,
      },
    },
  };
});
