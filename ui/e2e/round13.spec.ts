import { expect, test } from '@playwright/test';
import { appUrl, freshRepo, roomLink, showNewProjectForm, wizardCreate, wizardNext, wizardToReview, projectNav } from './helpers';

// A project without a repository: made with the wizard's "Later", opened, given a repository from Project settings, then
// used. The harness has no gh, so the wizard's GitHub choice explains that instead of listing repositories.
test('wizard: a project with no repository opens, gets a repository from settings, and then takes a task', async ({ page }) => {
  await page.goto(appUrl());
  await showNewProjectForm(page);

  // GitHub is explained, in the core's words, when gh is not installed
  await page.getByLabel('Project name', { exact: true }).fill('later-project');
  await page.getByLabel('Brief').fill('A calculator library. Keep changes small and tested.');
  await wizardNext(page);
  await page.getByRole('radiogroup', { name: 'Repository source' }).getByRole('radio', { name: 'GitHub' }).check();
  const gh = page.getByTestId('github-unavailable');
  await expect(gh).toContainText('GitHub CLI (gh) is not installed');
  await expect(gh).toContainText('Choose Local folder or Later instead.');
  await page.getByRole('button', { name: 'Back', exact: true }).click();

  // Later: no repository
  await wizardToReview(page, { later: true });
  const summary = page.getByRole('definition').filter({ hasText: 'None yet' });
  await expect(summary).toContainText('attach one later in Project settings');
  await wizardCreate(page);
  await expect(roomLink(page, 'general')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Switch project' })).toContainText('later-project');

  // nothing that needs a repository pretends to work
  await projectNav(page, 'nav-pulls');
  await expect(page.getByTestId('pulls-no-repo')).toContainText('Attach a GitHub repository to see pull requests');

  // attach a local repository from Project settings; the brief is there too
  await projectNav(page, 'nav-project');
  await expect(page.getByTestId('no-repo')).toHaveText('No repository yet');
  await expect(page.getByLabel('Project brief')).toHaveValue('A calculator library. Keep changes small and tested.');
  await expect(page.getByLabel('Mode')).toBeDisabled();
  const attach = page.getByTestId('attach-repo');
  await attach.getByLabel('Repository path').fill(freshRepo());
  await attach.getByRole('button', { name: 'Attach repository' }).click();
  await expect(page.getByTestId('attach-repo')).toHaveCount(0);
  await expect(page.getByTestId('no-repo')).toHaveCount(0);
  await expect(page.getByLabel('Mode')).toBeEnabled();

  // now it works like any project: a task can be created
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Add Multiply');
  await page.getByLabel('Description').fill('Add a Multiply function to the gocalc package.');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Add Multiply/ })).toBeVisible();
});
