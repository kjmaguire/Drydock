import { expect, test, type Page } from '@playwright/test';
import { choose, appUrl, createAgent, createAndStartTask, createProject, showNewProjectForm, wizardNext, projectNav, openChecks } from './helpers';

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

    // Activity (P87): the worker's and the reviewer's processes, each turn read from the core's typed activity
    const activity = page.getByTestId('task-activity');
    await expect(activity.getByTestId('activity-thread')).toHaveCount(2);
    await expect(activity.getByTestId('activity-thread').filter({ hasText: 'Worker One' }).getByTestId('activity-text').filter({ hasText: 'implemented Subtract' })).toBeVisible();
    await expect(activity.getByTestId('activity-thread').filter({ hasText: 'Reviewer One' })).toContainText('SHIP');

    // the task channel: Tests check card, the worker's message, the reviewer's verdict
    await openTaskChannel(page);
    await openChecks(page);
    await expect(page.getByTestId('check-card').first()).toBeVisible();
    await expect(page.locator('[data-testid=check-card][data-tool=Tests]').first()).toHaveAttribute('data-status', /pass|fail/);
    await expect(page.locator('[data-testid=check-card][data-tool=Tests][data-status=pass]').last()).toBeVisible();
    await expect(page.locator('[data-testid=message][data-author-kind=tool]').first()).toBeVisible();
    await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'Worker One' }).filter({ hasText: 'implemented Subtract' })).toBeVisible();
    await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'Reviewer One' }).filter({ hasText: 'SHIP' })).toBeVisible();

    // presence dots come from presence.list plus live events: some agent has a presence from the core, and every dot says one
    // of the status words (P86)
    await expect.poll(async () => (await page.getByTestId('presence').evaluateAll((ns) => ns.map((n) => n.getAttribute('data-presence')))).filter(Boolean).length, { timeout: 30_000 }).toBeGreaterThan(0);
    for (const t of await page.getByTestId('presence').allInnerTexts()) expect(t.trim()).toMatch(/^(Working|Waiting for you|Waiting|Starting|Idle|Error|Stopped)$/);

    // approve with the button bound to the request
    await page.getByRole('link', { name: 'Task details' }).click();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });
    await expect(page.getByTestId('review-panel')).toHaveCount(0);

    // reload: everything is a projection of the core, so the page shows the same state. The task turns Completed a moment
    // before the run commits its own completion (both arrive as events), so the comparison starts once the run says so too.
    await expect(page.locator('.task-meta')).toContainText('Completed', { timeout: 30_000 });
    const before = await snapshot(page);
    await page.reload();
    await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/);
    await expect.poll(() => snapshot(page)).toEqual(before);
    // nothing of the project waits in the Inbox (the sidebar's count is every project's, and other specs leave theirs)
    await page.getByTestId('nav-needs-you').click();
    await expect(page.getByTestId('inbox-needs')).toBeVisible();
    await expect(page.getByTestId('needs-item').filter({ hasText: 'gocalc-approve' })).toHaveCount(0);
  });

  test('request changes with feedback, the worker reworks, then approve', async ({ page }) => {
    await createProject(page, 'gocalc-changes');
    await createAgent(page, 'Worker Two', 'backend');
    await createAgent(page, 'Reviewer Two', 'reviewer');
    await createAndStartTask(page, 'Implement Subtract again', 'Worker Two', 'Reviewer Two');

    const panel = page.getByTestId('review-panel');
    await expect(panel).toBeVisible({ timeout: 90_000 });
    const firstRequest = await panel.getByTestId('pending-request').getAttribute('data-request-id');

    // the raw patch is asked for by the id of the diff evidence the card is about (L36)
    const diffAsks: { id: string; evidenceId?: number }[] = [];
    page.on('request', (req) => {
      // task.diff itself; the dock's task.diffFiles and task.diffFile are other calls
      if (/\/rpc\/task\.diff(\?|$)/.test(req.url())) diffAsks.push(JSON.parse(req.postData() ?? '{}'));
    });
    await page.getByRole('button', { name: 'Show the raw patch' }).click();
    await expect(page.getByTestId('diff')).toBeVisible();
    expect(diffAsks).toHaveLength(1);
    expect(typeof diffAsks[0]!.evidenceId).toBe('number');

    await panel.getByRole('button', { name: 'Request changes' }).click();
    await panel.getByLabel('Feedback for the worker').fill('please add a doc comment to Subtract');
    await panel.getByRole('button', { name: 'Send feedback' }).click();

    // the worker is sent back and a NEW review request (new id) is raised after the rework
    await expect(async () => {
      const id = await page.getByTestId('pending-request').getAttribute('data-request-id', { timeout: 1000 });
      expect(id).toBeTruthy();
      expect(id).not.toBe(firstRequest);
    }).toPass({ timeout: 90_000 });

    // the patch the rework recorded is not swapped in under the reader's eyes: the old text is gone, the page says so, and the
    // button asks for the newer evidence (L36)
    await expect(page.getByTestId('raw-diff-changed')).toHaveText('The patch changed since you opened it. Show it again.');
    await expect(page.getByTestId('diff')).toHaveCount(0);
    await page.getByRole('button', { name: 'Show the raw patch' }).click();
    await expect(page.locator('.diff-add', { hasText: 'Subtract returns a minus b' })).toBeVisible();
    expect(diffAsks).toHaveLength(2);
    expect(diffAsks[1]!.evidenceId!).toBeGreaterThan(diffAsks[0]!.evidenceId!);

    // the human's feedback reached the worker through the core: the channel shows the reworked report
    await openTaskChannel(page);
    await expect(page.locator('[data-testid=message][data-author-kind=agent]').filter({ hasText: 'added the doc comment' })).toBeVisible();
    await page.getByRole('link', { name: 'Task details' }).click();

    await page.getByTestId('review-panel').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });
    await expect(page.getByTestId('review-panel')).toHaveCount(0);
  });

  test('the core says what is at a bad repository path and the wizard does not go on without a repository', async ({ page }) => {
    await page.goto(appUrl());
    await showNewProjectForm(page);
    await wizardNext(page);
    await page.getByRole('radiogroup', { name: 'Repository source' }).getByRole('radio', { name: 'Local folder' }).check();
    // a full path on this system (Windows refuses /x as not a full path before it looks for a repository)
    await page.getByLabel('Repository path').fill(process.platform === 'win32' ? 'C:\\definitely\\not\\a\\repo' : '/definitely/not/a/repo');
    await expect(page.getByTestId('folder-check')).toHaveAttribute('data-state', 'missing');
    await expect(page.getByTestId('folder-check')).toContainText('does not exist yet');
    await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled();
    await expect(page.getByText('tick "Create a Git repository here"')).toBeVisible();
    // a relative path is refused with the core's reason
    await page.getByLabel('Repository path').fill('not/a/full/path');
    await expect(page.getByTestId('folder-check')).toHaveAttribute('data-state', 'invalid');
    await expect(page.getByTestId('folder-check')).toContainText('must be a full path');
    await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled();
  });
});

