// The workspace and the project are separate (2026-10-13, on the owner's request: "separate the Project stuff from the main
// workplace stuff, and give an option to delete a project"). Against the real core: Home lists the projects and the
// workspace areas, the skill library opens without entering a project, a project's page shows only the project, and a
// project is deleted from its settings without touching its repository.
import { existsSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { appUrl, createAgent, createProject, freshRepo, projectNav, projectSwitcher, roomLink, showNewProjectForm, sidebar, wizardCreate, wizardNext, workspaceNav } from './helpers';

test('Home holds the workspace, a project shows only itself, and deleting a project leaves its repository', async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'ws-delete-me', repo);

  // a project page (graphite sidebar, P97): the Inbox, the Drydock mark as the way home, the open project's tabs under it, the
  // workspace's pages and Settings on every page
  await expect(sidebar(page).getByTestId('nav-needs-you')).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-workspace')).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-overview')).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-library')).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-settings')).toBeVisible();

  // Home: the projects and the workspace areas; the switcher says Home
  await sidebar(page).getByTestId('nav-workspace').click();
  await expect(page).toHaveURL(/\/home$/);
  // Home greets by the time of day (Crew HQ, decision P64); the switcher says where you are
  await expect(page.getByRole('heading', { level: 1, name: /^Good (morning|afternoon|evening)$/ })).toBeVisible();
  await expect(projectSwitcher(page)).toContainText('Home');
  await expect(page.getByTestId('home-project').filter({ hasText: 'ws-delete-me' })).toBeVisible();
  // the project's tabs leave with it; the Inbox stays
  await expect(sidebar(page).getByTestId('nav-overview')).toHaveCount(0);
  await expect(sidebar(page).getByTestId('nav-needs-you')).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Details' })).toHaveCount(0);

  // the skill library is a page of the Library tab
  await workspaceNav(page, 'nav-library-skills');
  await expect(page).toHaveURL(/\/library\/skills$/);
  await expect(page.getByTestId('skill-library')).toBeVisible();
  await expect(page.getByTestId('library-skills')).toBeVisible();

  // back into the project from its card on Home
  await projectNav(page, 'nav-overview', 'ws-delete-me');
  await expect(sidebar(page).getByTestId('nav-needs-you')).toBeVisible();
  await expect(projectSwitcher(page)).toContainText('ws-delete-me');

  // delete it from Project settings (behind the gear): the name must be typed
  await projectNav(page, 'nav-project');
  const section = page.getByTestId('delete-project');
  await expect(section).toContainText('its branches and its commits are not touched');
  await section.getByTestId('delete-project-open').click();
  const confirm = section.getByTestId('delete-project-confirm');
  await expect(confirm).toBeDisabled();
  await section.getByTestId('delete-confirm').fill('ws-delete');
  await expect(confirm).toBeDisabled();
  await section.getByTestId('delete-confirm').fill('ws-delete-me');
  await confirm.click();

  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByTestId('home-flash')).toHaveText('ws-delete-me was deleted.');
  await expect(page.getByTestId('home-project').filter({ hasText: 'ws-delete-me' })).toHaveCount(0);
  await projectSwitcher(page).click();
  await expect(page.getByRole('menuitemradio', { name: 'ws-delete-me', exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  // the repository is still there, files and all
  expect(existsSync(join(repo, 'calc.go'))).toBe(true);
  expect(existsSync(join(repo, '.git'))).toBe(true);
});

test("workspace pages name no project; a project's own settings are pills of its Overview, not behind the gear; the agent CLIs and their MCP gateway switch are on Agents", async ({ page }) => {
  await createProject(page, 'ws-split');
  await createAgent(page, 'Split agent', 'backend');

  // the project's Integrations page is a pill of the project's Overview (with Project settings beside it), the Overview tab is
  // current, the project's bar stays, and no Settings rail is on the page
  await projectNav(page, 'nav-project-integrations');
  await expect(page).toHaveURL(/\/project\/integrations$/);
  await expect(page.getByTestId('project-integrations')).toContainText('ws-split');
  const section = page.getByRole('navigation', { name: 'Section' });
  await expect(section.getByTestId('nav-project-integrations')).toHaveAttribute('aria-current', 'page');
  await expect(section.getByTestId('nav-project')).toHaveText('Project settings');
  await expect(sidebar(page).getByTestId('nav-overview')).toHaveAttribute('aria-current', 'page');
  await expect(sidebar(page).getByTestId('nav-settings')).not.toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('navigation', { name: 'Settings' })).toHaveCount(0);
  await expect(sidebar(page).getByTestId('nav-needs-you')).toBeVisible();
  // laid out like the workspace's Integrations page: Services, then Agent tools, each a list of compact rows
  await expect(page.getByTestId('project-app-sync').getByRole('heading', { name: 'Services', level: 2 })).toBeVisible();
  await expect(page.getByTestId('project-mcp').getByRole('heading', { name: 'Agent tools', level: 2 })).toBeVisible();
  await expect(page.getByTestId('add-from-library')).toHaveCount(0);

  // the workspace's Library and Integrations name no project and none of its agents
  for (const nav of ['nav-library', 'nav-integrations']) {
    await workspaceNav(page, nav);
    const main = page.locator('#main');
    await expect(main.locator('h1')).toBeVisible();
    await expect(main).not.toContainText('ws-split');
    await expect(main).not.toContainText('Split agent');
  }
  await expect(page.getByTestId('cli-gateway')).toHaveCount(0);

  // behind the gear: the workspace's pages and the app's, nothing of this project; GitHub is a row of Integrations (decision P98)
  await workspaceNav(page, 'nav-settings');
  const rail = page.getByRole('navigation', { name: 'Settings' });
  await expect(rail.getByRole('heading')).toHaveText(['Workspace', 'App']);
  await expect(rail.getByTestId('nav-project')).toHaveCount(0);
  await expect(rail.getByTestId('nav-project-integrations')).toHaveCount(0);
  await expect(rail.getByTestId('nav-github')).toHaveCount(0);
  await workspaceNav(page, 'nav-integrations');
  await expect(page.getByRole('navigation', { name: 'All integrations' }).locator('[data-testid="integration-row"][data-key="github"]')).toContainText('GitHub');
  await workspaceNav(page, 'nav-settings');
  await expect(rail).not.toContainText('ws-split');

  // Agents: the agent CLIs as a list, the chosen one's detail with the MCP gateway's setting for it (which CLIs receive MCP servers)
  await workspaceNav(page, 'nav-connections');
  await expect(page.getByRole('heading', { level: 1, name: 'Agents' })).toBeVisible();
  const list = page.getByRole('navigation', { name: 'Agent CLIs' }).or(page.getByText('No agent CLIs found'));
  await expect(list.first()).toBeVisible();
  if (await page.getByRole('navigation', { name: 'Agent CLIs' }).isVisible()) {
    const gateway = page.getByTestId('agent-detail').getByRole('region', { name: 'MCP gateway' });
    await expect(gateway.getByRole('heading', { name: 'MCP gateway' })).toBeVisible();
    await expect(gateway.getByTestId('gateway-state')).toBeVisible();
  }
});

