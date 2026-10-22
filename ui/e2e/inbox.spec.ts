// The Inbox against the real core (decision P97): a worker's tool permission request, raised through the permission prompt tool
// by the scripted agent (a TEST DOUBLE of the CLI, cmd/drydock-e2e "drydock-e2e inbox ask"), is listed with every project's
// items, described by approval.get in the right pane and approved from there for the rest of its task; the run then goes on to
// the review and merge, which the Inbox lists next and which the Running tab and My tasks reach too.
import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createProjectWithoutRepo, createProject, sidebar } from './helpers';

test("the Inbox approves a worker's permission request for its task, and the run goes on to the review", async ({ page }) => {
  await createProject(page, 'gocalc-inbox');
  await createAgent(page, 'Worker In', 'backend');
  await createAgent(page, 'Reviewer In', 'reviewer');
  await createAndStartTask(page, 'Fetch the page, drydock-e2e inbox ask', 'Worker In', 'Reviewer In');
  // the run has started (the start is not left in flight when the next page opens)
  await expect(page.getByRole('button', { name: 'Start' })).toHaveCount(0, { timeout: 30_000 });
  // a second project, open when the Inbox is: the Inbox is the workspace's, and lists the first project's request all the same
  await createProjectWithoutRepo(page, 'shop-inbox');

  await sidebar(page).getByTestId('nav-needs-you').click();
  await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible();
  await expect(page.getByRole('tab', { name: /^Needs you/ })).toHaveAttribute('aria-selected', 'true');
  const ask = page.getByTestId('needs-item').filter({ hasText: 'gocalc-inbox' }).filter({ hasText: 'curl https://example.com' });
  await expect(ask).toBeVisible({ timeout: 90_000 });
  await expect(ask).toContainText('Worker In asks to use Bash: curl https://example.com');
  await expect(ask.getByTestId('needs-sub')).toHaveText(/^gocalc-inbox · approval · asked /);
  await expect(ask.getByTestId('task-key')).toHaveText(/^[A-Z0-9]{2,5}-1$/);

  // the request as the core describes it
  await ask.getByRole('button').click();
  const pane = page.getByRole('complementary', { name: 'Selected item' });
  await expect(pane.getByTestId('detail-kind')).toHaveText('Approval · gocalc-inbox');
  await expect(pane.getByTestId('detail-description')).toContainText('Worker In wants to run curl https://example.com');
  await expect(pane.getByTestId('detail-facts')).toContainText('Worker In · Claude Code');
  await expect(pane.getByTestId('detail-input')).toHaveText('curl https://example.com');
  await expect(pane.getByTestId('detail-applies')).toHaveText(/^This exact call, until [A-Z0-9]{2,5}-1 ends$/);
  await expect(pane.getByTestId('detail-facts')).toContainText('Expiresin ');
  await expect(pane.getByTestId('detail-facts')).toContainText('Action hash');
  await expect(pane.getByTestId('detail-once')).toBeVisible();

  // A approves it for the rest of the task: it leaves the Inbox and the worker goes on
  await page.keyboard.press('a');
  await expect(ask).toHaveCount(0, { timeout: 30_000 });
  const merge = page.getByTestId('needs-item').filter({ hasText: 'gocalc-inbox' }).filter({ hasText: 'ready to merge' });
  await expect(merge).toBeVisible({ timeout: 90_000 });
  await merge.getByRole('button').click();
  await expect(pane.getByTestId('detail-kind')).toHaveText('Ready to merge · gocalc-inbox');
  await expect(pane.getByTestId('detail-approve')).toHaveText('Merge A');

  // the task's page lists the rule the approval made, and asks for the merge there too
  await pane.getByTestId('detail-open').click();
  await expect(page.getByRole('heading', { level: 1, name: /Fetch the page/ })).toBeVisible();
  await expect(page.getByTestId('review-panel')).toBeVisible();
  await expect(page.locator('main')).toContainText('curl https://example.com');
});

test('the Running tab and My tasks are the workspace pages the sidebar links', async ({ page }) => {
  await createProjectWithoutRepo(page, 'mine-inbox');
  await sidebar(page).getByTestId('nav-running').click();
  await expect(page.getByRole('heading', { level: 1, name: 'Inbox' })).toBeVisible();
  await expect(page.getByRole('tab', { name: /^Running/ })).toHaveAttribute('aria-selected', 'true');
  await expect(sidebar(page).getByTestId('nav-running')).toHaveAttribute('aria-current', 'page');
  await page.getByRole('tab', { name: /^Done today/ }).click();
  await expect(page.getByRole('heading', { level: 2, name: 'Done today' })).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-needs-you')).toHaveAttribute('aria-current', 'page');
  await sidebar(page).getByTestId('nav-my-tasks').click();
  await expect(page.getByRole('heading', { level: 1, name: 'My tasks' })).toBeVisible();
  // another spec may have handed a task to the person: either the list or the plain empty state
  await expect(page.getByTestId('my-task').or(page.getByText('No task is assigned to you')).first()).toBeVisible();
});
