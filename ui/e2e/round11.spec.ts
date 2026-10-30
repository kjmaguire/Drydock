import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createProject } from './helpers';

// The workflow graph and the run explanation against the real Go core: React Flow draws the run's graph with each node's
// state, the checkpoint timeline shows the run as it was, and each turn shows its manifest and exact prompt.
test('workflow graph, checkpoint timeline and the explanation of every turn', async ({ page }) => {
  test.slow(); // the task runs three times: the first run, a time travel and a replay (P123)
  await createProject(page, 'gocalc-explain');
  await createAgent(page, 'Worker One', 'backend');
  await createAgent(page, 'Reviewer One', 'reviewer');
  await createAndStartTask(page, 'Implement Subtract', 'Worker One', 'Reviewer One');
  await expect(page.getByTestId('review-panel')).toBeVisible({ timeout: 90_000 });

  await page.getByRole('link', { name: 'Workflow and explanation' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Run of #\d+ Implement Subtract/ })).toBeVisible();
  const graph = page.getByTestId('workflow-graph');
  await expect(graph.locator('[data-testid=wf-node][data-node=work]')).toHaveAttribute('data-status', 'done');
  await expect(graph.locator('[data-testid=wf-node][data-node=work]')).toContainText('Worker One');
  await expect(graph.getByTestId('wf-edge').first()).toBeAttached();

  // the first checkpoint: nothing has run yet
  await graph.getByTestId('wf-checkpoint').first().click();
  await expect(graph.getByText(/As of checkpoint at step/)).toBeVisible();
  await expect(graph.locator('[data-testid=wf-node][data-node=work]')).toHaveAttribute('data-status', 'pending');
  await graph.getByRole('button', { name: 'Show the latest' }).click();
  await expect(graph.locator('[data-testid=wf-node][data-node=work]')).toHaveAttribute('data-status', 'done');

  // every turn in detail: what the worker was given and the exact prompt
  await page.getByRole('tab', { name: /Turns/ }).click();
  const work = page.locator('[data-testid=turn][data-node=work]').first();
  await work.locator('button').first().click();
  await expect(work.locator('[data-testid=manifest-item][data-included=true]').first()).toContainText('prompt');
  await expect(work.locator('[data-testid=manifest-item][data-included=false]').first()).toBeVisible();
  await work.getByRole('button', { name: 'Show the exact prompt' }).click();
  await expect(work.getByTestId('prompt-text')).toContainText('Subtract');

  await page.getByRole('tab', { name: /Checkpoints/ }).click();
  await expect(page.locator('[data-testid=checkpoint][data-latest=true]')).toHaveCount(1);

  // P123: a run waiting for you is not resumed (it says why); time travel from the checkpoint after prepare stops it and a new
  // run goes on from there, to the approval again
  const actions = page.getByTestId('run-actions');
  await expect(actions.getByRole('button', { name: 'Resume' })).toBeDisabled();
  await expect(page.getByTestId('run-actions-why')).toContainText('waiting for you');
  const firstRun = (await page.getByTestId('run-id').textContent())!;
  await graph.getByTestId('wf-checkpoint').nth(1).click();
  await expect(graph.getByText(/As of checkpoint at step 1/)).toBeVisible();
  await graph.getByRole('button', { name: 'Time travel from here' }).click();
  const confirm = page.getByTestId('rerun-confirm');
  await expect(confirm).toContainText('Runs first: work.');
  await expect(confirm).toContainText('This run (waiting for you) is stopped first');
  await confirm.getByRole('button', { name: 'Start the new run' }).click();
  await expect(page.getByTestId('run-origin')).toContainText('Time travel from step 1 of an earlier run');
  await expect(page.getByTestId('run-id')).not.toHaveText(firstRun);
  await expect(graph.locator('[data-testid=wf-node][data-node=approve]')).toHaveAttribute('data-status', 'waiting', { timeout: 90_000 });
  await expect(page.getByTestId('run-history')).toContainText('This task ran 2 times');

  // the earlier run is kept as it was, cancelled; a replay of it asks before the merge and goes to the approval again
  await page.getByTestId('run-origin').getByRole('link').click();
  await expect(page.getByTestId('run-id')).toHaveText(firstRun);
  await expect(page.getByTestId('run-status')).toHaveText('Cancelled');
  await actions.getByRole('button', { name: 'Replay from the start' }).click();
  await expect(confirm).toContainText("merge (merges the task's branch into its target branch)");
  await confirm.getByRole('button', { name: 'Start the replay' }).click();
  await expect(page.getByTestId('run-origin')).toContainText('Replay of an earlier run from step 0');
  await expect(graph.locator('[data-testid=wf-node][data-node=approve]')).toHaveAttribute('data-status', 'waiting', { timeout: 90_000 });
  await expect(page.getByTestId('run-history')).toContainText('This task ran 3 times');
});

// P124 against the real core, a real worktree and the scripted agent: the worktree's files go back to the safe point taken
// before the work turn (listed first, then undone), and a cancelled task runs again as a new task with the same work.
test("the worktree's files back to a safe point, and a cancelled task run again", async ({ page }) => {
  test.slow(); // the task runs twice: once, then again as a new task
  await createProject(page, 'gocalc-restore');
  await createAgent(page, 'Worker One', 'backend');
  await createAgent(page, 'Reviewer One', 'reviewer');
  await createAndStartTask(page, 'Implement Subtract', 'Worker One', 'Reviewer One');
  await expect(page.getByTestId('review-panel')).toBeVisible({ timeout: 90_000 });
  const taskUrl = page.url();

  await page.getByRole('link', { name: 'Workflow and explanation' }).click();
  const actions = page.getByTestId('run-actions');
  await actions.getByRole('button', { name: 'Put files back...' }).click();
  const dialog = page.getByTestId('restore-dialog');
  await expect(dialog.getByRole('button', { name: 'Put the files back' })).toBeDisabled();
  await dialog.getByLabel(/^Before work at step \d+$/).check();
  await expect(dialog.getByTestId('restore-files')).toContainText('calc.go');
  await dialog.getByRole('button', { name: 'Put the files back' }).click();
  await expect(dialog.getByTestId('restore-done')).toContainText(/Put back: \d+ files? changed\. What was there before is kept as refs\/drydock\/safe\//);
  await dialog.getByRole('button', { name: 'Undo the restore' }).click();
  await expect(dialog.getByTestId('restore-done')).toContainText('Undone: the files are back as they were before the restore');
  await dialog.getByRole('button', { name: 'Close' }).click();

  // later, the dialog closed and the page read again (P127): both restores' undo points are listed beside the safe points,
  // and the newest (the files before the undo) is put back with the same preview
  await page.reload();
  await actions.getByRole('button', { name: 'Put files back...' }).click();
  const undos = dialog.getByTestId('undo-points');
  await expect(undos.getByRole('radio')).toHaveCount(2);
  await undos.getByLabel(/before the restore of .* \(to an earlier undo point\)$/).check();
  await expect(dialog.getByTestId('restore-files')).toContainText('calc.go');
  await dialog.getByRole('button', { name: 'Put the files back' }).click();
  await expect(dialog.getByTestId('restore-done')).toContainText(/Put back: \d+ files? changed\./);
  await dialog.getByRole('button', { name: 'Close' }).click();

  // the task is cancelled; its run page offers to run it again, as a new task that starts from the beginning
  await page.goto(taskUrl);
  await page.getByRole('button', { name: 'Cancel task' }).click();
  await page.getByRole('button', { name: 'Yes, cancel task' }).click();
  await page.getByRole('link', { name: 'Workflow and explanation' }).click();
  await expect(page.getByTestId('run-status')).toHaveText('Cancelled', { timeout: 60_000 });
  await actions.getByRole('button', { name: 'Run again as a new task' }).click();
  const again = page.getByTestId('run-again-confirm');
  await expect(again).toContainText('which was cancelled, stays as it is');
  await again.getByRole('button', { name: 'Run it again' }).click();
  await expect(page).not.toHaveURL(taskUrl);
  await expect(page.getByRole('heading', { level: 1, name: /Implement Subtract/ })).toBeVisible();
  await expect(page.getByTestId('review-panel')).toBeVisible({ timeout: 90_000 });
});
