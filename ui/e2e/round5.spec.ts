import { expect, test } from '@playwright/test';
import { createAgent, createProject } from './helpers';

// Round 5: team management against the real Go core: purpose, model and effort at creation, editing, and removal.

test('team: create an agent with purpose, model and effort; refused values say why; edit it; remove it', async ({ page }) => {
  await createProject(page, 'team-mgmt');
  await page.getByTestId('nav-team').click();

  await page.getByLabel('Agent name').fill('Pay');
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await page.getByLabel(/Purpose/).fill('Own the payments service. Never touch the UI.');
  await page.getByLabel('Adapter', { exact: true }).selectOption('claude');
  await page.getByLabel('Model', { exact: true }).fill('sonnet');
  await page.getByLabel('Effort', { exact: true }).selectOption('high');
  await page.getByRole('button', { name: 'Create agent' }).click();

  const row = page.getByTestId('agent').filter({ hasText: 'Pay' });
  await expect(row).toBeVisible();
  await expect(row).toContainText('model sonnet');
  await expect(row).toContainText('effort high');
  await expect(row.getByTestId('agent-purpose')).toContainText('Own the payments service.');

  // a model that would be read as a command-line flag is refused by the core, in plain words
  await row.getByRole('button', { name: 'Edit Pay' }).click();
  const editor = page.getByTestId('agent-editor');
  await editor.getByLabel('Model', { exact: true }).fill('-x');
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(editor.getByRole('alert')).toContainText('does not start with');

  // a valid edit is stored and shown
  await editor.getByLabel('Model', { exact: true }).fill('opus');
  await editor.getByLabel('Effort', { exact: true }).selectOption('max');
  await editor.getByRole('textbox', { name: /^Purpose/ }).fill('Billing only.');
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByTestId('agent-editor')).toHaveCount(0);
  await expect(row).toContainText('model opus');
  await expect(row).toContainText('effort max');
  await expect(row.getByTestId('agent-purpose')).toContainText('Billing only.');

  // the Inspector shows the purpose the agent will be given
  await row.getByRole('button', { name: 'Inspect' }).click();
  await expect(page.getByTestId('inspector-purpose')).toContainText('Billing only.');

  // removal needs a confirmation and the agent leaves the team
  await page.getByTestId('nav-team').click();
  await row.getByRole('button', { name: 'Edit Pay' }).click();
  await page.getByTestId('agent-editor').getByRole('button', { name: 'Remove from team' }).click();
  await page.getByTestId('agent-editor').getByRole('button', { name: 'Yes, remove' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Pay' })).toHaveCount(0);

  // the team still works afterwards
  await createAgent(page, 'Rev', 'reviewer');
});
