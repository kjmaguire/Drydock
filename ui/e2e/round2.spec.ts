import { expect, test } from '@playwright/test';
import { choose, appUrl, createAgent, createAndStartTask, createProject, roomLink, workspaceNav, projectNav, openRoom, openChecks, openAgentPage } from './helpers';

test('Agents: notices need a checkbox and a click by the human; Test is gated behind a cost warning', async ({ page }) => {
  await createProject(page, 'gocalc-conn');
  await workspaceNav(page, 'nav-connections');
  // the list of rows beside the chosen one's detail; a row opens its detail and goes in the address
  const customRow = page.locator('[data-testid=agent-row][data-adapter=custom]');
  await expect(customRow).toHaveAttribute('data-usable', 'false');
  await customRow.click();
  await expect(customRow).toHaveAttribute('aria-current', 'true');
  await expect(page).toHaveURL(/\/agents\/custom$/);
  const custom = page.locator('[data-testid=agent-detail][data-adapter=custom]');
  await expect(custom).toBeVisible();
  await expect(custom).toHaveAttribute('data-usable', 'false');
  // the harness's custom manifest names a program it does not have: the one notice says so, and its notice to accept is on the Overview
  await expect(custom.getByTestId('agent-notice')).toContainText('custom was not found on this PC');
  const gate = custom.getByTestId('gate');
  await expect(gate).toHaveAttribute('data-acked', 'false');
  const accept = gate.getByRole('button', { name: 'Accept' });
  await expect(accept).toBeDisabled();
  await gate.getByLabel('I have read this and accept it').check();
  await accept.click();
  await expect(gate).toHaveAttribute('data-acked', 'true');
  // persisted by the core: still accepted after a reload, and the address opens the same agent
  await page.reload();
  await expect(page.locator('[data-testid=agent-detail][data-adapter=custom] [data-testid=gate]')).toHaveAttribute('data-acked', 'true');

  // Test connection never starts the CLI without the warning and a second click. (It is not run here: the connection
  // service in the harness looks at the real machine, so a test could start a real CLI.)
  // the Windows install (a Claude Code inside a WSL distro has a row of its own, data-key claude@wsl:<distro>)
  await page.locator('[data-testid=agent-row][data-key=claude]').click();
  const claude = page.locator('[data-testid=agent-detail][data-key=claude]');
  await expect(claude).toBeVisible();
  await expect(page.getByTestId('agent-detail')).toHaveCount(1);
  // on a machine without Claude Code there is nothing to test: its notice points to the install guidance instead
  if ((await claude.getAttribute('data-state')) === 'install') {
    await expect(claude.getByTestId('test-connection')).toHaveCount(0);
    await claude.getByTestId('agent-notice-fix').click();
    await expect(claude.getByTestId('install-tab')).toContainText('Drydock never runs this for you');
    return;
  }
  await claude.getByTestId('test-connection').click();
  const confirm = page.getByRole('dialog', { name: 'Test connection' });
  await expect(confirm).toContainText('It uses your account');
  await expect(confirm.getByLabel('Also send one tiny prompt')).not.toBeChecked();
  await expect(confirm.getByRole('button', { name: 'Run the test' })).toBeVisible();
  await confirm.getByRole('button', { name: 'Cancel' }).click();
  await expect(confirm).toHaveCount(0);
  await claude.getByTestId('agent-tab-tests').click();
  await expect(claude.getByTestId('connection-report')).toHaveCount(0);
});

