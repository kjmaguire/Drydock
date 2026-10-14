import { expect, test } from '@playwright/test';
import { createProject, workspaceNav } from './helpers';

// Round 9: OAuth sign-in of a web MCP server, against the real Go core. The provider is a test double (internal/mcp/oauthfake: an
// MCP server that wants a token, with discovery, dynamic client registration, PKCE, token and refresh); Drydock's side is the real
// flow. The test plays the human: it presses Sign in, opens the sign-in page the card links in a second browser tab (the double
// approves at once and redirects back to the loopback address the core listens on), and comes back to a signed-in card.

test('OAuth: sign in from the Library, the tools appear, sign out forgets it', async ({ page, context }) => {
  const provider = process.env.DRYDOCK_E2E_OAUTH_MCP!;
  expect(provider).toContain('/mcp');
  await createProject(page, 'oauth-a');
  await workspaceNav(page, 'nav-library');
  await expect(page.getByRole('heading', { level: 1, name: 'Library' })).toBeVisible();

  await page.getByRole('button', { name: 'Connect an MCP server' }).click();
  // the servers built into Drydock come first; this one is a custom server
  await page.getByTestId('mcp-add-custom').getByRole('button', { name: 'Add a custom server' }).click();
  const form = page.getByTestId('mcp-form');
  await form.getByLabel('Short id', { exact: false }).fill('lin');
  await form.getByLabel('Name', { exact: true }).fill('Hosted tracker');
  await form.getByLabel('Kind').selectOption('http');
  await form.getByLabel('Address', { exact: false }).fill(provider);
  await form.getByLabel('Sign-in').selectOption('oauth');
  await form.getByRole('button', { name: 'Connect server' }).click();

  const card = page.getByTestId('lib-mcp').filter({ hasText: 'Hosted tracker' });
  await expect(card).toBeVisible();
  const panel = card.getByTestId('oauth-panel');
  // not signed in, the sign-in is one control: the Sign in button, nothing else
  await expect(panel).toHaveAttribute('data-signed-in', 'false');
  await expect(panel.getByRole('button')).toHaveCount(1);
  await expect(panel.getByRole('button')).toHaveText('Sign in');
  await expect(panel).toHaveText('Sign in');
  // without a sign-in the server cannot be reached
  await card.getByRole('button', { name: 'Find tools' }).click();
  await expect(card.getByRole('alert')).toContainText('not signed in');

  await panel.getByRole('button', { name: 'Sign in' }).click();
  const link = card.getByTestId('oauth-link');
  await expect(link).toBeVisible({ timeout: 30_000 });
  const href = await link.getAttribute('href');
  expect(href).toContain('/authorize');
  expect(await link.getAttribute('target')).toBe('_blank');

  // the human's browser: open the sign-in page; the provider approves and sends the browser back to the core's loopback address
  const tab = await context.newPage();
  await tab.goto(href!);
  await expect(tab.locator('body')).toContainText('You are signed in');
  await tab.close();

  await expect(panel.getByTestId('oauth-signed-in')).toContainText('Signed in', { timeout: 30_000 });
  await expect(panel.getByRole('button', { name: 'Sign out' })).toBeVisible();
  await expect(card.getByTestId('mcp-tool').filter({ hasText: 'whoami' })).toBeVisible({ timeout: 30_000 });

  await panel.getByRole('button', { name: 'Sign out' }).click();
  await expect(panel).toHaveAttribute('data-signed-in', 'false');
  await expect(panel.getByRole('button', { name: 'Sign out' })).toHaveCount(0);
  await expect(panel.getByRole('button')).toHaveText('Sign in');
});
