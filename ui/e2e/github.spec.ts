// The GitHub settings page (decision P98; one page since 2026-10-16, on the owner's question "a separate section in settings for
// just github?"): reached from Integrations, with the gh sign-in on top, Access (the sign-in, issue sync, the MCP server for
// agents), the projects' repositories and the CI runners (runners.spec.ts drives them). Against the real core, whose PATH has no
// gh (global-setup.ts), so the sign-in says so in the core's words and a repository needs gh.
import { expect, test, type Page } from '@playwright/test';
import { createProject, workspaceNav } from './helpers';

/**
 * Opens the GitHub page the way a person does: Integrations, then its GitHub row (workspaceNav). Until the rebuilt Integrations
 * page lists GitHub in its "All integrations" navigation, the page is opened by its address.
 */
async function openGitHub(page: Page): Promise<void> {
  await workspaceNav(page, 'nav-integrations');
  const row = page.getByRole('navigation', { name: 'All integrations' }).locator('a[href="/github"]');
  if (await row.count()) await row.click();
  else await page.goto(new URL('/github', page.url()).toString());
  await expect(page.getByTestId('github-page')).toBeVisible();
}

test('GitHub: one settings page with the gh sign-in, access, the repositories and the CI runners, reached from Integrations', async ({ page }) => {
  await createProject(page, 'gocalc-github');

  await openGitHub(page);
  await expect(page).toHaveURL(/\/github$/);
  await expect(page.getByRole('heading', { level: 1, name: 'GitHub', exact: true })).toBeVisible();
  // its hub is Integrations: the sidebar marks it, and the breadcrumb leads back to it
  await expect(page.getByRole('navigation', { name: 'Main' }).getByTestId('nav-side-integrations')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Integrations' })).toHaveAttribute('href', '/integrations');
  await expect(page.getByTestId('github-page').locator('header [data-testid=learn-more][data-topic=github]')).toHaveAttribute('href', '/guide/github');

  // the sign-in: the harness has no gh, and the page says so with the fix; there is no sign-in button of Drydock's own
  const sign = page.getByTestId('gh-signin-state');
  await expect(sign).toHaveAttribute('data-signed-in', 'false');
  await expect(sign).toContainText('GitHub CLI not found');
  await expect(sign.locator('a[href="https://cli.github.com"]')).toBeVisible();
  await expect(sign.getByRole('button')).toHaveText(['Check again']);

  // Access, as rows: the sign-in, issue sync (connected inline with the integrations screen's own form) and the MCP server
  const access = page.getByRole('region', { name: 'Access' });
  await expect(access.getByTestId('access-gh')).toContainText('Not installed');
  await expect(access.getByTestId('sync-state')).toContainText('Off');
  await expect(access.getByTestId('sync-connect').getByRole('button', { name: 'Use the GitHub CLI sign-in' })).toBeVisible();
  await expect(access.getByTestId('mcp-state')).toContainText('Not connected');
  await access.getByTestId('mcp-open').click();
  const dialog = page.getByRole('dialog', { name: 'MCP server for agents' });
  await expect(dialog.locator('[data-testid="connector"][data-id="gh"]')).toBeVisible();
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByTestId('gh-tokens')).toContainText('Windows credential store, never shown');

  // the repositories of the projects: this one's is a local folder (no GitHub remote)
  const repos = page.getByRole('table', { name: 'Repositories' });
  const row = repos.getByTestId('repo-row').filter({ hasText: 'gocalc-github' });
  await expect(row).toHaveAttribute('data-state', 'local');
  await expect(row).toContainText('Local folder');
  await expect(row.getByTestId('repo-open')).toHaveAttribute('href', '/');

  // the CI runners are managed on this page, and not on Agents (once Connections)
  const runners = page.getByTestId('ci-runners-section');
  await expect(runners).toBeVisible();
  await expect(runners.getByTestId('runner-private-only')).toContainText('Private repositories only');
  await expect(runners.getByTestId('runner-slots')).toContainText('No runners yet.');
  await expect(runners.locator('[data-testid=learn-more][data-topic=runners]')).toHaveAttribute('href', '/guide/runners');
  await workspaceNav(page, 'nav-connections');
  await expect(page.getByRole('heading', { level: 1, name: 'Agents' })).toBeVisible();
  await expect(page.getByTestId('agent-detail').getByTestId('cli-gateway')).toBeVisible();
  await expect(page.getByTestId('ci-runners-section')).toHaveCount(0);

  // the breadcrumb goes back to Integrations
  await openGitHub(page);
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Integrations' }).click();
  await expect(page).toHaveURL(/\/integrations$/);

  // the Integrations page keeps GitHub as a row of its list, a link to this page (decision P98); its detail there points here too
  await workspaceNav(page, 'nav-integrations');
  await page.getByRole('navigation', { name: 'All integrations' }).locator('[data-testid="integration-row"][data-kind="github"]').click();
  await expect(page).toHaveURL(/\/github$/);
  await expect(page.getByRole('heading', { level: 1, name: 'GitHub' })).toBeVisible();
  await page.goto(new URL('/integrations/github', page.url()).toString());
  await page.getByTestId('github-page-link').click();
  await expect(page).toHaveURL(/\/github$/);
});
