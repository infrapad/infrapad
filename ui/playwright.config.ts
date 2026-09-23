import { defineConfig } from "@playwright/test";

const authPort = Number(process.env.INFRAPAD_E2E_AUTH_PORT ?? "18089");
const vitePort = Number(process.env.INFRAPAD_E2E_VITE_PORT ?? "15173");
const authOrigin = `http://localhost:${authPort}`;

export default defineConfig({
  testDir: "./apps/standalone/e2e",
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: authOrigin,
    trace: "retain-on-failure",
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : undefined,
  },
  webServer: [
    {
      command: `INFRAPAD_VITE_PORT=${vitePort} INFRAPAD_AUTH_PORT=${authPort} npm run dev`,
      url: `http://127.0.0.1:${vitePort}`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command: `cd ../deploy/podman/dummy-auth && GOWORK=off go run . proxy --listen=127.0.0.1:${authPort} --upstream=http://127.0.0.1:${vitePort}`,
      url: `http://127.0.0.1:${authPort}/oauth/healthz`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
});
