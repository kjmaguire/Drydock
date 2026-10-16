import { expect, test } from '@playwright/test';
import { createAgent, createProject, projectNav, workspaceNav } from './helpers';

// Round 16: the MCP connector cards (decision P43) on the Integrations page (decision P47), against the real Go core. The
// harness adds one card to the built-in catalog, "Hosted tracker (test double)", whose server is internal/mcp/oauthfake: a
// TEST DOUBLE of a hosted MCP server that wants an OAuth sign-in. Everything on Drydock's side is real: Connect adds it to the
// MCP library, starts the sign-in, the browser comes back to the core's loopback address, the core reaches the server and
// learns its tools, its serverInfo and the account from its whoami tool, and the card says Connected as that account. Then it
// is added to a project and granted to an agent on that project's Integrations page (the workspace's card names no project).
// No real vendor server is contacted: the other cards are only shown.

test('connector cards: connect a catalog server with OAuth, see it Connected and add it to a project and grant it to an agent', async ({ page, context }) => {
  await createProject(page, 'gallery-a');
  await createAgent(page, 'Tracker reader', 'backend');
  // the old MCP servers address is an alias of Integrations
  await page.goto(new URL('/mcp', page.url()).toString());
  await expect(page.getByRole('heading', { name: 'Integrations', level: 1 })).toBeVisible();
  await expect(page.getByTestId('nav-integrations')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByTestId('nav-mcp')).toHaveCount(0);
  await workspaceNav(page, 'nav-integrations');
  await expect(page.getByRole('heading', { name: 'Integrations', level: 1 })).toBeVisible();

  const gallery = page.getByTestId('connector-gallery');
  // the real catalog's cards sit on their vendor's tile: logo, name and publisher, nothing connected
  await gallery.locator('[data-testid="integration-tile"][data-kind="github"]').click();
  const github = gallery.locator('[data-testid="connector"][data-id="gh"]');
  await expect(github.locator('h4')).toHaveText('GitHub');
  await expect(github.locator('[data-brand="github"]')).toHaveCount(1);
  await expect(github.getByTestId('connector-status')).toHaveText('Not connected');
  // the two Cloudflare servers share one tile
  const cfTile = gallery.locator('[data-testid="integration-tile"][data-kind="cloudflare"][data-mcp="true"]');
  await expect(cfTile).toContainText('Agents: not connected');
  await cfTile.click();
  await expect(gallery.getByTestId('integration-agents').locator('[data-testid="connector"]')).toHaveCount(2);
  await expect(gallery.locator('[data-testid="connector"][data-id="cfapi"]')).toContainText('Cloudflare');
  await expect(gallery.getByTestId('integration-sync')).toHaveCount(0); // no app sync for Cloudflare

  // the test double has no app sync either: an MCP-only tile of its own
  const trkTile = gallery.locator('[data-testid="integration-tile"][data-kind="faketrk"]');
  await expect(trkTile).toContainText('Hosted tracker (test double)');
  await trkTile.click();
  const card = gallery.locator('[data-testid="connector"][data-id="faketrk"]');
  await expect(card.locator('h4')).toHaveText('Hosted tracker (test double)');
  await card.getByRole('button', { name: 'Connect Hosted tracker (test double)' }).click();
  await expect(card.getByTestId('connector-status')).toHaveText('Needs sign-in');
  const link = card.getByTestId('oauth-link');
  await expect(link).toBeVisible({ timeout: 30_000 });
  const href = await link.getAttribute('href');
  expect(href).toContain('/authorize');

  // the human's browser: the provider approves and sends the browser back to the core's loopback address
  const tab = await context.newPage();
  await tab.goto(href!);
  await expect(tab.locator('body')).toContainText('You are signed in');
  await tab.close();

  await expect(card.getByTestId('connector-status')).toHaveText('Connected', { timeout: 30_000 });
  await expect(trkTile).toContainText('Agents: connected');
  await expect(card.getByTestId('connector-account')).toContainText('Connected as alice');
  await expect(card.getByTestId('connector-account')).toContainText('OAuth test double (oauthy 1)');
  await card.getByText('1 tool', { exact: false }).click();
  await expect(card.getByTestId('connector-tool')).toHaveText(/whoami/);

  // the workspace's card names no project and offers no project controls: those are on the project's own Integrations page
  await expect(card.getByTestId('connector-projects')).toContainText("on that project's Integrations page");
  await expect(card.getByRole('button', { name: 'Add to project' })).toHaveCount(0);
  await expect(card.getByRole('button', { name: 'Grant to agents' })).toHaveCount(0);
  await expect(page.getByTestId('mcp-server')).toHaveCount(0);

  // add it to the project from the project's Integrations page, where the connected built-in server is in the library
  await projectNav(page, 'nav-project-integrations', 'gallery-a');
  await expect(page.getByRole('heading', { name: 'Integrations', level: 1 })).toBeVisible();
  const lib = page.getByTestId('add-from-library').getByTestId('library-server').filter({ hasText: 'Hosted tracker (test double)' });
  await expect(lib).toContainText('built into Drydock');
  await lib.getByRole('button', { name: 'Add Hosted tracker (test double) to this project' }).click();
  const inProject = page.locator('[data-testid="mcp-server"]').filter({ hasText: 'Hosted tracker (test double)' });
  await expect(inProject).toBeVisible();
  // and grant it to the project's agent there
  await inProject.getByRole('checkbox', { name: 'Tracker reader may use Hosted tracker (test double)' }).check();
  await expect(inProject.getByRole('checkbox', { name: 'Tracker reader may use Hosted tracker (test double)' })).toBeChecked();

  // the workspace's card counts it
  await workspaceNav(page, 'nav-integrations');
  await gallery.locator('[data-testid="integration-tile"][data-kind="faketrk"]').click();
  await expect(card.getByTestId('connector-projects')).toContainText('In 1 project.');

  // Disconnect puts the card back to Not connected and takes it out of the project
  await card.getByRole('button', { name: 'Disconnect' }).click();
  await card.getByRole('button', { name: 'Yes, disconnect' }).click();
  await expect(card.getByTestId('connector-status')).toHaveText('Not connected');
  await projectNav(page, 'nav-project-integrations', 'gallery-a');
  await expect(page.getByTestId('no-project-servers')).toBeVisible();
  await expect(inProject).toHaveCount(0);
});

