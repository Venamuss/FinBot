import react from "@vitejs/plugin-react";
import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";

export default defineConfig(({ mode }) => {
  // Server configuration only: never define client variables from backend env.
  const env = loadEnv(mode, process.cwd(), "FINBOT_DEV_");
  return {
    plugins: [react()],
    server: {
      proxy: {
        "/api": {
          target: env.FINBOT_DEV_API_URL || "http://127.0.0.1:8080",
          // Preserve Host so the backend's same-origin mutation check still works.
          changeOrigin: false,
        },
      },
    },
    test: {
      environment: "jsdom",
      setupFiles: ["./src/test/setup.ts"],
      clearMocks: true,
    },
  };
});
