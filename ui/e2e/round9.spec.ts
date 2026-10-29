import { expect, test } from '@playwright/test';
import { choose, createProject, workspaceNav } from './helpers';

// Round 9: OAuth sign-in of a web MCP server, against the real Go core. The provider is a test double (internal/mcp/oauthfake: an
// MCP server that wants a token, with discovery, dynamic client registration, PKCE, token and refresh); Drydock's side is the real
// flow. The test plays the human: it adds the server on the Integrations page (Your servers, decision P98), presses Sign in in
// its detail, opens the sign-in page it links in a second browser tab (the double approves at once and redirects back to the
// loopback address the core listens on), and comes back to a signed-in server.

test('OAuth: sign in to a server of your own, the tools appear, sign out forgets it', async ({ page, context }) => {
  const provider = process.env.DRYDOCK_E2E_OAUTH_MCP!;
  expect(provider).toContain('/mcp');
  await createProject(page, 'oauth-a');
  await workspaceNav(page, 'nav-integrations');
  await expect(page.getByRole('heading', { level: 1, name: 'Integrations' })).toBeVisible();

  // a server of your own: the dialog opens on its form
  await page.getByRole('button', { name: 'Add MCP server' }).click();
  const form = page.getByTestId('mcp-form');
  await form.getByLabel('Short id', { exact: false }).fill('lin');
  await form.getByLabel('Name', { exact: true }).fill('Hosted tracker');
  await choose(form.getByLabel('Kind'), 'http');
  await form.getByLabel('Address', { exact: false }).fill(provider);
  await choose(form.getByLabel('Sign-in'), 'oauth');
  await form.getByRole('button', { name: 'Connect server' }).click();

  const card = page.getByTestId('server-detail').filter({ hasText: 'Hosted tracker' });
  await expect(card).toBeVisible();
  // the address is the built-in Hosted tracker's, so the core counts it as that card's server: its row (under Agent tools, not
  // Your servers) says Set up until it is signed in
  const list = page.getByRole('navigation', { name: 'All integrations' });
  const row = list.locator('[data-testid="integration-row"][data-kind="faketrk"]');
  await expect(row).toContainText('Set up');
  await expect(list.locator('[data-testid="integration-row"][data-server="lin"]')).toHaveCount(0);
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

  // leave the servers as they were found
  await card.getByRole('button', { name: 'Remove' }).click();
  await card.getByRole('button', { name: 'Yes, remove' }).click();
  await expect(page.getByTestId('integrations-note')).toContainText('Hosted tracker was removed.');
  await expect(page.getByTestId('server-detail')).toHaveCount(0);
  await expect(row).toContainText('Set up');
});