// Decision P58: a project from scratch. "Choose folder..." opens the window's folder dialog (the harness's labelled stand-in
// "chooses" a folder that does not exist yet), the field says what is there and recommends a Dev Drive (the stand-in reports
// D:), and with "Create a Git repository here" ticked Drydock makes the folder, the repository (branch main) and its first
// commit, and the project works in it.
test('a project from scratch: Choose folder..., and Drydock makes the Git repository', async ({ page }) => {
  await page.goto(appUrl());
  await showNewProjectForm(page);
  await page.getByLabel('Project name', { exact: true }).fill('ws-from-scratch');
  await wizardNext(page);
  await page.getByRole('radiogroup', { name: 'Repository source' }).getByRole('radio', { name: 'Local folder' }).check();
  await expect(page.getByTestId('folder-hint')).toContainText('Dev Drive D: found.');
  await page.getByTestId('folder-choose').click();
  const field = page.getByLabel('Repository path');
  await expect(field).toHaveValue(/Fresh Project \d+$/);
  const folder = await field.inputValue();
  await expect(page.getByTestId('folder-check')).toHaveAttribute('data-state', 'missing');
  await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled();
  await page.getByLabel(/^Create a Git repository here/).check();
  await wizardNext(page); // Connectors
  await wizardNext(page); // Team
  await expect(page.getByTestId('team-list')).toHaveAttribute('data-ready', 'true');
  const removeAll = page.getByRole('button', { name: 'Remove all', exact: true });
  if (await removeAll.isVisible()) await removeAll.click();
  await wizardNext(page); // Review
  await expect(page.getByTestId('review-init')).toContainText('new Git repository here');
  await wizardCreate(page);
  await expect(roomLink(page, 'general')).toBeVisible();
  expect(existsSync(join(folder, '.git'))).toBe(true);
  // the project works in it: Project settings show no attach form
  await projectNav(page, 'nav-project');
  // the project shows the folder's full path (Windows' temp folders can be short 8.3 paths)
  await expect(page.locator('code').filter({ hasText: realpathSync.native(folder) }).first()).toBeVisible();
  await expect(page.getByTestId('no-repo')).toHaveCount(0);
  await expect(page.getByTestId('attach-repo')).toHaveCount(0);
});
