// The GitHub settings page (2026-10-16, on the owner's question "a separate section in settings for just github?"): one page
// behind the gear with the gh sign-in, what Drydock does with GitHub, the GitHub connection and MCP server (the Integrations
// tile's own panel) and the repositories with CI runners. Against the real core, whose PATH has no gh (global-setup.ts), so the
// sign-in says so in the core's words.
import { expect, test } from '@playwright/test';
import { createProject, workspaceNav } from './helpers';

test('GitHub: one settings page gathers the gh sign-in, the connection, the MCP server and the runners; the Integrations tile points to it', async ({ page }) => {
  await createProject(page, 'gocalc-github');

  await workspaceNav(page, 'nav-github');
  await expect(page).toHaveURL(/\/github$/);
  await expect(page.getByRole('heading', { level: 1, name: 'GitHub' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Settings' }).getByTestId('nav-github')).toHaveAttribute('aria-current', 'page');

  // the sign-in: the harness has no gh, and the page says so in the core's words, with no sign-in button of its own
  const sign = page.getByTestId('gh-signin-state');
  await expect(sign).toHaveAttribute('data-signed-in', 'false');
  await expect(sign).toContainText('not installed');
  await expect(sign.getByRole('button')).toHaveCount(0);
  // the claim is about the gh route only, and the page says where the other sign-ins and tokens are kept (audit L45)
  await expect(page.getByTestId('gh-signin')).toContainText('On this route Drydock reads and stores no GitHub token');

  // what it does is in the Guide; here the connection with the gh MCP server (the same panel as the Integrations tile) and the runners
  await expect(page.getByTestId('github-page').getByTestId('learn-more')).toHaveAttribute('href', '/guide/github');
  const connection = page.getByTestId('gh-connection');
  await expect(connection.locator('[data-testid="integration"][data-kind="github"]')).toBeVisible();
  await expect(connection.getByRole('button', { name: 'Use the GitHub CLI sign-in' })).toBeVisible();
  await expect(connection.locator('[data-testid="connector"][data-id="gh"]')).toBeVisible();
  await expect(connection.locator('[data-testid="integration-tile"]')).toHaveCount(0);
  await expect(page.getByTestId('gh-runners-none')).toBeVisible();

  // runners are managed on Connections, where the link lands
  await page.getByTestId('gh-runners-link').click();
  await expect(page).toHaveURL(/\/connections$/);
  await expect(page.getByTestId('ci-runners-section')).toBeVisible();

  // the Integrations page keeps its GitHub tile, whose panel points to the page
  await workspaceNav(page, 'nav-integrations');
  await page.locator('[data-testid="integration-tile"][data-kind="github"]').click();
  await page.getByTestId('github-page-link').click();
  await expect(page).toHaveURL(/\/github$/);
  await expect(page.getByRole('heading', { level: 1, name: 'GitHub' })).toBeVisible();
});
