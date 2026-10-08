import { defineConfig, devices } from "@playwright/test";

// The web server is `wrangler dev` (via scripts/dev.mjs) on ports taken from the
// environment, so several worktrees can run e2e at once:
//   PORT=8801 INSPECTOR_PORT=9301 npm run e2e
// Defaults (8788 / 9230) differ from `npm run dev` (8787) on purpose.
const port = Number(process.env.PORT ?? 8788);
const inspectorPort = Number(process.env.INSPECTOR_PORT ?? 9230);
const baseURL = `http://localhost:${port}`;

// Set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH to use an already installed Chromium
// when `npx playwright install chromium` cannot reach the download host.
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined;

export default defineConfig({
  testDir: "./e2e",
  outputDir: "test-results/e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: [
    ["list"],
    ["json", { outputFile: "test-results/e2e-report.json" }],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  use: {
    baseURL,
    screenshot: "on",
    trace: "retain-on-failure",
    launchOptions: executablePath ? { executablePath, args: ["--no-sandbox"] } : {},
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "node scripts/dev.mjs --build",
    url: `${baseURL}/api/health`,
    reuseExistingServer: false,
    timeout: 180_000,
    stdout: process.env.E2E_VERBOSE ? "pipe" : "ignore",
    stderr: "pipe",
    env: {
      PORT: String(port),
      INSPECTOR_PORT: String(inspectorPort),
      WRANGLER_STATE_DIR: ".wrangler/e2e-state",
      RESET_STATE: "1",
    },
  },
});