test('Providers (on the Models page): a fake key is stored by the core but never displayed; validation errors are shown verbatim; check and remove work', async ({ page }) => {
  await createProject(page, 'gocalc-prov');
  await workspaceNav(page, 'nav-providers');
  const secret = 'sk-e2e-not-a-real-key-12345';
  // the list's "Add a provider" row opens the form beside it, and the address says so
  await page.getByTestId('provider-add').click();
  await expect(page).toHaveURL(/\/models\/new(\?|$)/);

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
  await choose(page.getByLabel('Kind', { exact: true }), 'host');
  await page.getByLabel('Base URL').fill('http://127.0.0.1:9/v1');
  await page.getByLabel(/API key/).fill(secret);
  await choose(page.getByLabel('Data class'), 'private');
  await choose(page.getByLabel('Tier', { exact: true }), 'free');
  await page.getByRole('button', { name: 'Add provider' }).click();
  const card = page.locator('[data-testid=provider][data-name="Local vLLM"]');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Stored in the credential store (never shown)');
  await expect(card).toContainText('never'); // last seen
  // the new provider is chosen in the list and named in the address
  const id = (await card.getAttribute('data-id')) ?? '';
  expect(id).not.toBe('');
  await expect(page).toHaveURL(new RegExp(`/models/${id}(\\?|$)`));
  const row = page.locator('[data-testid=models-row][data-name="Local vLLM"]');
  await expect(row).toHaveAttribute('aria-current', 'true');
  await expect(row).toContainText('Your server · free');
  expect(await page.content()).not.toContain(secret);

  // Check now: nothing listens on port 9, so the core reports a failure (shown, not hidden); no models are listed
  await card.getByRole('button', { name: 'Check now' }).click();
  await expect(card.getByTestId('probe')).toHaveAttribute('data-ok', 'false');
  await expect(card.getByTestId('provider-models')).toContainText('None listed yet');

  // disable then remove (confirmed)
  await card.getByRole('button', { name: 'Disable' }).click();
  await expect(card.getByTestId('detail-status')).toHaveText('Disabled');
  await expect(row).toContainText('Disabled');
  await card.getByRole('button', { name: 'Remove', exact: true }).click();
  await card.getByRole('button', { name: /Yes, remove/ }).click();
  await expect(card).toHaveCount(0);
  await expect(row).toHaveCount(0);

  // a trainable provider carries its persistent warning
  await page.getByTestId('provider-add').click();
  await page.getByLabel('Provider name').fill('Public docs only');
  await page.getByLabel('Base URL').fill('http://127.0.0.1:9/v1');
  await choose(page.getByLabel('Data class'), 'trainable');
  await page.getByRole('button', { name: 'Add provider' }).click();
  await expect(page.getByTestId('trainable-note')).toContainText('never receives repo content');
});

test('threads, direct messages, archived task channels, the inspector and usage sources', async ({ page }) => {
  await createProject(page, 'gocalc-r2');
  await createAgent(page, 'Worker R2', 'backend');
  await createAgent(page, 'Reviewer R2', 'reviewer');

  // a thread in #general
  await openRoom(page, 'general');
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
  await openAgentPage(page, 'Worker R2');
  await page.getByRole('button', { name: 'Message Worker R2' }).click();
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
  await openChecks(page);
  await expect(page.locator('[data-testid=check-card][data-tool=Tests]').first()).toBeVisible();
  await expect(page.getByLabel(/^Message to #/)).toBeDisabled();

  // the task's usage rows carry a source badge (the core says reported or estimated)
  await page.getByRole('link', { name: 'Task details' }).click();
  const usage = page.getByTestId('usage-row');
  await expect(usage.first()).toBeVisible();
  for (const src of await usage.evaluateAll((els) => els.map((e) => e.getAttribute('data-source')))) expect(['reported', 'estimated']).toContain(src);
  await expect(page.getByTestId('task-cost').locator('xpath=..')).toContainText(/reported|estimated/);

  // the Agent Inspector (from the details panel's Agents tab: the thread opened earlier still holds the panel): runs with PID
  // and working directory, usage with sources. The task page has its own properties, so the panel starts closed beside it.
  await page.getByRole('complementary', { name: 'Details' }).getByRole('button', { name: 'Show details' }).click();
  await page.getByRole('complementary', { name: 'Details' }).getByRole('tab', { name: 'Agents' }).click();
  await page.getByRole('button', { name: 'Inspect' }).first().click();
  const insp = page.getByTestId('inspector');
  await expect(insp).toBeVisible();
  await expect(insp.getByTestId('agent-run').first()).toContainText(/PID\s*\d+/);
  await expect(insp.getByTestId('agent-run').first()).toContainText(/Working dir/);
  await expect(insp.getByTestId('usage-row').first()).toBeVisible();

  // Activity still works after the stream started at the head: history is backfilled on demand
  await projectNav(page, 'nav-activity');
  await expect(page.locator('[data-testid=event-row][data-type="task.created"]')).toBeVisible();
  await expect(page.locator('[data-testid=event-row][data-type="task.completed"]')).toBeVisible();
  await page.getByLabel('Filter by type prefix').fill('task.*');
  await expect(page.locator('[data-testid=event-row][data-type^="agent."]')).toHaveCount(0);
  void appUrl;
});
