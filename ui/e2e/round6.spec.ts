import { expect, test } from '@playwright/test';
import { createAgent, createProject } from './helpers';

// Round 6: MCP servers the human adds to a project, against the real Go core. The server is a real stdio MCP server (the e2e
// binary re-executed with DRYDOCK_E2E_MCP_SERVER=1, a test fixture). What an agent does with it is covered by the Go tests.

test('MCP servers: refused values say why; add a real server, find its tools, mark one read-only, grant it to one agent; secrets are never shown again', async ({ page }) => {
  await createProject(page, 'mcp-servers');
  await createAgent(page, 'Pay', 'backend');
  await createAgent(page, 'Rev', 'reviewer');
  await page.getByTestId('nav-mcp').click();
  await expect(page.getByRole('heading', { name: 'MCP servers' })).toBeVisible();
  await expect(page.getByText('No MCP servers on this project yet.')).toBeVisible();

  const exe = process.env.DRYDOCK_E2E_EXE!;
  await page.getByRole('button', { name: 'Add an MCP server' }).click();
  const form = page.getByTestId('mcp-form');

  // a plain-http address to a remote host is refused by the core
  await form.getByLabel('Short id', { exact: false }).fill('web');
  await form.getByLabel('Name', { exact: true }).fill('Remote');
  await form.getByLabel('Kind').selectOption('http');
  await form.getByLabel('Address', { exact: false }).fill('http://example.com/mcp');
  await form.getByRole('button', { name: 'Add server' }).click();
  await expect(form.getByRole('alert')).toContainText('plain http');

  // a real stdio server with a secret
  await form.getByLabel('Kind').selectOption('stdio');
  await form.getByLabel('Short id', { exact: false }).fill('tst');
  await form.getByLabel('Name', { exact: true }).fill('Test server');
  await form.getByLabel('Command, one argument per line', { exact: false }).fill(exe);
  await form.getByLabel('Environment, NAME=value per line', { exact: false }).fill('DRYDOCK_E2E_MCP_SERVER=1');
  await form.getByRole('button', { name: 'Add a secret' }).click();
  await form.getByLabel('Secret name 1').fill('API_KEY');
  await form.getByLabel('Secret value 1').fill('sekret-e2e-value-123456');
  await form.getByRole('button', { name: 'Add server' }).click();

  const card = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(card).toBeVisible();
  await expect(card.getByTestId('mcp-secrets')).toContainText('API_KEY');
  await expect(page.locator('body')).not.toContainText('sekret-e2e-value-123456');

  // find the tools by really starting it
  await card.getByRole('button', { name: 'Find tools' }).click();
  await expect(card.getByTestId('mcp-tool')).toHaveCount(3);
  await expect(card.getByTestId('mcp-tool').filter({ hasText: 'echo' })).toContainText('the server says it only reads');

  // the human, not the server, decides what only reads
  const echo = card.getByLabel('echo only reads');
  await expect(echo).not.toBeChecked();
  await echo.click(); // the box follows the stored value, which changes after the core answers
  await expect(echo).toBeChecked();

  // deny by default: grant it to one agent
  const pay = card.getByLabel('Pay may use Test server');
  await expect(pay).not.toBeChecked();
  await pay.click();
  await expect(pay).toBeChecked();
  await expect(card.getByLabel('Rev may use Test server')).not.toBeChecked();

  // it all survives a reload, and the secret is still not on the page
  await page.reload();
  await page.getByTestId('nav-mcp').click();
  const again = page.getByTestId('mcp-server').filter({ hasText: 'Test server' });
  await expect(again.getByLabel('echo only reads')).toBeChecked();
  await expect(again.getByLabel('Pay may use Test server')).toBeChecked();
  await expect(again.getByLabel('Rev may use Test server')).not.toBeChecked();
  await expect(page.locator('body')).not.toContainText('sekret-e2e-value-123456');

  // removal needs a confirmation
  await again.getByRole('button', { name: 'Remove', exact: true }).click();
  await again.getByRole('button', { name: 'Yes, remove' }).click();
  await expect(page.getByText('No MCP servers on this project yet.')).toBeVisible();
});
