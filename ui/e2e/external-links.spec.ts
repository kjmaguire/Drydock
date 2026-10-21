// Addresses outside the app, in the desktop window (audit 2026-10-16, M23). The harness serves the built app in Chromium, which
// is not the desktop shell, so this makes the page look like it is hosted by one (the global the Wails shell injects) and answers
// shell.openExternal itself: what is under test is the page, against the real router and the real Navigation API. The real
// shell, WebView2 and the system browser are NOT exercised here; nobody has run that on Windows.
import { expect, test, type Page } from '@playwright/test';
import { createProjectWithoutRepo, workspaceNav } from './helpers';

/** Records what the page asks the shell to open; the shell here says it did. */
async function shellStandIn(page: Page): Promise<unknown[]> {
  const asked: unknown[] = [];
  await page.route('**/rpc/shell.openExternal', async (route) => {
    asked.push(route.request().postDataJSON());
    await route.fulfill({ json: { result: { opened: true } } });
  });
  return asked;
}

test('in the desktop window a link to another site, window.open and a navigation away go to the shell, and the app stays where it is', async ({ page }) => {
  const asked = await shellStandIn(page);
  await page.addInitScript(() => {
    (window as unknown as { _wails: unknown })._wails = {};
  });
  let popups = 0;
  page.on('popup', () => popups++);
  await createProjectWithoutRepo(page, 'gocalc-links');

  // a link to another site (the gh install link on the GitHub page, which opens in a new window in a browser)
  await workspaceNav(page, 'nav-github');
  const link = page.getByTestId('gh-signin-state').locator('a[href="https://cli.github.com"]');
  await expect(link).toBeVisible();
  await link.click();
  await expect.poll(() => asked.length).toBe(1);
  expect(asked[0]).toEqual({ url: 'https://cli.github.com/' });
  await expect(page).toHaveURL(/\/github$/);
  expect(popups).toBe(0);

  // by the keyboard it is the same
  await link.focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => asked.length).toBe(2);
  await expect(page).toHaveURL(/\/github$/);

  // the app's own links still route inside the app
  await workspaceNav(page, 'nav-integrations');
  await expect(page).toHaveURL(/\/integrations$/);
  expect(asked).toHaveLength(2);

  // window.open makes no window and goes to the shell; an address that is not a web or mail address goes nowhere
  expect(await page.evaluate(() => window.open('https://example.com/a'))).toBeNull();
  expect(await page.evaluate(() => window.open('javascript:window.__ran=1'))).toBeNull();
  expect(await page.evaluate(() => window.open('file:///C:/Windows/System32/calc.exe'))).toBeNull();
  await expect.poll(() => asked.length).toBe(3);
  expect(asked[2]).toEqual({ url: 'https://example.com/a' });

  // a navigation that no click caught is cancelled: the window does not leave the app
  await page.evaluate(() => {
    window.location.href = 'https://example.com/away';
  });
  await expect.poll(() => asked.length).toBe(4);
  expect(asked[3]).toEqual({ url: 'https://example.com/away' });
  await expect(page).toHaveURL(/\/integrations$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Integrations' })).toBeVisible();

  // a link to a protocol handler of the system is stopped and never sent
  const stopped = await page.evaluate(() => {
    const a = document.createElement('a');
    a.href = 'ms-msdt:/id PCWDiagnostic';
    a.textContent = 'x';
    document.body.append(a);
    let prevented = false;
    window.addEventListener('click', (e) => (prevented = e.defaultPrevented), { once: true });
    a.click();
    a.remove();
    return prevented;
  });
  expect(stopped).toBe(true);
  expect(asked).toHaveLength(4);
  expect(popups).toBe(0);
  expect(await page.evaluate(() => (window as unknown as { __ran?: number }).__ran)).toBeUndefined();
});

test('in a browser (not the desktop window) a link to another site opens as it always did and the shell is not asked', async ({ page }) => {
  const asked = await shellStandIn(page);
  await page.context().route('https://cli.github.com/**', (route) => route.fulfill({ status: 200, contentType: 'text/html', body: '<title>gh</title>' }));
  await createProjectWithoutRepo(page, 'gocalc-links-browser');
  await workspaceNav(page, 'nav-github');
  const link = page.getByTestId('gh-signin-state').locator('a[href="https://cli.github.com"]');
  const [popup] = await Promise.all([page.waitForEvent('popup'), link.click()]);
  await popup.close();
  expect(asked).toEqual([]);
  await expect(page).toHaveURL(/\/github$/);
});
