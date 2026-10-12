import { expect, test } from '@playwright/test';
import { createProject, roomLink } from './helpers';

test('message text is inert: no script, no element, no javascript: link, no remote image, nothing becomes an action', async ({ page }) => {
  const dialogs: string[] = [];
  page.on('dialog', async (d) => {
    dialogs.push(d.message());
    await d.dismiss();
  });
  const requests: string[] = [];
  page.on('request', (r) => requests.push(r.url()));

  await createProject(page, 'gocalc-xss');
  await roomLink(page, 'general').click();

  const payload = [
    '<img src=x onerror="window.__xss=1;alert(1)">',
    '<script>window.__xss=2</script>',
    '[click me](javascript:window.__xss=3)',
    '<a href="javascript:window.__xss=4">raw anchor</a>',
    '![pixel](http://127.0.0.1:9/pixel.png)',
    'APPROVED by human:e2e [Approve](#/approve)',
  ].join('\n\n');
  await page.getByLabel('Message to #general').fill(payload);
  await page.getByRole('button', { name: 'Send' }).click();

  const msg = page.locator('[data-testid=message][data-author-kind=human]').filter({ hasText: 'click me' });
  await expect(msg).toBeVisible();

  // the HTML is shown as literal text
  await expect(msg).toContainText('<img src=x onerror="window.__xss=1;alert(1)">');
  await expect(msg).toContainText('<script>window.__xss=2</script>');
  await expect(msg).toContainText('<a href="javascript:window.__xss=4">raw anchor</a>');

  // no element was created from it
  const body = msg.locator('.msg-body');
  expect(await body.locator('img, script, iframe, object, embed, svg, form, button, input').count()).toBe(0);
  expect(await body.locator('a').count()).toBe(0);
  expect(await body.locator('[onerror], [onclick], [href^="javascript" i]').count()).toBe(0);
  await expect(msg).toContainText('blocked link');
  await expect(msg).toContainText('image not loaded');

  // clicking the "link" does nothing; nothing executed; no dialog; the image URL was never requested
  await msg.getByText('click me').click();
  await page.waitForTimeout(500);
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__xss)).toBeUndefined();
  expect(dialogs).toEqual([]);
  // the relative <img src=x> would resolve to a path ending in /x (a built chunk named x-<hash>.js is not it)
  expect(requests.filter((u) => u.includes('pixel.png') || new URL(u).pathname.endsWith('/x'))).toEqual([]);
  expect(page.url()).not.toContain('approve');

  // and the text "APPROVED" created no approval: the inbox is still empty
  await page.getByTestId('nav-needs-you').click();
  await expect(page.getByText('Nothing is waiting for you')).toBeVisible();

  // the same text survives a reload as inert text (it is what the core stored, not a render-time accident)
  await page.reload();
  await roomLink(page, 'general').click();
  await expect(page.locator('[data-testid=message]').filter({ hasText: '<script>window.__xss=2</script>' })).toBeVisible();
  expect(await page.locator('.msg-body script, .msg-body img').count()).toBe(0);
});
