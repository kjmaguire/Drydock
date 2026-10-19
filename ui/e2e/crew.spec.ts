import { expect, test, type Page } from '@playwright/test';
import { createAgent, createProject, projectNav } from './helpers';

// The Crew pages (Crew HQ, decision P64) against the real Go core: the crew as dots, and an agent's own page.

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

test("the Crew page shows each agent as a dot with its name linking to the agent's page: hero, chips, crew list, tabs, and the palette finds it", async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await createProject(page, 'gocalc-crew');
  await createAgent(page, 'Ada', 'backend');
  await createAgent(page, 'Rex', 'reviewer');

  // the Crew page: a dot per agent, what it is doing in words, the role, and its name a link
  await projectNav(page, 'nav-team');
  await expect(page.getByRole('heading', { level: 1, name: 'Crew' })).toBeVisible();
  const card = page.getByTestId('agent').filter({ hasText: 'Ada' });
  await expect(card.locator('[data-dot="true"]')).toBeVisible();
  await expect(card).toContainText('backend');
  await expect(card.locator('.now')).toBeVisible();
  await expect(card.getByRole('button', { name: 'Inspect' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Edit Ada' })).toBeVisible();
  await card.getByTestId('agent-link').click();

  // the agent's page
  await expect(page).toHaveURL(/\/team\/[^/]+$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Ada' })).toBeVisible();
  const chips = page.getByTestId('agent-chip');
  // its CLI and model (the helper creates it on the adapter's default model); the core says whether it is held in
  await expect(chips.filter({ hasText: /^Claude Code · (default model|sonnet)$/ })).toBeVisible();
  await expect(chips.filter({ hasText: /guarded|not held in/ })).toBeVisible();
  await expect(page.getByTestId('agent-now')).toContainText('Not on a task right now');
  await expect(page.getByTestId('stat-merged')).toContainText('0');
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
  await expect(page.locator('section[aria-label="Skills"]')).toContainText('No skill is accepted in this project yet');
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
});
