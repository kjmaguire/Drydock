import { expect, test } from '@playwright/test';
import { createProject } from './helpers';

// Decision P42 against the real Go core: a service is connected once, without a project, then assigned to a project and
// unassigned again. The vendor is a PROTOCOL FIXTURE (a test double of Linear's GraphQL API, started by the harness:
// DRYDOCK_E2E_LINEAR_API); the key typed here is made up.

const linearApi = (): string => {
  const u = process.env.DRYDOCK_E2E_LINEAR_API;
  if (!u) throw new Error('DRYDOCK_E2E_LINEAR_API is not set (global setup did not run)');
  return u;
};

test('integrations: connect Linear without a project, see its fixed name and logo, assign it to a project, then unassign it', async ({ page }) => {
  // a project exists, but connecting does not ask for it
  await createProject(page, 'gocalc-linear');
  await page.getByTestId('nav-integrations').click();

  // five services, each with its own logo and fixed name
  const tiles = page.getByTestId('integration-tile');
  await expect(tiles).toHaveCount(5);
  await expect(tiles.locator('[data-brand]')).toHaveCount(5);
  await page.locator('[data-testid=integration-tile][data-kind=linear]').click();
  const card = page.locator('[data-testid=integration][data-kind=linear]');
  await expect(card.getByRole('heading', { name: 'Linear', exact: true })).toBeVisible();
  await expect(card.getByRole('img', { name: 'Linear' })).toBeVisible();
  await expect(card.getByLabel(/name/i)).toHaveCount(0); // nothing to rename
  await expect(card.getByRole('link', { name: "Also give agents Linear's tools: MCP" })).toHaveAttribute('href', '/mcp');

  // connect: an API key (cleared from the page) and, under Advanced, the fixture's address instead of api.linear.app
  const key = 'lin_api_e2e_made_up_key_123';
  await card.getByLabel('API key', { exact: true }).fill(key);
  await card.getByText('Advanced').click();
  await card.getByLabel('API URL').fill(linearApi());
  await card.getByRole('button', { name: 'Connect Linear' }).click();
  // what the connection is comes from the API: the viewer and the organization
  const identity = card.getByTestId('connection-identity');
  await expect(identity).toContainText('Kyle Fixture');
  await expect(identity).toContainText('Fixture Org');
  await expect(identity).toContainText('Personal API key');
  await expect(card.getByTestId('int-report')).toHaveAttribute('data-ok', 'true');
  await expect(card.getByText('Not assigned to a project yet.')).toBeVisible();
  expect(await page.content()).not.toContain(key);
  const tile = page.locator('[data-testid=integration-tile][data-kind=linear]');
  await expect(tile).toContainText('Connected: Kyle Fixture in Fixture Org');
  await expect(tile).toContainText('Not assigned to a project');

  // assign it to the project, with the team read from Linear
  await card.getByRole('button', { name: 'Assign to project' }).click();
  const form = card.getByTestId('assign-form');
  await form.getByLabel('Project', { exact: true }).selectOption({ label: 'gocalc-linear' });
  await form.getByRole('button', { name: 'List the Team choices from Linear' }).click();
  await expect(form.locator('datalist option')).toHaveCount(2);
  await form.getByLabel('Team (optional)').fill('ENG');
  await form.getByRole('button', { name: 'Assign', exact: true }).click();
  const row = card.locator('[data-testid=assignment]');
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('gocalc-linear');
  await expect(row).toContainText('Team: ENG');
  await expect(tile).toContainText('Assigned to 1 project');
  await row.getByRole('button', { name: 'Sync now' }).click();
  await expect(row).toContainText('2 seen: 2 new');
  await row.getByRole('button', { name: 'Items' }).click();
  await expect(row.getByTestId('ext-item')).toHaveCount(2);
  await expect(row.getByTestId('ext-item').first()).toContainText('ENG-10');

  // unassign: the project's items go, the connection stays
  await row.getByRole('button', { name: 'Unassign' }).click();
  await row.getByRole('button', { name: 'Yes, unassign from gocalc-linear' }).click();
  await expect(card.locator('[data-testid=assignment]')).toHaveCount(0);
  await expect(card.getByText('Not assigned to a project yet.')).toBeVisible();
  await expect(identity).toContainText('Fixture Org');

  // leave the workspace as the other tests expect it
  await card.getByRole('button', { name: 'Disconnect' }).click();
  await card.getByRole('button', { name: 'Yes, disconnect Linear' }).click();
  await expect(page.getByText('Nothing is connected.')).toBeVisible();
});
