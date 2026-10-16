import { expect, test } from '@playwright/test';
import { choose, createAgent, createProject, workspaceNav, projectNav, switchProject } from './helpers';

// Round 6: the Library, against the real Go core. An MCP server is connected once, for the workspace, in the Library (the dialog
// lists the servers built into Drydock first, then a custom server); a project adds it from its own Integrations page and grants
// it to one of its agents there. A team member is defined once as a template and added to another project from its Team page.
// The Library itself names no project (decision P51). The server is a real stdio MCP server (the e2e binary re-executed with
// DRYDOCK_E2E_MCP_SERVER=1, a test fixture). What an agent does with it is covered by the Go tests.

test("Library: connect an MCP server once; a project adds it and grants it to an agent on its Integrations page; a template joins another project's team; projects stay separate", async ({ page }) => {
  await createProject(page, 'lib-a');
  await createAgent(page, 'Pay', 'backend');
  await createAgent(page, 'Rev', 'reviewer');
  await createProject(page, 'lib-b'); // now the current project
  await workspaceNav(page, 'nav-library');
  await expect(page.getByRole('heading', { level: 1, name: 'Library' })).toBeVisible();

  // connect once: the built-in servers come first, then a custom server; a refused address says why, then a real server with a secret
  const exe = process.env.DRYDOCK_E2E_EXE!;
  await page.getByRole('button', { name: 'Connect an MCP server' }).click();
  const prebuilt = page.getByTestId('mcp-add-prebuilt');
  await expect(prebuilt.getByTestId('connector').first()).toBeVisible();
  const custom = page.getByTestId('mcp-add-custom');
  await custom.getByRole('button', { name: 'Add a custom server' }).click();
  const form = page.getByTestId('mcp-form');
  await form.getByLabel('Short id', { exact: false }).fill('web');
  await form.getByLabel('Name', { exact: true }).fill('Remote');
  await choose(form.getByLabel('Kind'), 'http');
  await form.getByLabel('Address', { exact: false }).fill('http://example.com/mcp');
  await form.getByRole('button', { name: 'Connect server' }).click();
  await expect(form.getByRole('alert')).toContainText('plain http');
  await choose(form.getByLabel('Kind'), 'stdio');
  await form.getByLabel('Short id', { exact: false }).fill('tst');
  await form.getByLabel('Name', { exact: true }).fill('Test server');
  await form.getByLabel('Command, one argument per line', { exact: false }).fill(exe);
  await form.getByLabel('Environment, NAME=value per line', { exact: false }).fill('DRYDOCK_E2E_MCP_SERVER=1');
  await form.getByRole('button', { name: 'Add a secret' }).click();
  await form.getByLabel('Secret name 1').fill('API_KEY');
  await form.getByLabel('Secret value 1').fill('sekret-e2e-value-123456');
  await form.getByRole('button', { name: 'Connect server' }).click();

  // the new server is a tile in the grid, selected, with its panel open below the grid
  const serverTile = page.getByTestId('lib-mcp-tile').filter({ hasText: 'Test server' });
  await expect(serverTile).toHaveAttribute('aria-pressed', 'true');
  await expect(serverTile).toContainText('Tools not found yet');
  const card = page.getByTestId('lib-mcp').filter({ hasText: 'Test server' });
  await expect(card).toBeVisible();
  await expect(card.getByTestId('mcp-secrets')).toContainText('API_KEY');
  await expect(page.locator('body')).not.toContainText('sekret-e2e-value-123456');
  await expect(card.getByTestId('lib-mcp-projects')).toContainText('Not in any project yet');

  // find its tools by really starting it; the human decides which tool only reads
  await card.getByRole('button', { name: 'Find tools' }).click();
  await expect(card.getByTestId('mcp-tool')).toHaveCount(4);
  // the fixture's poisoned tool is held back, the others are not
  await expect(card.getByTestId('mcp-tool-flagged')).toHaveCount(1);
  await expect(card.locator('[data-testid="mcp-tool"][data-tool="format"]').getByTestId('mcp-tool-flagged')).toHaveText('held back');
  await card.getByLabel('echo only reads').click();
  await expect(card.getByLabel('echo only reads')).toBeChecked();
  // the Library is a workspace page: no project is named on it, nothing is dragged onto one
  await expect(page.getByTestId('lib-project')).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'Library' })).not.toContainText('lib-a');

  // a team member: defined once in the Library
  await page.getByRole('button', { name: 'New agent template' }).click();
  const tf = page.getByTestId('template-form');
  await tf.getByLabel('Template name').fill('Payments dev');
  await tf.getByLabel('Role').fill('backend');
  await tf.getByRole('textbox', { name: /^Purpose/ }).fill('Own the payments service.');
  await choose(tf.getByLabel('Model', { exact: true }), 'sonnet');
  await choose(tf.getByLabel('Effort', { exact: true }), 'high');
  await tf.getByLabel('Template gets Test server').check();
  await tf.getByRole('button', { name: 'Create template' }).click();
  const tpl = page.getByTestId('lib-template').filter({ hasText: 'Payments dev' });
  await expect(tpl).toBeVisible();
  const tplTile = page.getByTestId('lib-template-tile').filter({ hasText: 'Payments dev' });
  await expect(tplTile).toContainText('Gets 1 server');

  // project lib-a adds the server from its own Integrations page, and only Pay may use it
  await switchProject(page, 'lib-a');
  await projectNav(page, 'nav-project-integrations');
  await expect(page.getByTestId('project-integrations')).toBeVisible();
  const lib = page.getByTestId('add-from-library');
  await lib.getByRole('button', { name: 'Add Test server to this project' }).click();
  const onA = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(onA).toBeVisible();
  await expect(lib.locator('[data-testid="library-server"][data-server="tst"]')).toHaveCount(0);
  await onA.getByLabel('Pay may use Test server').check();
  await expect(onA.getByLabel('Pay may use Test server')).toBeChecked();
  await expect(onA.getByLabel('Rev may use Test server')).not.toBeChecked();
  await expect(onA.getByText('reads only')).toHaveCount(1);

  // the template joins lib-b's team from its Team page, with the server it names
  await switchProject(page, 'lib-b');
  await projectNav(page, 'nav-team');
  await expect(page.getByLabel('Template', { exact: true })).toContainText('Payments dev'); // the only template, chosen
  await page.getByRole('button', { name: 'Add to this team' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Payments dev' })).toContainText('effort high');

  // lib-b's Integrations page agrees: granting put the server into lib-b, for Payments dev only; lib-a's grants are not here
  await projectNav(page, 'nav-project-integrations');
  const onB = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(onB.getByLabel('Payments dev may use Test server')).toBeChecked();
  await expect(onB.getByLabel('Pay may use Test server')).toHaveCount(0);

  // the Library counts the projects without naming them
  await workspaceNav(page, 'nav-library');
  await expect(page.getByTestId('lib-mcp-tile').filter({ hasText: 'Test server' })).toContainText('In 2 projects');

  // disconnecting removes it from every project
  // (a fresh visit shows only the tiles: clicking one opens its panel)
  await expect(page.getByTestId('lib-mcp')).toHaveCount(0);
  await page.getByTestId('lib-mcp-tile').filter({ hasText: 'Test server' }).click();
  const again = page.getByTestId('lib-mcp').filter({ hasText: 'Test server' });
  await expect(again.getByTestId('lib-mcp-projects')).toContainText('In 2 projects.');
  await again.getByRole('button', { name: 'Disconnect' }).click();
  await again.getByRole('button', { name: 'Yes, disconnect' }).click();
  await expect(page.getByTestId('lib-mcp-tile')).toHaveCount(0);
  await expect(page.getByTestId('lib-mcp')).toHaveCount(0);
  await projectNav(page, 'nav-project-integrations', 'lib-a');
  await expect(page.getByTestId('no-project-servers')).toBeVisible();
  await expect(page.getByTestId('mcp-server')).toHaveCount(0);
});
