import { expect, test } from '@playwright/test';
import { appUrl, createAgent, createAndStartTask, createProject } from './helpers';

test('Connections: gates need a checkbox and a click by the human; Test is gated behind a cost warning', async ({ page }) => {
  await createProject(page, 'gocalc-conn');
  await page.getByTestId('nav-connections').click();
  const custom = page.locator('[data-testid=connection][data-adapter=custom]');
  await expect(custom).toBeVisible();
  await expect(custom).toHaveAttribute('data-usable', 'false');
  const gate = custom.getByTestId('gate');
  await expect(gate).toHaveAttribute('data-acked', 'false');
  const accept = gate.getByRole('button', { name: 'Accept' });
  await expect(accept).toBeDisabled();
  await gate.getByLabel('I have read this and accept it').check();
  await accept.click();
  await expect(gate).toHaveAttribute('data-acked', 'true');
  // persisted by the core: still accepted after a reload
  await page.reload();
  await expect(page.locator('[data-testid=connection][data-adapter=custom] [data-testid=gate]')).toHaveAttribute('data-acked', 'true');

  // Test connection never starts the CLI without the warning and a second click. (It is not run here: the connection
  // service in the harness looks at the real machine, so a test could start a real CLI.)
  const claude = page.locator('[data-testid=connection][data-adapter=claude]');
  await expect(claude).toBeVisible();
  await claude.getByRole('button', { name: 'Test connection...' }).click();
  await expect(claude).toContainText('cost a few cents');
  await expect(claude.getByRole('button', { name: 'Run the test' })).toBeVisible();
  await claude.getByRole('button', { name: 'Cancel' }).click();
  await expect(claude.getByTestId('connection-report')).toHaveCount(0);
});

test('Providers: a fake key is stored by the core but never displayed; validation errors are shown verbatim; test and delete work', async ({ page }) => {
  await createProject(page, 'gocalc-prov');
  await page.getByTestId('nav-providers').click();
  const secret = 'sk-e2e-not-a-real-key-12345';

  // plain http to a public host is refused by the core, and the page shows its words
  await page.getByLabel('Provider name').fill('Public over http');
  await page.getByLabel('Base URL').fill('http://8.8.8.8/v1');
  await page.getByLabel(/API key/).fill(secret);
  await page.getByRole('button', { name: 'Add provider' }).click();
  await expect(page.getByRole('alert')).toContainText('plain http is only allowed on loopback');
  await expect(page.getByLabel(/API key/)).toHaveValue('');
  expect(await page.content()).not.toContain(secret);

  // a loopback host is accepted
  await page.getByLabel('Provider name').fill('Local vLLM');
  await page.getByLabel('Kind', { exact: true }).selectOption('host');
  await page.getByLabel('Base URL').fill('http://127.0.0.1:9/v1');
  await page.getByLabel(/API key/).fill(secret);
  await page.getByLabel('Data class').selectOption('private');
  await page.getByLabel('Tier', { exact: true }).selectOption('free');
  await page.getByRole('button', { name: 'Add provider' }).click();
  const card = page.locator('[data-testid=provider][data-name="Local vLLM"]');
  await expect(card).toBeVisible();
  await expect(card).toContainText('stored in the credential store (never shown)');
  await expect(card).toContainText('never'); // last seen
  expect(await page.content()).not.toContain(secret);

  // Test: nothing listens on port 9, so the core reports a failure (shown, not hidden)
  await card.getByRole('button', { name: 'Test' }).click();
  await expect(card.getByTestId('probe')).toHaveAttribute('data-ok', 'false');
  await card.getByRole('button', { name: 'Models' }).click();
  await expect(card).toContainText('No models in the catalog');

  // disable then delete (confirmed)
  await card.getByRole('button', { name: 'Disable' }).click();
  await expect(card).toContainText('disabled');
  await card.getByRole('button', { name: 'Delete' }).click();
  await card.getByRole('button', { name: /Yes, delete/ }).click();
  await expect(card).toHaveCount(0);

  // a trainable provider carries its persistent warning
  await page.getByLabel('Provider name').fill('Public docs only');
  await page.getByLabel('Base URL').fill('http://127.0.0.1:9/v1');
  await page.getByLabel('Data class').selectOption('trainable');
  await page.getByRole('button', { name: 'Add provider' }).click();
  await expect(page.getByTestId('trainable-note')).toContainText('never receives repo content');
});

