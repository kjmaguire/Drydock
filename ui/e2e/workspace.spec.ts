// The workspace and the project are separate (2026-10-13, on the owner's request: "separate the Project stuff from the main
// workplace stuff, and give an option to delete a project"). Against the real core: Home lists the projects and the
// workspace areas, the skill library opens without entering a project, a project's page shows only the project, and a
// project is deleted from its settings without touching its repository.
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { createProject, freshRepo, projectSwitcher, sidebar } from './helpers';

test('Home holds the workspace, a project shows only itself, and deleting a project leaves its repository', async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'ws-delete-me', repo);

  // a project page: the project's links, Home first, and nothing of the workspace
  await expect(sidebar(page).getByTestId('nav-needs-you')).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-workspace')).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-library')).toHaveCount(0);
  await expect(sidebar(page).getByTestId('nav-settings')).toHaveCount(0);

  // Home: the projects and the workspace areas; the switcher says Home
  await sidebar(page).getByTestId('nav-workspace').click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible();
  await expect(projectSwitcher(page)).toContainText('Home');
  await expect(page.getByTestId('home-project').filter({ hasText: 'ws-delete-me' })).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-needs-you')).toHaveCount(0);
  await expect(page.getByRole('complementary', { name: 'Details' })).toHaveCount(0);

  // the skill library is a workspace page of its own
  await sidebar(page).getByTestId('nav-library-skills').click();
  await expect(page).toHaveURL(/\/library\/skills$/);
  await expect(page.getByTestId('skill-library')).toBeVisible();
  await expect(page.getByTestId('library-skills')).toBeVisible();

  // back into the project from the workspace sidebar's project link
  await sidebar(page).getByTestId('project-link').filter({ hasText: 'ws-delete-me' }).click();
  await expect(sidebar(page).getByTestId('nav-needs-you')).toBeVisible();
  await expect(projectSwitcher(page)).toContainText('ws-delete-me');

  // delete it from Project settings: the name must be typed
  await sidebar(page).getByTestId('nav-project').click();
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
  await expect(sidebar(page).getByTestId('project-link').filter({ hasText: 'ws-delete-me' })).toHaveCount(0);
  await projectSwitcher(page).click();
  await expect(page.getByRole('menuitemradio', { name: 'ws-delete-me', exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  // the repository is still there, files and all
  expect(existsSync(join(repo, 'calc.go'))).toBe(true);
  expect(existsSync(join(repo, '.git'))).toBe(true);
});
