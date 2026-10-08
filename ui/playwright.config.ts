import { defineConfig } from '@playwright/test';
import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

// The sandbox ships a preinstalled Chromium (PLAYWRIGHT_BROWSERS_PATH). If this Playwright version wants a different
// revision than the one installed, fall back to whichever chromium-* directory exists. `playwright install` is never run.
function chromiumFallback(): string | undefined {
  const base = process.env.PLAYWRIGHT_BROWSERS_PATH;
  if (!base || !existsSync(base)) return undefined;
  const dirs = readdirSync(base)
    .filter((d) => /^chromium-\d+$/.test(d))
    .sort();
  for (const d of dirs.reverse()) {
    for (const rel of ['chrome-linux/chrome', 'chrome-win/chrome.exe', 'chrome-mac/Chromium.app/Contents/MacOS/Chromium']) {
      const p = join(base, d, rel);
      if (existsSync(p)) return p;
    }
  }
  return undefined;
}

export default defineConfig({
  testDir: 'e2e',
  globalSetup: './e2e/global-setup.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 30_000 },
  reporter: [['list']],
  use: {
    headless: true,
    trace: 'retain-on-failure',
    launchOptions: process.env.DRYDOCK_E2E_USE_DEFAULT_CHROMIUM ? {} : { executablePath: process.env.DRYDOCK_E2E_CHROMIUM ?? chromiumFallback() },
  },
});