test('threads, direct messages, archived task channels, the inspector and usage sources', async ({ page }) => {
  await createProject(page, 'gocalc-r2');
  await createAgent(page, 'Worker R2', 'backend');
  await createAgent(page, 'Reviewer R2', 'reviewer');

  // a thread in #general
  await page.getByRole('link', { name: '# general' }).click();
  await page.getByLabel('Message to #general').fill('who owns the parser?');
  await page.getByRole('button', { name: 'Send' }).click();
  const root = page.locator('[data-testid=message]').filter({ hasText: 'who owns the parser?' });
  await root.getByRole('button', { name: /Open thread/ }).click();
  const thread = page.getByTestId('thread');
  await expect(thread).toBeVisible();
  await thread.getByLabel('Reply in thread').fill('the backend agent');
  await thread.getByRole('button', { name: 'Reply' }).click();
  await expect(thread.getByTestId('thread-message')).toHaveCount(2);
  await expect(root.getByRole('button', { name: /1 reply/ })).toBeVisible();
  // members list
  await page.locator('details.members summary').click();
  await expect(page.locator('details.members')).toContainText('Worker R2');

  // a direct message with an agent
  await page.getByTestId('nav-team').click();
  await page.getByTestId('agent').filter({ hasText: 'Worker R2' }).getByRole('button', { name: 'Message' }).click();
  await expect(page.getByLabel('Messages in dm:', { exact: false })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Direct messages' })).toBeVisible();
  await expect(page.locator('[data-testid=room-link]').filter({ hasText: 'Worker R2' })).toBeVisible();

  // run a task to completion so the channel is archived and the agent has runs and usage
  await createAndStartTask(page, 'Implement Subtract for R2', 'Worker R2', 'Reviewer R2');
  await expect(page.getByTestId('review-panel')).toBeVisible({ timeout: 90_000 });
  await page.getByRole('button', { name: 'Approve' }).click();
  await expect(page.getByTestId('task-status').first()).toHaveText(/Completed/, { timeout: 60_000 });

  // the archived task channel still opens (room.get) and its composer is disabled
  await page.getByRole('link', { name: 'Open task channel' }).click();
  await expect(page.getByText('archived', { exact: true })).toBeVisible();
  await expect(page.locator('[data-testid=check-card][data-tool=Tests]').first()).toBeVisible();
  await expect(page.getByLabel(/^Message to #/)).toBeDisabled();

  // the task's usage rows carry a source badge (the core says reported or estimated)
  await page.getByRole('link', { name: 'Task details' }).click();
  const usage = page.getByTestId('usage-row');
  await expect(usage.first()).toBeVisible();
  for (const src of await usage.evaluateAll((els) => els.map((e) => e.getAttribute('data-source')))) expect(['reported', 'estimated']).toContain(src);
  await expect(page.getByTestId('task-cost').locator('xpath=..')).toContainText(/reported|estimated/);

  // the Agent Inspector: runs with PID and working directory, usage with sources
  await page.getByRole('button', { name: 'Inspect' }).first().click();
  const insp = page.getByTestId('inspector');
  await expect(insp).toBeVisible();
  await expect(insp.getByTestId('agent-run').first()).toContainText(/PID\s*\d+/);
  await expect(insp.getByTestId('agent-run').first()).toContainText(/Working dir/);
  await expect(insp.getByTestId('usage-row').first()).toBeVisible();

  // Activity still works after the stream started at the head: history is backfilled on demand
  await page.getByRole('link', { name: 'Activity' }).click();
  await expect(page.locator('[data-testid=event-row][data-type="task.created"]')).toBeVisible();
  await expect(page.locator('[data-testid=event-row][data-type="task.completed"]')).toBeVisible();
  await page.getByLabel('Filter by type prefix').fill('task.*');
  await expect(page.locator('[data-testid=event-row][data-type^="agent."]')).toHaveCount(0);
  void appUrl;
});
