import { expect, test, type Locator, type Page } from '@playwright/test';
import { createProject, projectNav, workspaceNav } from './helpers';

// Decision P42 against the real Go core: a service is connected once, without a project, on the workspace's Integrations page
// (a list and the chosen row's detail, decision P98), then assigned to a project and unassigned again on that project's own
// Integrations page. The vendor is a PROTOCOL FIXTURE (a test double of Linear's GraphQL API, started by the harness:
// DRYDOCK_E2E_LINEAR_API); the key typed here is made up.

const linearApi = (): string => {
  const u = process.env.DRYDOCK_E2E_LINEAR_API;
  if (!u) throw new Error('DRYDOCK_E2E_LINEAR_API is not set (global setup did not run)');
  return u;
};

/** A kit menu: open it from its trigger and choose an item. */
async function fromMenu(page: Page, trigger: Locator, item: string): Promise<void> {
  await trigger.click();
  await page.getByRole('menuitem', { name: item, exact: true }).click();
}

test("integrations: connect Linear without a project, see its fixed name and logo, assign it on the project's Integrations page, then unassign it", async ({ page }) => {
  // a project exists, but connecting does not ask for it
  await createProject(page, 'gocalc-linear');
  await workspaceNav(page, 'nav-integrations');

  // the five services with app sync, each with its own logo and fixed name, then the vendors that only have agent tools (P47)
  const list = page.getByRole('navigation', { name: 'All integrations' });
  const services = list.getByTestId('list-group-services');
  await expect(services.getByRole('heading', { name: /^Services/, level: 2 })).toBeVisible();
  const rows = services.getByTestId('integration-row');
  await expect(rows).toHaveCount(5);
  await expect(rows.locator('[data-brand]')).toHaveCount(5);
  await expect(list.getByTestId('list-group-tools').locator('[data-testid=integration-row][data-mcp=true][data-kind=cloudflare]')).toHaveCount(1);
  const row = list.locator('[data-testid=integration-row][data-kind=linear]');
  await expect(row).toContainText('Set up');
  await row.click();
  await expect(page).toHaveURL(/\/integrations\/linear$/);
  const card = page.locator('[data-testid=integration][data-kind=linear]');
  await expect(card.getByRole('heading', { name: 'Linear', exact: true })).toBeVisible();
  await expect(card.locator('[data-brand=linear]').first()).toBeVisible();
  await expect(card.getByLabel(/name/i)).toHaveCount(0); // nothing to rename
  // the setup: three steps, the way to connect as one choice
  await expect(card.getByTestId('setup-steps')).toContainText('Assign to a project');
  // the agents' Linear tools (its MCP card) are in the same detail, not on a page of their own
  await expect(page.getByTestId('integration-sync')).toContainText('Linear');
  await expect(page.getByTestId('integration-agents').locator('[data-testid="connector"][data-id="linear"]')).toBeVisible();

  // a Sign in with Linear needs an OAuth app the owner registers with Linear (the vendors' own APIs have no dynamic client
  // registration, unlike their MCP servers): choosing it shows where to register one and takes the app's public client id,
  // kept by the core in its settings table; once saved the sign-in is on. Linear is not reached here (nothing signs in); the
  // id is removed again so the next run starts clean.
  await card.getByLabel('Sign in with Linear').check();
  const setup = card.getByTestId('oauth-app-setup');
  await expect(setup).toContainText('Sign in needs an OAuth app: register one at Linear and paste its client id here.');
  await expect(setup).toContainText('Allow the redirect URL http://127.0.0.1:41739/callback.');
  await expect(setup.getByTestId('oauth-app-register')).toHaveAttribute('href', 'https://linear.app/settings/api/applications/new');
  await expect(card.getByRole('button', { name: 'Sign in with Linear' })).toHaveCount(0);
  await setup.getByLabel('Client ID').fill('lin_client_e2e_made_up');
  await setup.getByRole('button', { name: 'Save the client ID' }).click();
  await expect(card.getByRole('button', { name: 'Sign in with Linear' })).toBeEnabled();
  await expect(card.getByTestId('oauth-app-in-use')).toContainText('you registered (client id lin_client_e2e_made_up)');
  await card.getByRole('button', { name: 'Change it' }).click();
  await card.getByTestId('oauth-app-setup').getByLabel('Client ID').fill('');
  await card.getByTestId('oauth-app-setup').getByRole('button', { name: 'Remove the client ID' }).click();
  await expect(card.getByRole('button', { name: 'Sign in with Linear' })).toHaveCount(0);
  await expect(card.getByTestId('oauth-app-setup')).toBeVisible(); // off again: the registration is offered once more
  await card.getByLabel('Personal API key').check();

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
  await expect(card.getByTestId('detail-status')).toHaveText('Connected');
  expect(await page.content()).not.toContain(key);
  // the row says it in a word, and the header counts it
  await expect(row).toContainText('On');
  await expect(page.locator('header')).toContainText(/· [1-9]\d* connected/);

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
  await expect(row).toContainText('On');
  expect(await page.content()).not.toContain(key);
  expect(await page.content()).not.toContain('From the Linear fixture.'); // item text is never reported

  // Assign to a project opens that project's Integrations page, where the team is read from Linear
  await fromMenu(page, card.getByTestId('assign-to-project'), 'gocalc-linear');
  await expect(page).toHaveURL(/\/project\/integrations$/);
  const mine = page.locator('[data-testid=project-integration][data-kind=linear]');
  await expect(mine).toHaveAttribute('data-assigned', 'false');
  await expect(mine).toContainText('connected as Kyle Fixture');
  // the services are compact rows: Linear's says it is not assigned here
  await expect(page.locator('[data-testid=project-integration-tile][data-kind=linear]').getByTestId('row-status')).toHaveText('not assigned');
  await mine.getByRole('button', { name: 'Assign to gocalc-linear' }).click();
  const form = mine.getByTestId('assign-form');
  await form.getByRole('button', { name: 'List the Team choices from Linear' }).click();
  await expect(form.locator('datalist option')).toHaveCount(2);
  await form.getByLabel('Team (optional)').fill('ENG');
  await form.getByRole('button', { name: 'Assign', exact: true }).click();
  const assignment = mine.locator('[data-testid=assignment]');
  await expect(assignment).toHaveCount(1);
  await expect(assignment).toContainText('gocalc-linear');
  await expect(assignment).toContainText('Team: ENG');
  await assignment.getByRole('button', { name: 'Sync now' }).click();
  // the background sync (every 15 s) may reach a new assignment before the click does: either way both items are in, and
  // the second sync finds nothing new
  await expect(assignment).toContainText(/2 seen: (2 new|0 new, 0 changed, 0 gone)/);
  await assignment.getByRole('button', { name: 'Items' }).click();
  await expect(assignment.getByTestId('ext-item')).toHaveCount(2);
  await expect(assignment.getByTestId('ext-item').first()).toContainText('ENG-10');

  // the workspace page names the project it is assigned to, with Sync now for the connection; Open goes back to the project
  await workspaceNav(page, 'nav-integrations');
  await list.locator('[data-testid=integration-row][data-kind=linear]').click();
  await expect(row).toContainText('· 1 project');
  await expect(card.getByTestId('integration-assigned')).toContainText('Assigned to 1 project.');
  const projectRow = card.getByTestId('integration-project').filter({ hasText: 'gocalc-linear' });
  await expect(projectRow).toContainText('Team: ENG');
  await card.getByRole('button', { name: 'Sync now' }).click();
  await expect(card.getByTestId('integration-synced')).toContainText('seen');
  await projectRow.getByRole('button', { name: 'Open gocalc-linear' }).click();
  await expect(page).toHaveURL(/\/project\/integrations$/);

  // unassign on the project's page: the project's items go, the connection stays
  await assignment.getByRole('button', { name: 'Unassign' }).click();
  await assignment.getByRole('button', { name: 'Yes, unassign from gocalc-linear' }).click();
  await expect(mine.locator('[data-testid=assignment]')).toHaveCount(0);
  await expect(mine).toHaveAttribute('data-assigned', 'false');

  // leave the workspace as the other tests expect it: Disconnect is in the detail's … menu and asks first
  await workspaceNav(page, 'nav-integrations');
  await list.locator('[data-testid=integration-row][data-kind=linear]').click();
  await expect(identity).toContainText('Fixture Org');
  await fromMenu(page, card.getByRole('button', { name: 'More for Linear' }), 'Disconnect');
  await card.getByRole('button', { name: 'Yes, disconnect Linear' }).click();
  await expect(card.getByTestId('detail-status')).toHaveText('Not connected');
  await expect(row).toContainText('Set up');
});

