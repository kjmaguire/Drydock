import { expect, test } from '@playwright/test';
import { createProject, projectNav, workspaceNav } from './helpers';

// Decision P42 against the real Go core: a service is connected once, without a project, on the workspace's Integrations page,
// then assigned to a project and unassigned again on that project's own Integrations page. The vendor is a PROTOCOL FIXTURE (a test double of Linear's GraphQL API, started by the harness:
// DRYDOCK_E2E_LINEAR_API); the key typed here is made up.

const linearApi = (): string => {
  const u = process.env.DRYDOCK_E2E_LINEAR_API;
  if (!u) throw new Error('DRYDOCK_E2E_LINEAR_API is not set (global setup did not run)');
  return u;
};

test("integrations: connect Linear without a project, see its fixed name and logo, assign it on the project's Integrations page, then unassign it", async ({ page }) => {
  // a project exists, but connecting does not ask for it
  await createProject(page, 'gocalc-linear');
  await workspaceNav(page, 'nav-integrations');

  // the five services with app sync, each with its own logo and fixed name, then the vendors that only have agent tools (P47)
  const tiles = page.locator('[data-testid=integration-tile]:not([data-mcp])');
  await expect(tiles).toHaveCount(5);
  await expect(tiles.locator('[data-brand]')).toHaveCount(5);
  await expect(page.locator('[data-testid=integration-tile][data-mcp=true][data-kind=cloudflare]')).toHaveCount(1);
  await page.locator('[data-testid=integration-tile][data-kind=linear]').click();
  const card = page.locator('[data-testid=integration][data-kind=linear]');
  await expect(card.getByRole('heading', { name: 'Linear', exact: true })).toBeVisible();
  await expect(card.getByRole('img', { name: 'Linear' })).toBeVisible();
  await expect(card.getByLabel(/name/i)).toHaveCount(0); // nothing to rename
  // the agents' Linear tools (its MCP card) are on the same tile, not on a page of their own
  await expect(page.getByTestId('integration-sync')).toContainText('Linear');
  await expect(page.getByTestId('integration-agents').locator('[data-testid="connector"][data-id="linear"]')).toBeVisible();

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
  await expect(card.getByTestId('integration-assigned')).toContainText('Not assigned to a project yet.');
  expect(await page.content()).not.toContain(key);
  const tile = page.locator('[data-testid=integration-tile][data-kind=linear]');
  await expect(tile).toContainText('Sync: connected as Kyle Fixture');
  await expect(tile).toContainText('Agents: not connected');
  // the workspace page assigns nothing: that is the project's own Integrations page
  await expect(card.getByRole('button', { name: 'Assign to project' })).toHaveCount(0);

  // the page reads like Home: the services as one group of tiles under a heading, each with a state badge
  const servicesGroup = page.getByTestId('integrations-services');
  await expect(servicesGroup.getByRole('heading', { name: 'Services', level: 2 })).toBeVisible();
  await expect(tile.getByTestId('tile-badge')).toHaveText('connected');

  // Check against your account (P59): the same read-only checks as the live tests, with the key Drydock already holds
  // (nothing is typed or shown), here against the fixture standing in for the account
  await expect(card.getByTestId('integration-verified')).toContainText('Not checked against your account yet');
  await card.getByRole('button', { name: 'Check against your account' }).click();
  const check = card.getByTestId('verify-report');
  await expect(check).toHaveAttribute('data-ok', 'true');
  await expect(check).toContainText('nothing differed from what Drydock expects');
  await expect(check.getByTestId('verify-read')).toContainText('Items read3'); // not assigned yet: everything the key can see
  await expect(check.getByTestId('verify-call')).toHaveCount(3);
  await expect(check.getByTestId('verify-call').first()).toHaveAttribute('data-status', '200');
  await expect(card.getByTestId('integration-verified')).toContainText('Verified against your account on');
  await expect(tile.getByTestId('tile-badge')).toHaveText('verified');
  expect(await page.content()).not.toContain(key);
  expect(await page.content()).not.toContain('From the Linear fixture.'); // item text is never reported

  // assign it to the project from the project's Integrations page, with the team read from Linear
  await projectNav(page, 'nav-project-integrations', 'gocalc-linear');
  const mine = page.locator('[data-testid=project-integration][data-kind=linear]');
  await expect(mine).toHaveAttribute('data-assigned', 'false');
  await expect(mine).toContainText('connected as Kyle Fixture');
  await mine.getByRole('button', { name: 'Assign to gocalc-linear' }).click();
  const form = mine.getByTestId('assign-form');
  await form.getByRole('button', { name: 'List the Team choices from Linear' }).click();
  await expect(form.locator('datalist option')).toHaveCount(2);
  await form.getByLabel('Team (optional)').fill('ENG');
  await form.getByRole('button', { name: 'Assign', exact: true }).click();
  const row = mine.locator('[data-testid=assignment]');
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('gocalc-linear');
  await expect(row).toContainText('Team: ENG');
  await row.getByRole('button', { name: 'Sync now' }).click();
  await expect(row).toContainText('2 seen: 2 new');
  await row.getByRole('button', { name: 'Items' }).click();
  await expect(row.getByTestId('ext-item')).toHaveCount(2);
  await expect(row.getByTestId('ext-item').first()).toContainText('ENG-10');

  // the workspace page counts the assignment without naming the project
  await workspaceNav(page, 'nav-integrations');
  await page.locator('[data-testid=integration-tile][data-kind=linear]').click();
  await expect(card.getByTestId('integration-assigned')).toContainText('Assigned to 1 project.');
  await expect(card).not.toContainText('gocalc-linear');

  // unassign on the project's page: the project's items go, the connection stays
  await projectNav(page, 'nav-project-integrations', 'gocalc-linear');
  await row.getByRole('button', { name: 'Unassign' }).click();
  await row.getByRole('button', { name: 'Yes, unassign from gocalc-linear' }).click();
  await expect(mine.locator('[data-testid=assignment]')).toHaveCount(0);
  await expect(mine).toHaveAttribute('data-assigned', 'false');

  // leave the workspace as the other tests expect it
  await workspaceNav(page, 'nav-integrations');
  await page.locator('[data-testid=integration-tile][data-kind=linear]').click();
  await expect(identity).toContainText('Fixture Org');
  await card.getByRole('button', { name: 'Disconnect' }).click();
  await card.getByRole('button', { name: 'Yes, disconnect Linear' }).click();
  await expect(page.getByText('Nothing is connected.')).toBeVisible();
});
