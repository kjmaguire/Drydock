import { expect, test } from '@playwright/test';
import { choose, createProject, projectNav, workspaceNav } from './helpers';

// Decision P118 against the real Go core: a project's own routing level that falls back to the workspace's (Project settings
// and the Routing page), and a budget on a workflow.

test("a project's routing level follows the workspace until it chooses its own, and both are kept", async ({ page }) => {
  await createProject(page, 'gocalc-level');
  await projectNav(page, 'nav-project');
  const panel = page.getByTestId('routing-level-panel');
  const level = panel.getByLabel('Level', { exact: true });
  await expect(panel.getByTestId('routing-level-now')).toContainText("run at Balanced, the workspace's level");
  await choose(level, 'quality');
  await expect(panel.getByTestId('routing-level-now')).toHaveAttribute('data-source', 'project');
  await expect(panel.getByTestId('routing-level-now')).toContainText('run at Quality, its own level');

  // the workspace's level is on the Routing page; the project keeps its own
  await workspaceNav(page, 'nav-routing');
  const ws = page.getByTestId('workspace-level');
  await expect(ws.getByLabel('Workspace level')).toHaveAttribute('data-value', 'balanced');
  await choose(ws.getByLabel('Workspace level'), 'cheapest');
  await expect(ws.getByTestId('workspace-level-note')).toContainText('Cheapest: cheap models first');
  await page.reload();
  await expect(page.getByTestId('workspace-level').getByLabel('Workspace level')).toHaveAttribute('data-value', 'cheapest');

  await projectNav(page, 'nav-project', 'gocalc-level');
  await expect(panel.getByTestId('routing-level-now')).toContainText('run at Quality, its own level');
  // following the workspace again names the workspace's new level
  await choose(level, '');
  await expect(panel.getByTestId('routing-level-now')).toContainText("run at Cheapest, the workspace's level");
  await page.reload();
  await expect(page.getByTestId('routing-level-panel').getByTestId('routing-level-now')).toHaveAttribute('data-source', 'workspace');
  await workspaceNav(page, 'nav-routing');
  await choose(page.getByTestId('workspace-level').getByLabel('Workspace level'), 'balanced'); // leave the workspace as other tests expect it
  await expect(page.getByTestId('workspace-level').getByTestId('workspace-level-note')).toContainText('Balanced');
});

test('a budget on a workflow names one of the workflows the core runs and counts its runs in this project', async ({ page }) => {
  await createProject(page, 'gocalc-wfbudget');
  await projectNav(page, 'nav-budgets');
  await page.getByRole('button', { name: 'Add budget' }).first().click();
  const form = page.getByRole('form', { name: 'New budget' });
  await choose(form.getByLabel('Scope'), 'workflow');
  const wf = form.getByLabel('Workflow (its runs in this project)');
  await choose(wf, 'iterate-until-verified');
  await form.getByLabel('Limit in USD').fill('3');
  await page.getByRole('button', { name: 'Add budget' }).last().click();
  const card = page.locator('[data-testid="budget"][data-scope="workflow"]');
  await expect(card).toBeVisible();
  await expect(card).toContainText('iterate-until-verified');
  await expect(card).toContainText('$3');
});
