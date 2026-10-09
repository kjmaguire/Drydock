import { expect, test, type Page } from '@playwright/test';
import { appUrl, createAgent, createAndStartTask, createProject, showNewProjectForm } from './helpers';

/** What the page shows about the task, used to compare before and after a reload (the projection property). */
async function snapshot(page: Page) {
  return {
    heading: await page.getByRole('heading', { level: 1 }).innerText(),
    status: await page.getByTestId('task-status').first().innerText(),
    evidence: await page.getByTestId('evidence').allInnerTexts(),
    reviews: await page.getByTestId('review').allInnerTexts(),
    meta: await page.locator('.task-meta').innerText(),
  };
}

async function openTaskChannel(page: Page) {
  await page.getByRole('link', { name: 'Open task channel' }).click();
  await expect(page.getByRole('log')).toBeVisible();
}

test.describe('the product loop against the real Go core (scripted fake agent)', () => {
  test('create project and team, run a task to review, check the channel, approve, reload', async ({ page }) => {
    await createProject(page, 'gocalc-approve');
    await createAgent(page, 'Worker One', 'backend');
    await createAgent(page, 'Reviewer One', 'reviewer');
    await createAndStartTask(page, 'Implement Subtract', 'Worker One', 'Reviewer One');

    // the human review panel appears when the run raises its review request
    const panel = page.getByTestId('review-panel');
    await expect(panel).toBeVisible({ timeout: 90_000 });
    await expect(panel.getByTestId('pending-request')).toHaveAttribute('data-kind', 'review');
    // the Needs you badge counts it
    await expect(page.getByTestId('needs-count')).not.toHaveText('0');

    // evidence with pass/fail, the reviewer verdict and a diff
    await expect(page.getByTestId('evidence').filter({ hasText: 'test' }).first()).toHaveAttribute('data-result', 'pass');
    await expect(page.getByTestId('review').first()).toHaveAttribute('data-verdict', 'SHIP');
    await page.getByRole('button', { name: 'Show the raw patch' }).click();
    await expect(page.locator('.diff-add', { hasText: 'return a - b' })).toBeVisible();

    // the task channel: Tests check card, the worker's message, the reviewer's verdict
    await openTaskChannel(page);
    await expect(page.getByTestId('check-card').first()).toBeVisible();
    await expect(page.locator('[data-testid=check-card][data-tool=Tests]').first()).toHaveAttribute('data-status', /pass|fail/);
    await expect(page.locator('[data-testid=check-card][data-tool=Tests][data-status=pass]').last()).toBeVisible();
    await expect(page.locator('[data-testid=message][data-author-kind=tool]').first()).toBeVisible();
    await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'Worker One' }).filter({ hasText: 'implemented Subtract' })).toBeVisible();
    await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'Reviewer One' }).filter({ hasText: 'SHIP' })).toBeVisible();

    // presence dots come from presence.list plus live events: some agent is no longer "Unknown"
    await expect.poll(async () => (await page.getByTestId('presence').allInnerTexts()).filter((t) => !/Unknown/.test(t)).length, { timeout: 30_000 }).toBeGreaterThan(0);

    // approve with the button bound to the request
    await page.getByRole('link', { name: 'Task details' }).click();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });
    await expect(page.getByTestId('review-panel')).toHaveCount(0);
    await expect(page.getByTestId('needs-count')).toHaveText('0');

    // reload: everything is a projection of the core, so the page shows the same state
    const before = await snapshot(page);
    await page.reload();
    await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/);
    await expect.poll(() => snapshot(page)).toEqual(before);
    await expect(page.getByTestId('needs-count')).toHaveText('0');
  });

  test('request changes with feedback, the worker reworks, then approve', async ({ page }) => {
    await createProject(page, 'gocalc-changes');
    await createAgent(page, 'Worker Two', 'backend');
    await createAgent(page, 'Reviewer Two', 'reviewer');
    await createAndStartTask(page, 'Implement Subtract again', 'Worker Two', 'Reviewer Two');

    const panel = page.getByTestId('review-panel');
    await expect(panel).toBeVisible({ timeout: 90_000 });
    const firstRequest = await panel.getByTestId('pending-request').getAttribute('data-request-id');

    await panel.getByRole('button', { name: 'Request changes' }).click();
    await panel.getByLabel('Feedback for the worker').fill('please add a doc comment to Subtract');
    await panel.getByRole('button', { name: 'Send feedback' }).click();

    // the worker is sent back and a NEW review request (new id) is raised after the rework
    await expect(async () => {
      const id = await page.getByTestId('pending-request').getAttribute('data-request-id', { timeout: 1000 });
      expect(id).toBeTruthy();
      expect(id).not.toBe(firstRequest);
    }).toPass({ timeout: 90_000 });

    await page.getByRole('button', { name: 'Show the raw patch' }).click();
    await expect(page.locator('.diff-add', { hasText: 'Subtract returns a minus b' })).toBeVisible();

    // the human's feedback reached the worker through the core: the channel shows the reworked report
    await openTaskChannel(page);
    await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'added the doc comment' })).toBeVisible();
    await page.getByRole('link', { name: 'Task details' }).click();

    await page.getByTestId('review-panel').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });
    await expect(page.getByTestId('review-panel')).toHaveCount(0);
  });

  test('the core refuses a bad repository path and the UI shows its message', async ({ page }) => {
    await page.goto(appUrl());
    await showNewProjectForm(page);
    await page.getByLabel('Repository path').fill('/definitely/not/a/repo');
    await page.getByRole('button', { name: 'Create project' }).click();
    await expect(page.getByRole('alert')).toContainText('not a usable Git repository');
  });
});

test('project delivery settings and the Pull Requests view show the core\'s honest answers (no GitHub origin in this repo)', async ({ page }) => {
  await createProject(page, 'gocalc-pr');
  await page.getByRole('link', { name: 'Project settings' }).click();
  await page.getByLabel('Mode', { exact: true }).selectOption('pr_flow');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('pr_flow is not available');
  await page.getByRole('link', { name: 'Pull Requests' }).click();
  await expect(page.getByText('No task has a pull request')).toBeVisible();
  await page.getByRole('button', { name: 'Load open issues' }).click();
  await expect(page.getByRole('alert')).toContainText('origin');
});

test('the sidebar is keyboard navigable and Needs you comes first', async ({ page }) => {
  await createProject(page, 'gocalc-keys');
  const items = page.locator('nav[aria-label=Main] [data-nav]');
  await expect(items.first()).toHaveAttribute('href', '#/project/new');
  await page.getByTestId('nav-needs-you').focus();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('link', { name: '# alerts' })).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(page.getByTestId('nav-needs-you')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'Needs you' })).toBeVisible();
});
