// The installed app on a real Windows machine (docs/packaging.md section 14; .github/workflows/release.yml). Nothing here
// starts a browser: the release workflow installs Drydock with its installer, starts the installed drydock.exe with WebView2's
// own remote-debugging switch (WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS), and these tests attach to that window over CDP. So what
// they drive is the real exe, its real WebView2, the core on NTFS under Defender, and the UI as the person sees it.
//
// DRYDOCK_CDP      the debugging endpoint, e.g. http://127.0.0.1:9333
// DRYDOCK_REPO     a git repository on the runner for the project test
// DRYDOCK_OUT      where screenshots and installed-app.json (the measurements) go
// DRYDOCK_STARTED  when the workflow started the exe (Unix ms), for the startup time
//
// @first runs on the first start after installing; @again after the app was closed and the installer run over it.
import { chromium, expect, test, type Browser, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createProject, roomLink } from '../e2e/helpers';

const out = process.env.DRYDOCK_OUT ?? 'installed-out';
const results: Record<string, unknown> = {};
let browser: Browser;
let page: Page;
const consoleErrors: string[] = [];

test.describe.configure({ mode: 'serial' });

test.beforeAll(async () => {
  mkdirSync(out, { recursive: true });
  const endpoint = process.env.DRYDOCK_CDP;
  if (!endpoint) throw new Error('DRYDOCK_CDP is not set: start the installed drydock.exe with remote debugging first');
  browser = await chromium.connectOverCDP(endpoint);
  // the app's one window: its page is the one that is not DevTools or about:blank
  await expect
    .poll(() => browser.contexts().flatMap((c) => c.pages()).find((p) => !p.url().startsWith('devtools:') && p.url() !== 'about:blank'), {
      message: 'the Drydock window has a page',
      timeout: 60_000,
    })
    .toBeTruthy();
  page = browser.contexts().flatMap((c) => c.pages()).find((p) => !p.url().startsWith('devtools:') && p.url() !== 'about:blank')!;
  page.on('console', (m) => {
    if (m.type() === 'error') consoleErrors.push(m.text());
  });
  page.on('pageerror', (e) => consoleErrors.push(`pageerror: ${e.message}`));
  // the helpers navigate by the app's own origin (wails://wails.localhost or http://wails.localhost)
  const home = new URL(page.url());
  home.pathname = '/';
  home.hash = '';
  process.env.DRYDOCK_E2E_URL = home.toString();
});

test.afterAll(async () => {
  results.consoleErrors = consoleErrors;
  writeFileSync(join(out, 'installed-app.json'), JSON.stringify(results, null, 2));
  // detach only: the workflow checks a second launch against the running app, then closes it itself
  await browser?.close().catch(() => {});
});

test('the installed exe opens its window and draws the shell', { tag: '@first' }, async () => {
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible({ timeout: 60_000 });
  const started = Number(process.env.DRYDOCK_STARTED ?? 0);
  if (started > 0) results.shellVisibleMs = Date.now() - started;
  results.url = page.url();
  results.userAgent = await page.evaluate(() => navigator.userAgent);
  results.viewport = await page.evaluate(() => ({ width: innerWidth, height: innerHeight, dpr: devicePixelRatio }));
  await expect(page.getByTestId('region-error')).toHaveCount(0);
  await page.screenshot({ path: join(out, '01-first-start.png') });
});

test('a project on a real Windows repository: the wizard, the core and git on NTFS', { tag: '@first' }, async () => {
  const repo = process.env.DRYDOCK_REPO;
  test.skip(!repo, 'DRYDOCK_REPO is not set');
  const t0 = Date.now();
  await createProject(page, 'windows-proof', repo);
  results.createProjectMs = Date.now() - t0;
  await expect(roomLink(page, 'general')).toBeVisible();
  await expect(page.getByTestId('region-error')).toHaveCount(0);
  await page.screenshot({ path: join(out, '02-project.png') });
});

test('after the app was closed and installed again, it opens on the same data', { tag: '@again' }, async () => {
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible({ timeout: 60_000 });
  const started = Number(process.env.DRYDOCK_STARTED ?? 0);
  if (started > 0) results.shellVisibleMs = Date.now() - started;
  // the project made before the reinstall is still there, with its room
  await expect(roomLink(page, 'general').or(page.getByText('windows-proof').first()).first()).toBeVisible();
  await expect(page.getByTestId('region-error')).toHaveCount(0);
  await page.screenshot({ path: join(out, '06-after-reinstall.png') });
});

// The app's own pages that need no agent, a login or the network.
const PAGES = ['/settings', '/connections', '/agents', '/models', '/security', '/backups', '/data', '/support', '/license', '/github', '/library'];

test('every workspace page opens on Windows without an error', { tag: '@first' }, async () => {
  const timings: Record<string, number> = {};
  for (const path of PAGES) {
    const u = new URL(page.url());
    u.pathname = path;
    const t0 = Date.now();
    await page.goto(u.toString());
    await expect(page.locator('#main h1, #main [role=heading]').first(), path).toBeVisible();
    timings[path] = Date.now() - t0;
    await expect(page.getByTestId('region-error'), `${path} drew an error`).toHaveCount(0);
    await page.screenshot({ path: join(out, `03-page${path.replaceAll('/', '-')}.png`) });
  }
  results.pageMs = timings;
});

test('a backup on NTFS: Back up now writes one and lists it', { tag: '@first' }, async () => {
  const u = new URL(page.url());
  u.pathname = '/backups';
  await page.goto(u.toString());
  await page.getByRole('button', { name: 'Back up now', exact: true }).click();
  // the list names each backup drydock-<UTC time>-<label>.db (internal/backup)
  await expect(page.getByText(/drydock-\d{8}T\d{6}Z-/).first()).toBeVisible({ timeout: 60_000 });
  await page.screenshot({ path: join(out, '04-backup.png') });
});

test('a support bundle on Windows: preview, then create, scrubbed', { tag: '@first' }, async () => {
  const u = new URL(page.url());
  u.pathname = '/support';
  await page.goto(u.toString());
  await page.getByRole('button', { name: 'Preview the bundle', exact: true }).click();
  await expect(page.getByText('summary.json').first()).toBeVisible({ timeout: 60_000 });
  await page.getByRole('button', { name: 'Create the bundle', exact: true }).click();
  await expect(page.getByTestId('bundle-path')).toContainText('drydock-support-', { timeout: 60_000 });
  results.supportBundle = await page.getByTestId('bundle-path').textContent();
  await page.screenshot({ path: join(out, '05-support-bundle.png') });
});

test('nothing in the window logged an error', { tag: ['@first', '@again'] }, async () => {
  // the app has no network of its own beyond what a person configures; an error here is a real one on Windows
  expect(consoleErrors).toEqual([]);
});
