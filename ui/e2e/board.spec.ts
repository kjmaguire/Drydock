import { expect, test, type Locator } from '@playwright/test';
import { createAgent, createAndStartTask, createProject, projectNav } from './helpers';

/** Pick an item of a menu. A menu still closing after its last choice ignores a click on its opener, as it does for a person. */
// Chooses an item from a menu. `chosen` says the choice took effect (the filter chip's words): a click on a slow runner can land
// while the menu is still placing itself, so the choice is made again until it shows, not only until a click went through.
async function fromMenu(trigger: Locator, item: Locator, chosen?: () => Promise<void>): Promise<void> {
  await expect(async () => {
    if (!(await item.isVisible())) await trigger.click();
    await item.click({ timeout: 2_000 });
    if (chosen) await chosen();
  }).toPass({ timeout: 20_000 });
}

// The project board (graphite, decision P97) against the real Go core and the scripted agent: a task the crew ran to its merge
// request sits in Review as "Ready to merge" with its checks, verdict and diff; a new task (C) waits in Spec; the filters, the
// labels, the list and the timeline show the same tasks; Merge answers the request and the task moves to Merged.
test('the board: columns from the steps, a ready-to-merge card, C for a new task, filters, labels, views, and Merge', async ({ page }) => {
  test.setTimeout(300_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await createProject(page, 'gocalc-board');
  await createAgent(page, 'Worker B', 'backend');
  await createAgent(page, 'Reviewer B', 'reviewer');
  await createAndStartTask(page, 'Implement Subtract', 'Worker B', 'Reviewer B');
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 90_000 });

  // C opens a new task; it is created and waits in Spec
  await projectNav(page, 'nav-tasks');
  await expect(page.getByTestId('task-board')).toBeVisible();
  await page.keyboard.press('c');
  await expect(page).toHaveURL(/\/tasks\/new$/);
  await page.getByLabel('Task title').fill('Write the README');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Write the README/ })).toBeVisible();
  await projectNav(page, 'nav-tasks');

  // the bar
  await expect(page.getByRole('heading', { level: 1, name: 'gocalc-board' })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'Board' })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('crew-line')).toContainText('1 waiting on you');

  const columns = page.getByTestId('board-column');
  await expect(columns).toHaveCount(5);
  expect(await columns.evaluateAll((cs) => cs.map((c) => c.getAttribute('aria-label')))).toEqual(['Spec', 'Building', 'Checks', 'Review', 'Merged']);
  const col = (name: string) => page.locator(`[data-testid="board-column"][data-column="${name}"]`);
  const cardOf = (title: string) => page.getByTestId('task-row').filter({ hasText: title });

  // the task the crew ran: in Review, ready to merge, with its checks, the reviewer's verdict and the diff
  const ship = col('review').getByTestId('task-row').filter({ hasText: 'Implement Subtract' });
  await expect(ship).toBeVisible();
  await expect(ship.getByTestId('card-tag')).toHaveText('Ready to merge');
  await expect(ship).toHaveAttribute('data-waiting', 'true');
  await expect(ship.locator('.key')).toHaveText(/^[A-Z0-9]{2,5}-1$/);
  await expect(ship.getByTestId('checks-bar')).toHaveAttribute('aria-label', /^Checks: \d+ passed/);
  await expect(ship.getByTestId('verdict')).toContainText('Reviewer B approved');
  await expect(ship.getByTestId('verdict')).toContainText(/\+\d+\s−\d+/);
  await expect(ship.getByRole('link', { name: /View diff of/ })).toHaveAttribute('href', /\/task\//);
  // the new task: in Spec, in the backlog, nobody on it yet
  const readme = col('spec').getByTestId('task-row').filter({ hasText: 'Write the README' });
  await expect(readme.getByTestId('card-tag')).toHaveText('Backlog');
  await expect(readme).toContainText('Not assigned yet');

  // Needs me: only what waits on the person
  await page.getByTestId('filter-needs-me').click();
  await expect(page.getByTestId('task-row')).toHaveCount(1);
  await expect(cardOf('Implement Subtract')).toHaveCount(1);
  await page.getByTestId('filter-needs-me').click();
  await expect(page.getByTestId('task-row')).toHaveCount(2);

  // Agent: the tasks of one agent
  const agentChip = page.getByTestId('filter-agent');
  await fromMenu(agentChip, page.getByRole('menuitemradio', { name: 'Worker B' }), () => expect(agentChip).toContainText('Agent: Worker B', { timeout: 2_000 }));
  await expect(page.getByTestId('task-row')).toHaveCount(1);
  await expect(cardOf('Implement Subtract')).toHaveCount(1);
  await fromMenu(agentChip, page.getByRole('menuitemradio', { name: 'Any agent' }), () => expect(agentChip).toHaveText('Agent', { timeout: 2_000 }));
  await expect(page.getByTestId('task-row')).toHaveCount(2);

  // a card's labels, from its menu; then the Label filter has it
  const key = (await readme.locator('.key').innerText()).trim();
  await fromMenu(readme.getByRole('button', { name: `More for ${key}` }), page.getByRole('menu', { name: `More for ${key}` }).getByRole('menuitem', { name: 'Labels...' }));
  const dialog = page.getByRole('dialog', { name: `Labels of ${key}` });
  await dialog.getByLabel('Labels').fill('Docs, first-run');
  await dialog.getByRole('button', { name: 'Save labels' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(readme.getByTestId('card-label')).toHaveText(['docs', 'first-run']);
  const labelChip = page.getByTestId('filter-label');
  await fromMenu(labelChip, page.getByRole('menuitemradio', { name: 'docs', exact: true }), () => expect(labelChip).toContainText('Label: docs', { timeout: 2_000 }));
  await expect(page.getByTestId('task-row')).toHaveCount(1);
  await expect(cardOf('Write the README')).toHaveCount(1);
  await fromMenu(labelChip, page.getByRole('menuitemradio', { name: 'Any label' }), () => expect(labelChip).toHaveText('Label', { timeout: 2_000 }));
  await expect(page.getByTestId('task-row')).toHaveCount(2);

  // the list: the same tasks as rows
  await page.getByTestId('task-view-list').click();
  await expect(page.getByTestId('task-list')).toBeVisible();
  await expect(cardOf('Implement Subtract').locator('.stage')).toHaveText('Review');
  await expect(cardOf('Write the README').locator('.stage')).toHaveText('Spec');
  // the timeline: the task that ran is a block in its worker's lane; the one not started is said to be left out
  await page.getByTestId('task-view-timeline').click();
  const timeline = page.getByTestId('task-timeline');
  await expect(timeline.getByTestId('timeline-lane').filter({ hasText: 'Worker B' }).getByTestId('timeline-block')).toHaveCount(1);
  await expect(timeline).toContainText('1 task not started yet');
  await page.getByTestId('task-view-board').click();

  // Merge answers the merge request: the task is merged, and Merged counts it this week
  await ship.getByRole('button', { name: /^Merge / }).click();
  const merged = col('merged').getByTestId('task-row').filter({ hasText: 'Implement Subtract' });
  await expect(merged).toBeVisible({ timeout: 60_000 });
  await expect(merged).toContainText('Today');
  await expect(col('merged').getByTestId('column-count')).toHaveText('1 this week');
  await expect(page.getByTestId('crew-line')).toContainText('0 waiting on you');
  await merged.getByRole('link', { name: 'Implement Subtract' }).click();
  await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });
});
