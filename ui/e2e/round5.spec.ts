import { expect, test } from '@playwright/test';
import { choose, createAgent, createProject, openAgentPage, openNewAgent, projectNav } from './helpers';

// Round 5: team management against the real Go core: purpose, model and effort at creation, editing, and removal.

test('team: create an agent with purpose, model and effort; refused values say why; edit it; remove it', async ({ page }) => {
  await createProject(page, 'team-mgmt');
  await openNewAgent(page);

  await page.getByLabel('Agent name').fill('Pay');
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await page.getByLabel(/Purpose/).fill('Own the payments service. Never touch the UI.');
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  // Claude best practices: on by default, with the recommendation for the role and the exact text that is added
  const bp = page.getByTestId('claude-practices');
  await expect(bp).toContainText('Recommended for a backend (worker)');
  await expect(bp.getByLabel('Follow Claude Code best practices')).toBeChecked();
  await expect(page.getByLabel('Model', { exact: true })).toHaveAttribute('data-value', 'sonnet');
  await expect(page.getByLabel('Effort', { exact: true })).toHaveAttribute('data-value', 'medium');
  await bp.getByText(/What this adds/).click();
  await expect(bp.getByTestId('claude-practices-text')).toContainText('Never skip, delete or weaken a test');
  await choose(page.getByLabel('Model', { exact: true }), 'sonnet');
  await choose(page.getByLabel('Effort', { exact: true }), 'high');
  await page.getByRole('button', { name: 'Create agent' }).click();

  const row = page.getByTestId('agent').filter({ hasText: 'Pay' });
  await expect(row).toBeVisible();
  await expect(row.getByTestId('agent-runs')).toHaveText('Claude Code · sonnet');
  // not on a task yet: the card's line is the first sentence of its purpose
  await expect(row.getByTestId('agent-doing')).toHaveText('Own the payments service.');

  // a model that would be read as a command-line flag is refused by the core, in plain words (the editor is on its page)
  await openAgentPage(page, 'Pay');
  const chips = page.getByTestId('agent-chip');
  await expect(chips.filter({ hasText: 'effort: high' })).toBeVisible();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const editor = page.getByTestId('agent-editor');
  // "Another model..." turns the list into a text field for a full model name
  await choose(editor.getByLabel('Model', { exact: true }), '__other__');
  await editor.getByLabel('Model', { exact: true }).fill('-x');
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(editor.getByRole('alert')).toContainText('does not start with');

  // a valid edit is stored and shown
  await editor.getByRole('button', { name: 'Choose from the list instead' }).click();
  await choose(editor.getByLabel('Model', { exact: true }), 'opus');
  await choose(editor.getByLabel('Effort', { exact: true }), 'max');
  await editor.getByRole('textbox', { name: /^Purpose/ }).fill('Billing only.');
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByTestId('agent-editor')).toHaveCount(0);
  await expect(chips.filter({ hasText: 'Claude Code · opus' })).toBeVisible();
  await expect(chips.filter({ hasText: 'effort: max' })).toBeVisible();
  await expect(page.getByTestId('agent-page-purpose')).toHaveText('Billing only.');

  // the Inspector shows the purpose the agent will be given
  await page.getByRole('button', { name: 'Inspect', exact: true }).click();
  await expect(page.getByTestId('inspector-purpose')).toContainText('Billing only.');

  // removal needs a confirmation and the agent leaves the team
  await projectNav(page, 'nav-team');
  await expect(row.getByTestId('agent-runs')).toHaveText('Claude Code · opus');
  await openAgentPage(page, 'Pay');
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await page.getByTestId('agent-editor').getByRole('button', { name: 'Remove from team' }).click();
  await page.getByTestId('agent-editor').getByRole('button', { name: 'Yes, remove' }).click();
  await projectNav(page, 'nav-team');
  await expect(page.getByTestId('agent').filter({ hasText: 'Pay' })).toHaveCount(0);

  // the team still works afterwards
  await createAgent(page, 'Rev', 'reviewer');
});
