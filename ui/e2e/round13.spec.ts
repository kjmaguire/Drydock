import { expect, test } from '@playwright/test';
import { appUrl, createProjectWithoutRepo, freshRepo, openNewProject, projectItem, projectNav } from './helpers';

// A project without a repository (made through the core: New project always gives one a folder), opened, given a repository from
// Project settings, then used. The harness has no gh, so New project's Clone from GitHub explains that instead of listing
// repositories.
test('a project with no repository opens, gets a repository from settings, and then takes a task', async ({ page }) => {
  await page.goto(appUrl());
  await openNewProject(page);

  // GitHub is explained, in the core's words, when gh is not installed
  const dialog = page.getByTestId('new-project-dialog');
  await dialog.getByRole('radio', { name: 'Clone from GitHub' }).click();
  const gh = dialog.getByTestId('github-unavailable');
  await expect(gh).toContainText('GitHub CLI (gh) is not installed');
  await expect(gh).toContainText('Choose New folder or Open a folder instead.');
  await expect(dialog.getByTestId('new-project-create')).toBeDisabled();
  await page.keyboard.press('Escape');

  // no repository
  await createProjectWithoutRepo(page, 'later-project', 'A calculator library. Keep changes small and tested.');
  await expect(projectItem(page, 'later-project')).toHaveAttribute('aria-expanded', 'true');

  // nothing that needs a repository pretends to work
  await projectNav(page, 'nav-pulls');
  await expect(page.getByTestId('pulls-no-repo')).toContainText('Attach a GitHub repository to see pull requests');

  // attach a local repository from Project settings; the brief is there too
  await projectNav(page, 'nav-project');
  await expect(page.getByTestId('no-repo')).toHaveText('No repository yet');
  await expect(page.getByLabel('Project brief')).toHaveValue('A calculator library. Keep changes small and tested.');
  await expect(page.getByLabel('Mode', { exact: true })).toBeDisabled();
  const attach = page.getByTestId('attach-repo');
  await attach.getByLabel('Repository path').fill(freshRepo());
  await attach.getByRole('button', { name: 'Attach repository' }).click();
  await expect(page.getByTestId('attach-repo')).toHaveCount(0);
  await expect(page.getByTestId('no-repo')).toHaveCount(0);
  await expect(page.getByLabel('Mode', { exact: true })).toBeEnabled();

  // now it works like any project: a task can be created
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Add Multiply');
  await page.getByLabel('Description').fill('Add a Multiply function to the gocalc package.');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Add Multiply/ })).toBeVisible();
});
