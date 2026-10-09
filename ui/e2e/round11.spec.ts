import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createProject } from './helpers';

// The workflow graph and the run explanation against the real Go core: React Flow draws the run's graph with each node's
// state, the checkpoint timeline shows the run as it was, and each turn shows its manifest and exact prompt.
test('workflow graph, checkpoint timeline and the explanation of every turn', async ({ page }) => {
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
  await expect(graph.locator('.react-flow__edge').first()).toBeAttached();

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
});
