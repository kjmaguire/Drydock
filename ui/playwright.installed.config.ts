import { defineConfig } from '@playwright/test';

// The installed app on Windows (e2e-installed/installed.spec.ts): no browser is started and no core is served from ui/dist; the
// tests attach over CDP to the drydock.exe the release workflow installed and started (.github/workflows/release.yml).
export default defineConfig({
  testDir: 'e2e-installed',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 180_000,
  expect: { timeout: 30_000 },
  reporter: [['list']],
  use: { actionTimeout: 30_000, trace: 'off' },
});
