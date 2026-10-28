import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  workers: 1,
  timeout: 60_000,
  use: { baseURL: "http://127.0.0.1:18787", viewport: { width: 1280, height: 900 }, trace: "retain-on-failure" },
  webServer: {
    command: "node tests/e2e/server.mjs",
    url: "http://127.0.0.1:18787/api/v1/health",
    reuseExistingServer: false,
    timeout: 660_000,
    gracefulShutdown: { signal: "SIGTERM", timeout: 15_000 },
  },
});
