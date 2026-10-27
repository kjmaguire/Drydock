// Narrow windows (the UI review of 2026-10-21, decision P85): on Windows people snap Drydock to half the screen beside their
// editor (about 960 px), and the app must still read there and on a phone-sized window (390 px). Measured on the real app and
// the real core: nothing wider than the window, a room's messages at least 480 px wide at 960, the details panel closed until
// asked for and then over the page, the Rooms rail and the chat list as drawers below 900 px, and the top tabs as icons that
// keep their names.
import { expect, test, type Page } from '@playwright/test';
import { appUrl, createProjectWithoutRepo, openRoom, roomLink } from './helpers';

const noSidewaysScroll = async (page: Page) => {
  const { scroll, width, over } = await page.evaluate(() => {
    const w = window.innerWidth;
    // the outermost elements that reach past the window's right edge, to say what is too wide
    const over = Array.from(document.querySelectorAll<HTMLElement>('body *'))
      .filter((e) => e.getBoundingClientRect().right > w + 0.5 && !(e.parentElement && e.parentElement.getBoundingClientRect().right > w + 0.5))
      .slice(0, 5)
      .map((e) => `${e.tagName.toLowerCase()}${e.getAttribute('data-testid') ? `[${e.getAttribute('data-testid')}]` : ''}${e.getAttribute('aria-label') ? `(${e.getAttribute('aria-label')})` : ''} right=${Math.round(e.getBoundingClientRect().right)}`);
    return { scroll: document.documentElement.scrollWidth, width: w, over };
  });
  expect(scroll, `the page is wider than the window: ${over.join('; ')}`).toBeLessThanOrEqual(width);
};
const widthOf = async (page: Page, selector: string) => (await page.locator(selector).first().boundingBox())?.width ?? 0;

test('a half-screen window keeps the conversation readable and the panels out of its way', async ({ page }) => {
  await createProjectWithoutRepo(page, 'narrow-room');
  await page.setViewportSize({ width: 960, height: 800 });
  await openRoom(page, 'general');
  await expect(page.getByRole('heading', { level: 1, name: /general/ })).toBeVisible();

  // the details panel starts as the slim strip; opened, it floats over the room and closes again
  await expect(page.getByRole('button', { name: 'Show details' })).toBeVisible();
  expect(await widthOf(page, '[role="log"]')).toBeGreaterThanOrEqual(480);
  await noSidewaysScroll(page);
  await page.getByRole('button', { name: 'Show details' }).click();
  const panel = page.getByRole('complementary', { name: 'Details' });
  await expect(panel.getByRole('button', { name: 'Hide details' })).toBeVisible();
  expect(await panel.evaluate((n) => getComputedStyle(n).position)).toBe('absolute');
  expect(await widthOf(page, '[role="log"]')).toBeGreaterThanOrEqual(480);
  await panel.getByRole('button', { name: 'Hide details' }).click();

  // the top tabs are icons, still named for a screen reader and a tooltip
  const tasks = page.getByRole('link', { name: 'Tasks', exact: true });
  await expect(tasks).toBeVisible();
  expect(await tasks.getAttribute('title')).toBe('Tasks');
  expect(await widthOf(page, '[data-testid="nav-tasks"]')).toBeLessThan(60);

  // a laptop window under 1200 px keeps the sidebar folded too, so a list-and-detail page keeps its detail (UI audit 2026-10-26);
  // from 1200 px the sidebar opens again by itself
  const sidebar = page.getByTestId('sidebar');
  await page.setViewportSize({ width: 1100, height: 800 });
  await expect(sidebar).toHaveAttribute('data-rail', 'true');
  await page.setViewportSize({ width: 1280, height: 800 });
  await expect(sidebar).not.toHaveAttribute('data-rail', 'true');
});

test('a phone-sized window shows the rooms and the chat list as drawers and never scrolls sideways', async ({ page }) => {
  await createProjectWithoutRepo(page, 'narrow-phone');
  await openRoom(page, 'general');
  await page.setViewportSize({ width: 390, height: 800 });
  await expect(page.locator('[role="log"]')).toBeVisible();
  // the rail is not on the page until asked for
  await expect(roomLink(page, 'reviews')).toHaveCount(0);
  expect(await widthOf(page, '[role="log"]')).toBeGreaterThan(300);
  await noSidewaysScroll(page);
  await page.getByRole('button', { name: 'Rooms', exact: true }).click();
  await expect(roomLink(page, 'reviews')).toBeVisible();
  await roomLink(page, 'reviews').click();
  // a navigation closes the drawer
  await expect(page.getByRole('heading', { level: 1, name: /reviews/ })).toBeVisible();
  await expect(roomLink(page, 'general')).toHaveCount(0);
  await noSidewaysScroll(page);

  // Chats: the list is the whole screen until a chat is open, then a drawer behind a button
  const url = new URL(appUrl());
  url.pathname = '/chats';
  await page.goto(url.toString());
  const list = page.getByRole('complementary', { name: 'Chats' });
  await expect(list).toBeVisible();
  expect(await list.getAttribute('data-layout')).toBe('full');
  await noSidewaysScroll(page);
});