test('project delivery settings and the Pull Requests view show the core\'s honest answers (no GitHub origin in this repo)', async ({ page }) => {
  await createProject(page, 'gocalc-pr');
  await projectNav(page, 'nav-project');
  await choose(page.getByLabel('Mode', { exact: true }), 'pr_flow');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('pr_flow is not available');
  await projectNav(page, 'nav-pulls');
  await expect(page.getByText('No task has a pull request')).toBeVisible();
  await page.getByRole('button', { name: 'Load open issues' }).click();
  await expect(page.getByRole('alert')).toContainText('origin');
});

test("the sidebar is keyboard navigable: the Drydock mark comes first (the way home), the arrows move along it, Enter opens the Inbox", async ({ page }) => {
  await createProject(page, 'gocalc-keys');
  const items = page.locator('nav[aria-label=Main] [data-nav]');
  await expect(items.first()).toHaveAttribute('href', '/home');
  await expect(items.nth(1)).toHaveAttribute('href', '/needs-you');
  await page.getByTestId('nav-needs-you').focus();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByTestId('nav-running')).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByTestId('nav-my-tasks')).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByTestId('nav-chats')).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByTestId('nav-project-item').first()).toBeFocused();
  await page.keyboard.press('End');
  await expect(page.getByTestId('nav-guide-top')).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(page.getByTestId('nav-settings')).toBeFocused();
  await page.getByTestId('nav-running').focus();
  await page.keyboard.press('ArrowUp');
  await expect(page.getByTestId('nav-needs-you')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible();
});
