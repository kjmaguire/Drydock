// The installed app on a real Windows machine (docs/packaging.md section 14; .github/workflows/release.yml). Nothing here
// starts a browser: the release workflow installs Drydock with its installer, puts the test variant of drydock.exe (the same
// build with app.DebugPort set, which opens WebView2's remote debugging on 127.0.0.1) in the install folder, starts it, and these
// tests attach to its window over CDP. So what they drive is the installed app, its real WebView2, the core on NTFS under
// Defender, and the UI as the person sees it. (The shipped exe has no debugging port; Wails clears WebView2's own switches.
// The workflow checks the shipped exe's window through UI Automation instead.)
//
// DRYDOCK_CDP      the debugging endpoint, e.g. http://127.0.0.1:9333
// DRYDOCK_REPO     a git repository on the runner for the project test
// DRYDOCK_OUT      where screenshots and installed-app.json (the measurements) go
// DRYDOCK_STARTED  when the workflow started the exe (Unix ms), for the startup time

import { chromium, expect, test, type Browser, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createProject, roomLink } from '../e2e/helpers';

const out = process.env.DRYDOCK_OUT ?? 'installed-out';
const results: Record<string, unknown> = {};
let browser: Browser;
let page: Page;
const consoleErrors: string[] = [];
const failedRequests: { status: number; method: string; url: string }[] = [];

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
  // every answer of 400 or more, with its address: the console only says "Failed to load resource"
  page.on('response', (r) => {
    if (r.status() >= 400) failedRequests.push({ status: r.status(), method: r.request().method(), url: r.url() });
  });
  // the helpers navigate by the app's own origin (wails://wails.localhost or http://wails.localhost)
  const home = new URL(page.url());
  home.pathname = '/';
  home.hash = '';
  process.env.DRYDOCK_E2E_URL = home.toString();
});

test.afterAll(async () => {
  results.consoleErrors = consoleErrors;
  results.failedRequests = failedRequests;
  writeFileSync(join(out, 'installed-app.json'), JSON.stringify(results, null, 2));
  // detach only: the workflow checks a second launch against the running app, then closes it itself
  await browser?.close().catch(() => {});
});

test('the installed exe opens its window and draws the shell', async () => {
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible({ timeout: 60_000 });
  const started = Number(process.env.DRYDOCK_STARTED ?? 0);
  if (started > 0) results.shellVisibleMs = Date.now() - started;
  results.url = page.url();
  results.userAgent = await page.evaluate(() => navigator.userAgent);
  results.viewport = await page.evaluate(() => ({ width: innerWidth, height: innerHeight, dpr: devicePixelRatio }));
  await expect(page.getByTestId('region-error')).toHaveCount(0);
  await page.screenshot({ path: join(out, '01-first-start.png') });
});

test('the first start says the agents run as the person, under their own terms, until they say they understand (D1)', async () => {
  const notice = page.getByTestId('terms-notice');
  await expect(notice).toBeVisible();
  await expect(notice).toContainText('never reads, copies or sends your Claude credentials');
  await page.screenshot({ path: join(out, '01b-terms-notice.png') });
  await notice.getByRole('button', { name: 'I understand' }).click();
  await expect(notice).toHaveCount(0);
  // acknowledged for good: a reload does not bring it back
  await page.reload();
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
  await expect(page.getByTestId('terms-notice')).toHaveCount(0);
});

test('a project on a real Windows repository: the wizard, the core and git on NTFS', async () => {
  const repo = process.env.DRYDOCK_REPO;
  test.skip(!repo, 'DRYDOCK_REPO is not set');
  const t0 = Date.now();
  await createProject(page, 'windows-proof', repo);
  results.createProjectMs = Date.now() - t0;
  await expect(roomLink(page, 'general')).toBeVisible();
  await expect(page.getByTestId('region-error')).toHaveCount(0);
  await page.screenshot({ path: join(out, '02-project.png') });
});

// The app's own pages that need no agent, a login or the network.
const PAGES = ['/settings', '/connections', '/agents', '/models', '/security', '/backups', '/data', '/support', '/license', '/github', '/library'];

test('every workspace page opens on Windows without an error', async () => {
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

test('a backup on NTFS: Back up now writes one and lists it', async () => {
  const u = new URL(page.url());
  u.pathname = '/backups';
  await page.goto(u.toString());
  await page.getByRole('button', { name: 'Back up now', exact: true }).click();
  // the list names each backup drydock-<UTC time>-<label>.db (internal/backup)
  await expect(page.getByText(/drydock-\d{8}T\d{6}Z-/).first()).toBeVisible({ timeout: 60_000 });
  await page.screenshot({ path: join(out, '04-backup.png') });
});

test('a support bundle on Windows: preview, then create, scrubbed', async () => {
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

test('nothing in the window threw, and every file of the app was there', async () => {
  // A call into the core that the core refuses (the GitHub page without gh, a connection not set up) answers 422 through Wails'
  // bridge: the page shows it, as it should. What must never happen: an uncaught error, an error logged by the page itself, or a
  // file of the app that is not there (a 404 on a script, a style sheet or a page). Each failed request is kept in
  // installed-app.json with its address.
  const own = consoleErrors.filter((e) => !e.startsWith('Failed to load resource'));
  expect(own).toEqual([]);
  const missing = failedRequests.filter((r) => r.status === 404 && /\.(js|css|html|woff2?)(\?|$)|\/$/.test(new URL(r.url).pathname) && !r.url.endsWith('.map'));
  expect(missing).toEqual([]);
  expect(failedRequests.filter((r) => r.status >= 500)).toEqual([]);
});