test('the Library lists the MCP servers built into Drydock before the custom server, and an OAuth card there shows one Sign in', async ({ page }) => {
  await createProject(page, 'gallery-lib');
  await workspaceNav(page, 'nav-library');
  await page.getByRole('button', { name: 'Connect an MCP server' }).click();
  const dialog = page.getByRole('dialog', { name: 'Connect an MCP server' });
  const prebuilt = dialog.getByTestId('mcp-add-prebuilt');
  const custom = dialog.getByTestId('mcp-add-custom');
  await expect(prebuilt.getByRole('heading', { name: 'Built into Drydock' })).toBeVisible();
  await expect(custom.getByRole('heading', { name: 'Add a custom MCP server' })).toBeVisible();
  // the built-in servers come first, as the Integrations page's cards: logo, publisher, license, Connect
  const order = await dialog.locator('[data-testid="mcp-add-prebuilt"], [data-testid="mcp-add-custom"]').evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')));
  expect(order).toEqual(['mcp-add-prebuilt', 'mcp-add-custom']);
  const github = prebuilt.locator('[data-testid="connector"][data-id="gh"]');
  await expect(github.locator('[data-brand="github"]')).toHaveCount(1);
  await expect(github).toContainText('License');
  await expect(github.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
  // the custom form waits behind its button
  await expect(dialog.getByTestId('mcp-form')).toHaveCount(0);
  await custom.getByRole('button', { name: 'Add a custom server' }).click();
  await expect(dialog.getByTestId('mcp-form')).toBeVisible();
  await expect(custom.getByRole('button', { name: 'Import from a file' })).toHaveCount(0); // the form replaced the two buttons
});

test('prebuilt agents: opening one shows its skills with what they do, where they come from and their license, and its MCP servers by name', async ({ page }) => {
  await createProject(page, 'prebuilt-details');
  await workspaceNav(page, 'nav-library');
  const lib = page.getByTestId('library-prebuilt');
  const ui = lib.locator('[data-testid="prebuilt"][data-id="ui-engineer"]');
  // the badges are readable: the server by its full name, the skills listed on the badge
  await expect(ui.getByTestId('prebuilt-server-chip')).toHaveText('Context7');
  await expect(ui.getByTestId('prebuilt-skills-chip')).toHaveAttribute('title', 'Skills: frontend-design, web-design-guidelines, react-best-practices, composition-patterns');
  await ui.getByRole('button', { name: 'What UI Engineer comes with' }).click();
  const d = page.getByTestId('prebuilt-details');
  await expect(d.getByTestId('prebuilt-model')).toHaveText('sonnet');
  const fd = d.locator('[data-testid="prebuilt-skill"][data-skill="frontend-design"]');
  await expect(fd).toContainText('Guidance for distinctive, intentional visual design');
  await expect(fd).toContainText('Apache-2.0');
  await expect(fd).toContainText('pinned at 683bc88e56f3');
  await expect(fd.getByRole('link', { name: /anthropics\/skills/ })).toHaveAttribute('href', 'https://github.com/anthropics/skills/tree/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/frontend-design');
  const ctx7 = d.locator('[data-testid="prebuilt-server"][data-server="ctx7"]');
  await expect(ctx7).toContainText('Context7');
  await expect(ctx7).toContainText('by Upstash');
  await expect(ctx7.locator('[data-brand="context7"]')).toHaveCount(1);
});
