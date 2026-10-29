// The task page's live run (the graphite mockup "Task", decision P97) against the real Go core with the scripted agent (a TEST
// DOUBLE, cmd/drydock-e2e: a task whose description carries "drydock-e2e live run" reads calc.go and keeps working until it is
// interrupted; a steer that carries "drydock-e2e steered" makes it run the tests and keep working). The page shows the run as it
// goes, steers the agent (it reads the message before its next step), pauses it (the pill says who paused it) and resumes it,
// opens the worktree's terminal with O, and cancels the task; before it starts, the person marks its criteria (P125).
import { expect, test, type Locator } from '@playwright/test';
import { choose, createAgent, createProject, projectNav } from './helpers';

/** Open a kit menu by its trigger (repeated while it is still closing) and choose an item. */
async function fromMenu(trigger: Locator, item: Locator): Promise<void> {
  await expect(async () => {
    if (!(await item.isVisible())) await trigger.click();
    await item.click({ timeout: 2_000 });
  }).toPass({ timeout: 20_000 });
}

test('steer, pause and resume a running task from its page', async ({ page }) => {
  test.setTimeout(240_000);
  await page.setViewportSize({ width: 1366, height: 900 });
  await createProject(page, 'gocalc-live');
  await createAgent(page, 'Builder', 'backend');
  await createAgent(page, 'Rex', 'reviewer');

  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Parse negative exponents');
  await page.getByLabel('Description').fill('"2e-3" should read as 0.002. drydock-e2e live run');
  await page.getByLabel('Acceptance criteria').fill('Given 2e-3 When parsed Then 0.002\nGiven 2e- When parsed Then it says what is missing');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Parse negative exponents' })).toBeVisible();

  // before it runs: the criteria are open, the steer box is off and says why
  const criteria = page.getByTestId('task-criteria').getByRole('listitem');
  await expect(criteria).toHaveCount(2);
  await expect(criteria.first()).toHaveAttribute('data-state', 'open');
  // the person's word on a criterion (P125): met, a waiver with its reason, then the first reopened; the core keeps each
  await fromMenu(page.getByRole('button', { name: 'Mark criterion 1' }), page.getByRole('menuitem', { name: 'Mark as met' }));
  await expect(criteria.first()).toHaveAttribute('data-state', 'met');
  await expect(criteria.first().getByTestId('criterion-why')).toHaveText(/^Confirmed by \S+\.$/);
  await fromMenu(page.getByRole('button', { name: 'Mark criterion 2' }), page.getByRole('menuitem', { name: 'Waive...' }));
  const waive = page.getByRole('form', { name: 'Waive criterion 2' });
  await waive.getByLabel('Why is it waived?').fill('the error text is covered by the parser task');
  await waive.getByRole('button', { name: 'Waive' }).click();
  await expect(criteria.nth(1)).toHaveAttribute('data-state', 'waived');
  await expect(criteria.nth(1).getByTestId('criterion-why')).toHaveText(/^Waived by \S+: the error text is covered by the parser task\.$/);
  await page.reload();
  await expect(criteria.nth(1)).toHaveAttribute('data-state', 'waived');
  await fromMenu(page.getByRole('button', { name: 'Mark criterion 1' }), page.getByRole('menuitem', { name: 'Reopen' }));
  await expect(criteria.first()).toHaveAttribute('data-state', 'open');
  const steer = page.getByLabel('Message the agent');
  await expect(steer).toBeDisabled();
  await expect(page.getByTestId('steer')).toContainText('Start the task to steer its agent.');
  // the breadcrumb: the project, then the task's key
  await expect(page.locator('article header').getByRole('link', { name: 'gocalc-live' })).toHaveAttribute('href', '/tasks');
  await expect(page.getByTestId('task-key')).toHaveText(/^[A-Z0-9]{2,5}-\d+$/);

  await choose(page.getByLabel('Worker', { exact: true }), { label: 'Builder (backend)' });
  await choose(page.getByLabel('Reviewer', { exact: true }), { label: 'Rex (reviewer)' });
  await page.getByRole('button', { name: 'Assign' }).click();
  await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Start', exact: true }).click();

  // the run: Building · run 1, the agent line, the step it took
  const pill = page.getByTestId('task-status');
  await expect(pill).toHaveText('Building · run 1', { timeout: 90_000 });
  const activity = page.getByTestId('task-activity');
  await expect(activity.getByTestId('activity-agent')).toContainText('Builder · Claude Code');
  const read = activity.locator('[data-testid=activity-step][data-kind=read]');
  await expect(read).toContainText('Read', { timeout: 60_000 });
  await expect(read).toContainText('calc.go');
  await expect(page.getByTestId('task-runs-on')).toHaveText(/^Claude Code/);
  await expect(page.locator('.task-meta code')).toHaveText(/^drydock\//);

  // steer: Ctrl+Enter sends it; the agent reads it before its next step and does what it says
  await expect(steer).toBeEnabled();
  await expect(steer).toHaveAttribute('placeholder', 'Steer Builder: it reads this before its next step');
  await steer.fill('drydock-e2e steered: run the tests first');
  await steer.press('Control+Enter');
  await expect(page.getByTestId('steer').getByRole('status')).toHaveText('Sent. Builder reads it before its next step.', { timeout: 30_000 });
  await expect(steer).toHaveValue('');
  const ran = activity.locator('[data-testid=activity-step][data-kind=run]');
  await expect(ran.first()).toContainText('go test ./...', { timeout: 60_000 });

  // pause: the pill says who paused it, the button resumes
  const box = page.getByTestId('steer');
  await box.getByRole('button', { name: 'Pause' }).click();
  await expect(pill).toHaveText(/^Paused by \S+ · run 1$/, { timeout: 30_000 });
  await expect(page.getByTestId('task-state')).toHaveText(/^Paused by \S+$/);
  await expect(box.getByRole('button', { name: 'Resume' })).toBeEnabled();
  // a reload shows the same: the page is the core's state
  await page.reload();
  await expect(pill).toHaveText(/^Paused by \S+ · run 1$/);
  await page.getByTestId('steer').getByRole('button', { name: 'Resume' }).click();
  await expect(pill).toHaveText('Building · run 1', { timeout: 30_000 });
  await expect(page.getByTestId('steer').getByRole('button', { name: 'Pause' })).toBeVisible();

  // O opens a terminal in the task's worktree, in the dock
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press('o');
  await expect(page.getByTestId('terminal-panel')).toBeVisible();
  await expect(page.getByTestId('terminal-panel').getByRole('tab', { selected: true })).toBeVisible({ timeout: 30_000 });

  // cancel the task: the run stops and the steer box says there is nothing to steer
  await page.getByRole('button', { name: 'Cancel task' }).click();
  await page.getByRole('button', { name: 'Yes, cancel task' }).click();
  await expect(pill).toHaveText(/^Cancelled/, { timeout: 60_000 });
  await expect(page.getByLabel('Message the agent')).toBeDisabled();
});
