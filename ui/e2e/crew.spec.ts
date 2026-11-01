import { expect, test, type Page } from '@playwright/test';
import { createAgent, createAndStartTask, createProject, createProjectWithoutRepo, projectNav, sidebar, switchProject, workspaceNav } from './helpers';

// The Crew pages (graphite, decision P97) against the real Go core: a project's crew (the header's figures, Today's timeline,
// a card per agent, New agent with N), an agent's own page, and the workspace's Crew page with every project's agents.

/** The visible text under 12 px, outside decoration hidden from assistive tech (the readability guard of layout.spec.ts). */
async function smallText(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const small: string[] = [];
    for (const el of Array.from(document.querySelectorAll('body *'))) {
      const own = Array.from(el.childNodes)
        .filter((n) => n.nodeType === 3 && n.textContent!.trim())
        .map((n) => n.textContent!.trim())
        .join(' ');
      if (!own || el.closest('[aria-hidden="true"], .sr-only, script, style, svg')) continue;
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      if (r.width === 0 || r.height === 0 || s.visibility === 'hidden' || s.display === 'none') continue;
      const fs = parseFloat(s.fontSize);
      if (fs < 11.9) small.push(`${el.tagName.toLowerCase()} ${fs}px "${own.slice(0, 40)}"`);
    }
    return small;
  });
}

