import { expect, test } from '@playwright/test';
import { createAgent, createProject } from './helpers';

// Round 6: the Library, against the real Go core. An MCP server is connected once, then dragged into projects and onto agents; a team
// member is defined once as a template and dragged into a project. The server is a real stdio MCP server (the e2e binary re-executed
// with DRYDOCK_E2E_MCP_SERVER=1, a test fixture). What an agent does with it is covered by the Go tests.

test('Library: connect an MCP server once, drag it onto a project and an agent, drag a template into another project; projects stay separate', async ({ page }) => {
  // tall, so a drag never has to scroll the page between pressing the mouse and dropping
  await page.setViewportSize({ width: 1500, height: 3200 });
  await createProject(page, 'lib-a');
  await createAgent(page, 'Pay', 'backend');
  await createAgent(page, 'Rev', 'reviewer');
  await createProject(page, 'lib-b'); // now the current project
  await page.getByTestId('nav-library').click();
  await expect(page.getByRole('heading', { name: 'Library' })).toBeVisible();

  // connect once: a refused address says why, then a real server with a secret
  const exe = process.env.DRYDOCK_E2E_EXE!;
  await page.getByRole('button', { name: 'Connect an MCP server' }).click();
  const form = page.getByTestId('mcp-form');
  await form.getByLabel('Short id', { exact: false }).fill('web');
  await form.getByLabel('Name', { exact: true }).fill('Remote');
  await form.getByLabel('Kind').selectOption('http');
  await form.getByLabel('Address', { exact: false }).fill('http://example.com/mcp');
  await form.getByRole('button', { name: 'Connect server' }).click();
  await expect(form.getByRole('alert')).toContainText('plain http');
  await form.getByLabel('Kind').selectOption('stdio');
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

  // drag the server onto project lib-a
  const projA = page.getByTestId('lib-project').filter({ hasText: 'lib-a' });
  const projB = page.getByTestId('lib-project').filter({ hasText: 'lib-b' });
  // drop on the project itself (its heading), not on an agent inside its card; the open panel's header drags like the tile
  await card.locator('h3').dragTo(projA.getByRole('heading', { name: 'lib-a', exact: true }));
  await expect(page.getByTestId('library-note')).toContainText('Test server is now in lib-a.');
  await expect(projA.getByTestId('proj-server')).toHaveCount(1);
  await expect(projB.getByTestId('proj-server')).toHaveCount(0);
  await expect(card.getByTestId('lib-mcp-projects')).toContainText('lib-a');
  await expect(serverTile).toContainText('In 1 project');

  // drag its tile onto the agent Pay: only Pay may use it
  await serverTile.dragTo(projA.getByTestId('lib-agent').filter({ hasText: 'Pay' }));
  await expect(page.getByTestId('library-note')).toContainText('Pay can now use Test server.');
  await expect(projA.getByTestId('lib-agent').filter({ hasText: 'Pay' }).getByTestId('agent-server')).toHaveCount(1);
  await expect(projA.getByTestId('lib-agent').filter({ hasText: 'Rev' }).getByTestId('agent-server')).toHaveCount(0);

  // a template: defined once, dragged into the other project
  await page.getByRole('button', { name: 'New agent template' }).click();
  const tf = page.getByTestId('template-form');
  await tf.getByLabel('Template name').fill('Payments dev');
  await tf.getByLabel('Role').fill('backend');
  await tf.getByRole('textbox', { name: /^Purpose/ }).fill('Own the payments service.');
  await tf.getByLabel('Model', { exact: true }).fill('sonnet');
  await tf.getByLabel('Effort', { exact: true }).selectOption('high');
  await tf.getByLabel('Template gets Test server').check();
  await tf.getByRole('button', { name: 'Create template' }).click();
  const tpl = page.getByTestId('lib-template').filter({ hasText: 'Payments dev' });
  await expect(tpl).toBeVisible();
  const tplTile = page.getByTestId('lib-template-tile').filter({ hasText: 'Payments dev' });
  await expect(tplTile).toContainText('Gets 1 server');
  await tplTile.dragTo(projB.getByRole('heading', { name: 'lib-b', exact: true }));
  await expect(page.getByTestId('library-note')).toContainText('Payments dev joined lib-b, with Test server');
  const newAgent = projB.getByTestId('lib-agent').filter({ hasText: 'Payments dev' });
  await expect(newAgent).toBeVisible();
  await expect(newAgent.getByTestId('agent-server')).toHaveCount(1);
  await expect(projB.getByTestId('proj-server')).toHaveCount(1); // granting also put the server into lib-b

  // the project's own page agrees (lib-b is current), and the agent kept the template's settings
  await page.getByTestId('nav-mcp').click();
  const onB = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(onB.getByLabel('Payments dev may use Test server')).toBeChecked();
  await expect(onB.getByText('reads only')).toHaveCount(1);
  await page.getByTestId('nav-team').click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Payments dev' })).toContainText('effort high');

  // disconnecting removes it from every project
  // (a fresh visit shows only the tiles: clicking one opens its panel)
  await page.getByTestId('nav-library').click();
  await expect(page.getByTestId('lib-mcp')).toHaveCount(0);
  await page.getByTestId('lib-mcp-tile').filter({ hasText: 'Test server' }).click();
  const again = page.getByTestId('lib-mcp').filter({ hasText: 'Test server' });
  await again.getByRole('button', { name: 'Disconnect' }).click();
  await again.getByRole('button', { name: 'Yes, disconnect' }).click();
  await expect(page.getByTestId('lib-mcp-tile')).toHaveCount(0);
  await expect(page.getByTestId('lib-mcp')).toHaveCount(0);
  await expect(projA.getByTestId('proj-server')).toHaveCount(0);
  await expect(projB.getByTestId('proj-server')).toHaveCount(0);
});
