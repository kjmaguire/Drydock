import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createProject } from './helpers';

// The bottom dock against the real Go core: Monaco's diff of the task, the check logs and agent stream, and a real shell
// in the task's worktree through xterm.js.
test('the dock: Monaco diff, logs and a terminal in the task worktree', async ({ page }) => {
  await createProject(page, 'gocalc-dock');
  await createAgent(page, 'Worker One', 'backend');
  await createAgent(page, 'Reviewer One', 'reviewer');
  await createAndStartTask(page, 'Implement Subtract', 'Worker One', 'Reviewer One');
  await expect(page.getByTestId('review-panel')).toBeVisible({ timeout: 90_000 });

  // the task page shows the change in Monaco's diff editor, both sides
  const diff = page.getByTestId('diff-viewer').first();
  await expect(diff.getByRole('button', { name: /calc\.go/ })).toBeVisible();
  const editor = diff.getByTestId('monaco-diff');
  // (in a narrow pane Monaco shows the diff inline even when side by side is chosen, so both texts are looked for anywhere)
  await expect(editor).toContainText(/return\sa\s-\sb/, { timeout: 60_000 });
  await expect(editor).toContainText(/return\s0/);
  await diff.getByRole('button', { name: 'Inline' }).click();
  await expect(diff.getByRole('button', { name: 'Inline' })).toHaveAttribute('aria-pressed', 'true');
  await expect(editor.locator('.monaco-diff-editor')).not.toHaveClass(/side-by-side/);
  await diff.getByRole('button', { name: 'Side by side' }).click();

  // the dock's Logs tab: the test check's output and the worker's stream, rendered
  await page.getByRole('tab', { name: 'Logs' }).click();
  const logs = page.getByTestId('logs-panel');
  await logs.getByRole('button', { name: /Check:\s*test/ }).click();
  await expect(logs.getByTestId('log-text')).toContainText('ok');
  await logs.getByRole('button', { name: /Agent:\s*work/ }).first().click();
  await expect(logs.getByTestId('log-text')).toContainText('implemented Subtract');
  await expect(logs.getByTestId('log-text')).not.toContainText('"kind"');

  // a real shell in the task's worktree
  await page.getByRole('tab', { name: 'Terminal' }).click();
  const term = page.getByTestId('terminal-panel');
  await term.getByRole('button', { name: 'New terminal in the task' }).click();
  const xterm = term.getByTestId('xterm');
  await expect(xterm.locator('.xterm-rows')).toBeVisible();
  await xterm.click();
  await page.keyboard.type('echo dock-$((6*7)); ls\n');
  await expect(xterm.locator('.xterm-rows')).toContainText('dock-42', { timeout: 20_000 });
  await expect(xterm.locator('.xterm-rows')).toContainText('calc.go');
  await term.getByRole('button', { name: /^Close / }).click();
  await expect(term.getByText(/No terminal is open/)).toBeVisible();

  // Ctrl+J hides the dock
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('Control+j');
  await expect(page.getByTestId('terminal-panel')).toBeHidden();
});