test('integrations: the list filters by a word and by state, and the address keeps the chosen row', async ({ page }) => {
  await createProject(page, 'gocalc-filter');
  await workspaceNav(page, 'nav-integrations');
  const list = page.getByRole('navigation', { name: 'All integrations' });
  await expect(list.getByTestId('integration-row').first()).toBeVisible();
  // / goes to the filter box (from anywhere in the list or the detail); a word narrows the list; Escape clears it
  await list.locator('[data-testid=integration-row][data-kind=linear]').focus();
  await page.keyboard.press('/');
  const box = list.getByTestId('list-filter');
  await expect(box).toBeFocused();
  await page.keyboard.type('notion');
  await expect(list.getByTestId('integration-row')).toHaveCount(1);
  await page.keyboard.press('ArrowDown');
  await expect(page).toHaveURL(/\/integrations\/notion$/);
  await expect(page.locator('[data-testid=integration][data-kind=notion]')).toBeVisible();
  await box.focus();
  await page.keyboard.press('Escape');
  await expect(list.getByTestId('integration-row').first()).toBeVisible();
  expect(await list.getByTestId('integration-row').count()).toBeGreaterThan(5);
  // Needs you shows only what needs the person; nothing does on a fresh workspace
  await list.getByTestId('list-filter-needs').click();
  await expect(list.getByTestId('list-filter-needs')).toHaveAttribute('aria-pressed', 'true');
  await list.getByTestId('list-filter-all').click();
  // the address opens a row, after a reload too
  await page.goto(new URL('/integrations/sentry', page.url()).toString());
  await expect(page.locator('[data-testid=integration][data-kind=sentry]')).toBeVisible();
  await expect(list.locator('[data-key=sentry]')).toHaveAttribute('aria-current', 'true');
  // GitHub's row is its own page
  await list.locator('a[href="/github"]').click();
  await expect(page).toHaveURL(/\/github$/);
});
