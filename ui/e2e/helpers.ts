import { expect, type Page } from '@playwright/test';

export const appUrl = (): string => {
  const u = process.env.DRYDOCK_E2E_URL;
  if (!u) throw new Error('DRYDOCK_E2E_URL is not set (global setup did not run)');
  return u;
};

let nextRepo = 0;
/** A fresh git repository (copy of testdata/fixtures/gocalc) that no other test has touched. */
export function freshRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_REPOS ?? '[]') as string[];
  const r = repos[nextRepo++];
  if (!r) throw new Error('out of fresh repositories: raise REPOS in global-setup.ts');
  return r;
}

/** Open the app with the token, create a project for a fresh repo, and return its name. */
export async function createProject(page: Page, name: string): Promise<void> {
  await page.goto(appUrl());
  // first run (or a later project): the create form
  if (!(await page.getByLabel('Repository path').isVisible().catch(() => false))) {
    await page.getByRole('link', { name: '+ New project' }).click();
  }
  await page.getByLabel('Repository path').fill(freshRepo());
  await page.getByLabel('Project name (optional)').fill(name);
  await page.getByRole('button', { name: 'Create project' }).click();
  await expect(page.getByRole('link', { name: /# general/ })).toBeVisible();
}

export async function createAgent(page: Page, name: string, role: string): Promise<void> {
  await page.getByTestId('nav-team').click();
  await page.getByLabel('Agent name').fill(name);
  await page.getByLabel('Role', { exact: true }).fill(role);
  await page.getByLabel('Adapter', { exact: true }).selectOption('claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: name })).toBeVisible();
}

export async function createAndStartTask(page: Page, title: string, worker: string, reviewer: string): Promise<void> {
  await page.getByTestId('nav-tasks').click();
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill(title);
  await page.getByLabel('Description').fill('Subtract returns 0; make it return a - b.');
  await page.getByLabel('Acceptance criteria').fill('Given 5 and 3 When Subtract is called Then 2');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: new RegExp(title) })).toBeVisible();
  await page.getByLabel('Worker', { exact: true }).selectOption({ label: `${worker} (backend)` });
  await page.getByLabel('Reviewer', { exact: true }).selectOption({ label: `${reviewer} (reviewer)` });
  await page.getByRole('button', { name: 'Assign' }).click();
  await expect(page.getByRole('button', { name: 'Start' })).toBeEnabled();
  await page.getByRole('button', { name: 'Start' }).click();
}