test("the Crew page: the figures, a lane and a card per agent, New agent on N, a card opening the agent's page, and a run on Today", async ({ page }) => {
  test.setTimeout(240_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  await createProject(page, 'gocalc-crew');
  await createAgent(page, 'Ada', 'backend');
  await createAgent(page, 'Rex', 'reviewer');

  // the header: the figures and the two buttons
  await projectNav(page, 'nav-team');
  await expect(page.getByRole('heading', { level: 1, name: 'Crew' })).toBeVisible();
  await expect(page.getByTestId('crew-figures')).toHaveText(/^2 agents · \d working · \d waiting on you$/);
  await expect(page.getByTestId('figure-waiting')).toHaveText('0 waiting on you');
  await expect(page.getByRole('button', { name: 'Templates' })).toBeVisible();
  // Today: one lane per agent, under an axis that ends at now, and the legend
  const timeline = page.getByRole('region', { name: 'Today' });
  await expect(timeline.getByTestId('crew-lane')).toHaveCount(2);
  await expect(timeline.getByTestId('crew-axis')).toContainText('now');
  await expect(timeline.getByTestId('crew-legend')).toHaveText('working nowfinished runfailed runwaiting on you');
  // a card per agent: its dot, its CLI and model, its state and its line; the whole card is its name's link
  const card = page.getByTestId('agent').filter({ hasText: 'Ada' });
  await expect(card.locator('[data-dot="true"]')).toBeVisible();
  await expect(card.getByTestId('agent-runs')).toHaveText(/^Claude Code · (default model|sonnet)$/);
  await expect(card.getByTestId('agent-runs')).toHaveAttribute('title', 'Role: backend');
  await expect(card.getByTestId('presence')).toHaveText(/^(stopped|idle|working)$/);
  await expect(card.getByTestId('agent-doing')).toBeVisible();
  expect(await smallText(page), 'crew page: text under 12 px').toEqual([]);

  // N opens New agent outside a text field; Escape closes it
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('n');
  // it opens on Describe (decision P138)
  await expect(page.locator('dialog[open]').getByTestId('describe-agent')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('dialog[open]')).toHaveCount(0);

  // the card opens the agent's page
  await card.getByTestId('agent-link').click();
  await expect(page).toHaveURL(/\/team\/[^/]+$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Ada' })).toBeVisible();
  const chips = page.getByTestId('agent-chip');
  // its CLI and model (the helper creates it on the adapter's default model); the core says whether it is held in
  await expect(chips.filter({ hasText: /^Claude Code · (default model|sonnet)$/ })).toBeVisible();
  await expect(chips.filter({ hasText: /guarded|not held in/ })).toBeVisible();
  await expect(page.getByTestId('agent-now')).toContainText('Not on a task right now');
  await expect(page.getByTestId('stat-merged')).toContainText('0');
  // Message and Edit are buttons; Inspect, All agents and Remove are in the "..." menu
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
  await page.getByTestId('agent-more').click();
  await expect(page.getByRole('menuitem', { name: 'Inspect' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: 'Remove Ada...' })).toBeVisible();
  await page.keyboard.press('Escape');
  // the crew list beside it: every agent, this one marked, and the way to add one
  const crew = page.getByRole('navigation', { name: 'Crew' });
  await expect(crew.getByTestId('crew-link')).toHaveCount(2);
  await expect(crew.getByTestId('crew-link').filter({ hasText: 'Ada' })).toHaveAttribute('aria-current', 'page');
  await expect(crew.getByRole('link', { name: 'Add an agent' })).toHaveAttribute('href', '/team');
  // the Crew tab stays current; nothing on the page reads under 12 px
  await expect(page.getByTestId('nav-team')).toHaveAttribute('aria-current', 'page');
  expect(await smallText(page), 'agent page: text under 12 px').toEqual([]);
  // the tabs
  await page.getByTestId('agent-tab-skills').click();
  await expect(page.locator('section[aria-label="Skills"]')).toContainText('Ada has no skill yet.');
  await page.getByTestId('agent-tab-history').click();
  await expect(page.locator('section[aria-label="Runs"]')).toContainText('has not run yet');
  await page.getByTestId('agent-tab-overview').click();
  // the other agent's page, from the crew list
  await crew.getByTestId('crew-link').filter({ hasText: 'Rex' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Rex' })).toBeVisible();
  await expect(chips.first()).toContainText('Stopped');

  // a reload shows the same page (the Go side serves index.html for /team/:id)
  await page.reload();
  await expect(page.getByRole('heading', { level: 1, name: 'Rex' })).toBeVisible();

  // the command palette offers the page by the agent's name
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox').fill("open ada's");
  await expect(page.getByRole('option', { name: "Open Ada's page" })).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'Ada' })).toBeVisible();

  // Message from the hero opens a direct message with the agent
  await page.getByRole('button', { name: 'Message Ada' }).click();
  await expect(page.getByLabel('Messages in dm:', { exact: false })).toBeVisible();

  // a task Ada works on puts its run on Today, named by the task's key and placed by time (the scripted agent of the harness)
  await createAndStartTask(page, 'Implement Subtract on the crew page', 'Ada', 'Rex');
  await projectNav(page, 'nav-team');
  const block = page.locator('[data-testid="crew-lane"]').filter({ hasText: 'Ada' }).getByTestId('crew-block').first();
  await expect(block).toHaveAttribute('title', /^Ada · [A-Z0-9]{2,5}-\d+ · \d\d:\d\d–(\d\d:\d\d|now) · (working now|finished|failed|cancelled|waiting on you|waited on you)$/, { timeout: 120_000 });
  const geometry = await block.evaluate((el) => ({ left: Number(el.getAttribute('data-left')), width: Number(el.getAttribute('data-width')) }));
  expect(geometry.left).toBeGreaterThanOrEqual(0);
  expect(geometry.left + geometry.width).toBeLessThanOrEqual(100.01);
  expect(geometry.width).toBeGreaterThan(0);
});

test("the workspace's Crew page: every project's agents grouped by project, their day on one timeline, and a card that opens its project", async ({ page }) => {
  test.setTimeout(180_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  await createProjectWithoutRepo(page, 'crew-ws-alpha');
  await createAgent(page, 'Alma', 'backend');
  await createProjectWithoutRepo(page, 'crew-ws-beta');
  await createAgent(page, 'Bruno', 'reviewer');

  // the Workspace group of the sidebar has Crew; it is a workspace page (the projects are lines, no project's tabs show)
  await workspaceNav(page, 'nav-crew');
  await expect(page).toHaveURL(/\/crew$/);
  await expect(sidebar(page).getByTestId('nav-crew')).toHaveAttribute('aria-current', 'page');
  await expect(sidebar(page).getByTestId('nav-overview')).toHaveCount(0);
  await expect(page.getByRole('heading', { level: 1, name: 'All crews' })).toBeVisible();
  await expect(page.getByTestId('crew-figures')).toHaveText(/^\d+ agents · \d+ working · \d+ waiting on you$/);

  // the cards are grouped by project, each group under its project's colour and name
  const alpha = page.getByTestId('crew-project').filter({ has: page.getByRole('heading', { name: /^crew-ws-alpha/ }) });
  const beta = page.getByTestId('crew-project').filter({ has: page.getByRole('heading', { name: /^crew-ws-beta/ }) });
  await expect(alpha.getByTestId('agent')).toHaveCount(1);
  await expect(alpha.getByTestId('agent')).toHaveAttribute('data-name', 'Alma');
  await expect(beta.getByTestId('agent')).toHaveAttribute('data-name', 'Bruno');
  await expect(alpha.locator('h2 .sq')).toBeVisible();
  // Today has a lane for each of them, marked with its project
  const lanes = page.getByRole('region', { name: 'Today' }).getByTestId('crew-lane');
  await expect(lanes.filter({ hasText: 'Alma' }).locator('.name')).toHaveAttribute('title', 'Alma (crew-ws-alpha)');
  await expect(lanes.filter({ hasText: 'Bruno' }).locator('.name')).toHaveAttribute('title', 'Bruno (crew-ws-beta)');
  expect(await smallText(page), 'workspace crew: text under 12 px').toEqual([]);

  // New agent asks which project
  await page.getByRole('button', { name: /^New agent/ }).click();
  const dialog = page.locator('dialog[open]');
  await expect(dialog.getByLabel('Project', { exact: true })).toBeVisible();
  await expect(dialog.getByTestId('describe-agent')).toBeVisible();
  await page.keyboard.press('Escape');

  // the beta project is open (it was made last); a card of alpha opens alpha and the agent's page
  await alpha.getByTestId('agent-link').click();
  await expect(page.getByRole('heading', { level: 1, name: 'Alma' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Switch project' })).toContainText('crew-ws-alpha');
  await expect(page.getByRole('navigation', { name: 'Crew' }).getByTestId('crew-link')).toHaveCount(1);

  // a reload of /crew opens the same page (the Go side serves index.html for it)
  await switchProject(page, 'crew-ws-beta');
  const u = new URL(page.url());
  u.pathname = '/crew';
  await page.goto(u.toString());
  await expect(page.getByRole('heading', { level: 1, name: 'All crews' })).toBeVisible();
  await expect(page.getByTestId('agent').filter({ hasText: 'Alma' })).toBeVisible();
});
