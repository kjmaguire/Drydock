import { expect, test } from '@playwright/test';
import { choose, createAgent, createProject, openAgentPanel, openDescribeAgent, projectNav } from './helpers';

// Editing an agent in place (decision P137) and describing a new one (decision P138), against the real Go core. The harness has no
// model, so the draft is written by the offline rules.

test('the Crew card opens the agent editor panel; model, effort and permission persist after a reload', async ({ page }) => {
  await createProject(page, 'edit-in-place');
  await createAgent(page, 'Ada', 'backend');
  const card = page.locator('[data-testid="agent"][data-name="Ada"]');
  await expect(card.getByTestId('agent-permission-chip')).toHaveText('Edits freely');

  const panel = await openAgentPanel(page, 'Ada');
  await expect(panel.getByTestId('agent-runs-on')).toHaveText('Claude Code');
  await expect(panel).toContainText('Applies from its next turn');
  await choose(panel.getByLabel('Model', { exact: true }), 'opus');
  await panel.getByRole('radiogroup', { name: 'Effort' }).getByRole('radio', { name: 'high', exact: true }).click();
  await panel.getByRole('radiogroup', { name: 'Permissions' }).getByRole('radio', { name: 'Read only' }).click();
  await expect(panel.getByTestId('scope-note')).toContainText('opus · high is used for Ada in edit-in-place only');
  await panel.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.locator('dialog[open]')).toHaveCount(0);
  await expect(card.getByTestId('agent-model-chip')).toHaveText('opus · high');

  await page.reload();
  await projectNav(page, 'nav-team');
  await expect(card.getByTestId('agent-model-chip')).toHaveText('opus · high');
  await expect(card.getByTestId('agent-permission-chip')).toHaveText('Read only');
  const again = await openAgentPanel(page, 'Ada', false);
  await expect(again.getByRole('radiogroup', { name: 'Effort' }).getByRole('radio', { name: 'high', exact: true })).toHaveAttribute('aria-checked', 'true');
  await expect(again.getByRole('radiogroup', { name: 'Permissions' }).getByRole('radio', { name: 'Read only' })).toHaveAttribute('aria-checked', 'true');

  // Everywhere: an agent without a template is saved as one
  await again.getByRole('radiogroup', { name: 'Save for' }).getByRole('radio', { name: 'Everywhere' }).click();
  await expect(again.getByTestId('scope-note')).toContainText('Also saves Ada as a template');
  await again.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByTestId('agent-saved-note')).toHaveText('Saved as the template Ada.');
});

test('a sentence drafts an agent (offline rules), the person adjusts it and creates it', async ({ page }) => {
  await createProject(page, 'describe-agent');
  await openDescribeAgent(page);
  const box = page.locator('dialog[open]').getByTestId('describe-agent');
  await box.getByLabel('What should this agent do?').fill('Reviews pull requests for security problems. Should never edit code, just comment. Use the best model.');
  await box.getByRole('button', { name: 'Draft agent' }).click();
  const draft = box.getByTestId('agent-draft');
  await expect(draft).toHaveAttribute('data-source', 'rules');
  await expect(draft.getByLabel('Agent name')).toHaveValue('Security Reviewer');
  await expect(draft.getByLabel('Model', { exact: true })).toHaveAttribute('data-value', 'opus');
  await expect(draft.getByLabel('Permissions', { exact: true })).toHaveAttribute('data-value', 'plan');
  await expect(draft.getByTestId('draft-why')).toContainText('"never edit"');
  await expect(box.getByTestId('draft-source')).toContainText('no model was used');
  await draft.getByLabel('Agent name').fill('Sec');
  await draft.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.locator('dialog[open]')).toHaveCount(0);
  const card = page.locator('[data-testid="agent"][data-name="Sec"]');
  await expect(card).toBeVisible();
  await expect(card.getByTestId('agent-model-chip')).toHaveText('opus · high');
  await expect(card.getByTestId('agent-permission-chip')).toHaveText('Read only');

  // a starter chip drafts at once
  await openDescribeAgent(page, false);
  await box.getByRole('group', { name: 'Start from' }).getByRole('button', { name: 'Tester' }).click();
  await expect(box.getByTestId('agent-draft').getByLabel('Agent name')).toHaveValue('Tester');
});
