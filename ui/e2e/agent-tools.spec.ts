// Agent tools by role and a person's own CLI (decision P115) against the real core: a cell of the project's table changed on
// its Integrations page stays changed after a reload and goes back to the default; a rule the core cannot compile is refused
// with the reason and a sound one is kept; on the Agents page a custom CLI is added from the template (its program is not
// installed here, so its Connection says so), shown behind its own notice, and removed.
import { expect, test } from '@playwright/test';
import { createProjectWithoutRepo, projectNav, workspaceNav } from './helpers';

test("a project's tools by role: a changed cell is kept and put back, rules are checked by the core", async ({ page }) => {
  await createProjectWithoutRepo(page, 'agent-tools');
  await projectNav(page, 'nav-project-integrations');
  const panel = page.getByTestId('role-tools');
  await expect(panel).toBeVisible();
  const cell = panel.getByLabel('PM: Read files', { exact: true });
  await expect(cell).not.toBeChecked();
  await expect(panel.getByLabel('Backend: Read files', { exact: true })).toBeChecked();
  // the box shows what the core answers (a controlled checkbox), so the click is checked by the answer, not by check()
  await cell.click();
  await expect(cell).toBeChecked();
  await expect(panel.locator('tr[data-role="pm"] td[data-column="read_range"]')).toHaveAttribute('data-changed', 'true');
  await page.reload();
  await expect(page.getByTestId('role-tools').getByLabel('PM: Read files', { exact: true })).toBeChecked();
  await page.getByTestId('role-tools').locator('tr[data-role="pm"]').getByTestId('role-reset').click();
  await expect(page.getByTestId('role-tools').getByLabel('PM: Read files', { exact: true })).not.toBeChecked();
  await expect(page.getByTestId('role-tools').locator('tr[data-role="pm"] td[data-column="read_range"]')).toHaveAttribute('data-changed', 'false');

  const rules = page.getByTestId('role-rules');
  await rules.getByLabel('Name').fill('no trackers for QA');
  await rules.getByLabel('Expression').fill('column ==');
  await rules.getByRole('button', { name: 'Add rule' }).click();
  await expect(page.getByTestId('role-tools').getByRole('alert')).toContainText('no trackers for QA');
  await rules.getByLabel('Expression').fill('role == "qa" && column == "trackers"');
  await rules.getByRole('button', { name: 'Add rule' }).click();
  await expect(rules.getByTestId('role-rule')).toHaveCount(1);
  await page.reload();
  await expect(page.getByTestId('role-rules').getByTestId('role-rule')).toContainText('role == "qa" && column == "trackers"');
  await page.getByTestId('role-rules').getByRole('button', { name: 'Remove the rule no trackers for QA' }).click();
  await expect(page.getByTestId('role-rules').getByTestId('no-rules')).toBeVisible();
});

test('a custom CLI is added from the template on the Agents page, gated, and removed', async ({ page }) => {
  await createProjectWithoutRepo(page, 'custom-cli');
  await workspaceNav(page, 'nav-connections');
  await page.getByTestId('custom-cli-open').click();
  const form = page.getByTestId('custom-cli-form');
  await form.getByLabel('Id').fill('e2e-cli');
  await form.getByTestId('custom-cli-template').click();
  await expect(form.getByTestId('custom-cli-yaml')).toHaveValue(/id: e2e-cli,/);
  await expect(form.getByTestId('custom-cli-yaml')).toHaveValue(/gate: \{id: custom-e2e-cli,/);
  await form.getByTestId('custom-cli-add').click();
  await expect(form).toBeHidden();
  // its program (my-cli.exe) is not on this machine: the Connection is listed as not installed, with its own notice
  const row = page.locator('[data-testid="agent-row"][data-adapter="e2e-cli"]');
  await expect(row).toBeVisible();
  const detail = page.getByTestId('agent-detail');
  await expect(detail).toHaveAttribute('data-adapter', 'e2e-cli');
  await expect(detail.getByTestId('gate')).toBeVisible();
  await detail.getByTestId('agent-tab-install').click();
  await expect(detail.getByTestId('custom-cli')).toContainText('e2e-cli.yaml');
  await detail.getByTestId('custom-cli-remove').click();
  await detail.getByTestId('custom-cli-remove-confirm').click();
  await expect(row).toHaveCount(0);
});
