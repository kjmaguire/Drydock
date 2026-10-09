import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createProject } from './helpers';

// Two tasks that change the same file (the scripted worker writes calc.go for both). The second must wait for the first and say
// why; the human can let it go on; the first is untouched.
test('collision guard: a task that changes the same file as an earlier running task waits, says why, and can be released', async ({ page }) => {
  await createProject(page, 'gocalc-guard');
  await createAgent(page, 'Worker G', 'backend');
  await createAgent(page, 'Reviewer G', 'reviewer');

  // the first task runs to the human's approval and stays there, so it is still active
  await createAndStartTask(page, 'First change to calc', 'Worker G', 'Reviewer G');
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 90_000 });

  // the second changes the same file: it does not reach review, it waits and names the task and the file
  await createAndStartTask(page, 'Second change to calc', 'Worker G', 'Reviewer G');
  const note = page.getByTestId('blocked-note');
  await expect(note).toBeVisible({ timeout: 90_000 });
  await expect(note).toContainText('waiting for');
  await expect(note).toContainText('First change to calc');
  await expect(note).toContainText('calc.go');
  await expect(page.getByTestId('task-status').first()).toContainText('Blocked');
  await expect(page.getByTestId('review-panel')).toHaveCount(0);

  // the human lets it go on: it carries on to its own approval
  await note.getByRole('button', { name: 'Let it go on now' }).click();
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByTestId('blocked-note')).toHaveCount(0);

  // the first task was never disturbed: it is still waiting for its approval
  await page.getByTestId('nav-tasks').click();
  const first = page.getByTestId('task-row').filter({ hasText: 'First change to calc' });
  await expect(first).toContainText(/Waiting|Verification|Review/i);
});
