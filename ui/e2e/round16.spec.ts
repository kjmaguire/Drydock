import { expect, test } from '@playwright/test';
import { createAgent, createProject } from './helpers';

// Round 16: the MCP connector gallery (decision P43), against the real Go core. The harness adds one card to the built-in
// catalog, "Hosted tracker (test double)", whose server is internal/mcp/oauthfake: a TEST DOUBLE of a hosted MCP server that
// wants an OAuth sign-in. Everything on Drydock's side is real: Connect adds it to the MCP library, starts the sign-in, the
// browser comes back to the core's loopback address, the core reaches the server and learns its tools, its serverInfo and the
// account from its whoami tool, and the card says Connected as that account. Then it is added to a project and granted to an
// agent from the card. No real vendor server is contacted: the other cards are only shown.

test('connector gallery: connect a catalog server with OAuth, see it Connected and add it to a project and grant it to an agent', async ({ page, context }) => {
  await createProject(page, 'gallery-a');
  await createAgent(page, 'Tracker reader', 'backend');
  await page.getByTestId('nav-mcp').click();
  await expect(page.getByRole('heading', { name: 'MCP servers', level: 1 })).toBeVisible();

  const gallery = page.getByTestId('connector-gallery');
  // the real catalog's cards: logo, name and publisher, nothing connected
  const github = gallery.locator('[data-testid="connector"][data-id="gh"]');
  await expect(github.locator('h4')).toHaveText('GitHub');
  await expect(github.locator('[data-brand="github"]')).toHaveCount(1);
  await expect(github.getByTestId('connector-status')).toHaveText('Not connected');
  await expect(gallery.locator('[data-testid="connector"][data-id="cfapi"]')).toContainText('Cloudflare');
  await expect(gallery.getByRole('region', { name: 'Cloudflare' })).toBeVisible();

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
  await expect(card.getByTestId('connector-account')).toContainText('Connected as alice');
  await expect(card.getByTestId('connector-account')).toContainText('OAuth test double (oauthy 1)');
  await card.getByText('1 tool', { exact: false }).click();
  await expect(card.getByTestId('connector-tool')).toHaveText(/whoami/);

  // add it to the project from the card
  await card.getByRole('button', { name: 'Add to project' }).click();
  await expect(card).toContainText('In: gallery-a.');
  const inProject = page.locator('[data-testid="mcp-server"]').filter({ hasText: 'Hosted tracker (test double)' });
  await expect(inProject).toBeVisible();
  // and grant it to the project's agent, from the card
  await card.getByRole('button', { name: 'Grant to agents' }).click();
  await card.getByRole('checkbox', { name: 'Tracker reader may use Hosted tracker (test double)' }).check();
  await expect(inProject.getByRole('checkbox', { name: 'Tracker reader may use Hosted tracker (test double)' })).toBeChecked();

  // Disconnect puts the card back to Not connected and takes it out of the project
  await card.getByRole('button', { name: 'Disconnect' }).click();
  await card.getByRole('button', { name: 'Yes, disconnect' }).click();
  await expect(card.getByTestId('connector-status')).toHaveText('Not connected');
  await expect(inProject).toHaveCount(0);
});

test('prebuilt agents: opening one shows its skills with what they do, where they come from and their license, and its MCP servers by name', async ({ page }) => {
  await createProject(page, 'prebuilt-details');
  await page.getByTestId('nav-library').click();
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
