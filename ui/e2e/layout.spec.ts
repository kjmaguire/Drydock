// The readability guard (2026-10-13, on the owner's request: "sizing, too compact views hard to read"). Every screen at a
// Windows laptop's usual CSS size (1366 x 768: a 1920 x 1080 screen at 140 %) is measured in the real app against the real
// core: no visible text under 12 px (avatar initials, which are decoration and hidden from assistive tech, aside), nothing
// wider than the window, and the details panel takes width only where it has the page's context (a room, a task, a run).
import { expect, test, type Page } from '@playwright/test';
import { createAgent, createProject, roomLink, projectItem, projectNav, openRoom } from './helpers';

const ROUTES = ['/needs-you', '/tasks', '/tasks/new', '/pulls', '/team', '/crew', '/activity', '/project', '/project/integrations', '/plan', '/requirements', '/milestones', '/memory', '/skills', '/packs', '/integrations', '/integrations/linear', '/integrations/mslearn', '/budgets', '/evals', '/security', '/chats', '/library', '/connections', '/agents', '/agents/claude', '/github', '/models/new', '/routing', '/backups', '/data', '/support', '/license', '/settings', '/home', '/library/skills', '/security-scan', '/models', '/settings/shortcuts', '/settings/about', '/library/starters', '/my-tasks'];

/** The visible text under 12 px, outside decoration hidden from assistive tech, and whether anything overflows the window. */
async function measure(page: Page) {
  return page.evaluate(() => {
    const small: string[] = [];
    for (const el of Array.from(document.querySelectorAll('body *'))) {
      const own = Array.from(el.childNodes)
        .filter((n) => n.nodeType === 3 && n.textContent!.trim())
        .map((n) => n.textContent!.trim())
        .join(' ');
      if (!own || el.closest('[aria-hidden="true"], .sr-only, script, style, svg, .xterm, .monaco-editor')) continue;
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      if (r.width === 0 || r.height === 0 || s.visibility === 'hidden' || s.display === 'none') continue;
      const fs = parseFloat(s.fontSize);
      if (fs < 11.9) small.push(`${el.tagName.toLowerCase()}${el.getAttribute('data-testid') ? `[${el.getAttribute('data-testid')}]` : ''} ${fs}px "${own.slice(0, 40)}"`);
    }
    const details = document.querySelector('aside[aria-label="Details"]')?.getBoundingClientRect().width ?? 0;
    return { small, overflowX: document.documentElement.scrollWidth > innerWidth + 1, details };
  });
}

test('every screen reads at a laptop size: text of 12 px or more, nothing wider than the window, the details panel only beside a conversation', async ({ page }) => {
  test.setTimeout(300_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  await createProject(page, 'gocalc-layout');
  await createAgent(page, 'Worker One', 'backend');

  // beside a room the panel has the room's context and starts open
  await openRoom(page, 'general');
  await expect(page.getByRole('log')).toBeVisible();
  let m = await measure(page);
  expect(m.small, 'room').toEqual([]);
  expect(m.overflowX, 'room overflows the window').toBe(false);
  expect(m.details, 'details beside a room').toBeGreaterThan(200);

  for (const path of ROUTES) {
    const u = new URL(page.url());
    u.pathname = path;
    await page.goto(u.toString());
    await expect(page.locator('#main h1, #main [role=heading]').first()).toBeVisible();
    m = await measure(page);
    expect(m.small, `${path}: text under 12 px`).toEqual([]);
    expect(m.overflowX, `${path} overflows the window`).toBe(false);
    expect(m.details, `${path}: the details panel takes width beside a page without a conversation`).toBeLessThan(60);
  }

  // a workspace page has no details panel at all (nothing of a project shows there); on a project page without a
  // conversation, opened by hand it stays open there, and the rooms keep their own choice
  await expect(page.getByRole('button', { name: 'Show details' })).toHaveCount(0);
  await page.getByTestId('nav-workspace').click();
  await projectItem(page, 'gocalc-layout').click();
  // each measurement waits for its page and is retried until the panel settles: a click returns before the next page renders,
  // and measuring then reads the previous page's panel
  const details = () => expect.poll(async () => (await measure(page)).details);
  await projectNav(page, 'nav-activity');
  await expect(page).toHaveURL(/\/activity$/);
  await page.getByRole('button', { name: 'Show details' }).click();
  await details().toBeGreaterThan(200);
  await openRoom(page, 'general');
  await expect(page.getByRole('log')).toBeVisible();
  await details().toBeGreaterThan(200);
  await page.getByRole('button', { name: 'Hide details' }).click();
  await details().toBeLessThan(60);
  await projectNav(page, 'nav-team');
  await expect(page).toHaveURL(/\/team$/);
  await details().toBeGreaterThan(200);
});

// The shell does not move and the board does not hide a column (critique 2026-11-13): scrollIntoView on the task page scrolled
// the whole shell up (the top bar off the screen, a band under it), and at 800 px the board cut its Review column off.
test('the shell stays put when the task page scrolls to its foot, and a narrow board shows all five columns', async ({ page }) => {
  await createProject(page, 'gocalc-layout-shift');
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Hold the shell still');
  await page.getByRole('button', { name: /^Create (task|only)$/ }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Hold the shell still/ })).toBeVisible();
  const steer = page.getByTestId('steer');
  await expect(steer).toBeVisible();
  // the boxes from the main region up to the document cannot be scrolled, even by code: scrollIntoView (the Evidence link, a
  // focus move) scrolled them when they were overflow hidden, which clips but still scrolls. Each is asked to move 48 px.
  const moved = await page.evaluate(() => {
    const out: string[] = [];
    const boxes: Element[] = [];
    for (let el: Element | null = document.getElementById('main'); el; el = el.parentElement) boxes.push(el);
    for (const el of boxes) {
      if (el === document.scrollingElement) continue; // the document itself has nothing to scroll
      // taller content than the box, as when a page or a banner grows past it: only then can a box be scrolled at all
      const tall = document.createElement('div');
      tall.style.height = '3000px';
      el.appendChild(tall);
      el.scrollTop = 48;
      if (el.scrollTop !== 0) out.push(`${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''} moved ${el.scrollTop}px`);
      el.scrollTop = 0;
      tall.remove();
    }
    return out;
  });
  expect(moved).toEqual([]);

  await page.setViewportSize({ width: 800, height: 900 });
  await projectNav(page, 'nav-tasks');
  const review = page.getByRole('heading', { name: /^Review/ }).first();
  await expect(review).toBeVisible();
  const box = await review.boundingBox();
  expect(box!.x + box!.width).toBeLessThanOrEqual(800);
  expect((await measure(page)).overflowX).toBe(false);
});

// Work's left column scrolls as a whole when its fixed parts are taller than the window (CI 2026-11-13: Sync now sat under the
// window's foot once the shell stopped scrolling by code, and nothing could bring it back).
test("Work's left column keeps its foot within reach in a short window", async ({ page }) => {
  await createProject(page, 'gocalc-short-window');
  await page.setViewportSize({ width: 1280, height: 300 });
  // the links at the column's foot (Board, Pull requests, Rooms) sit under the lines Sync now is among; a tall block before them
  // stands for the setup notes and lines that grew past the window on CI
  const foot = page.getByRole('navigation', { name: 'More of this project' });
  await foot.evaluate((nav) => {
    const tall = document.createElement('div');
    tall.style.cssText = 'flex: none; height: 900px';
    nav.before(tall);
  });
  const rooms = foot.getByTestId('nav-rooms');
  await rooms.scrollIntoViewIfNeeded();
  await expect(rooms).toBeInViewport();
  await rooms.click({ trial: true });
});
