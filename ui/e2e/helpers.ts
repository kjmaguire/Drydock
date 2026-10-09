import { expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { inflateRawSync } from 'node:zlib';

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

/** First run shows the create form directly; later runs have a "+ New project" link. Wait for whichever the app renders. */
export async function showNewProjectForm(page: Page): Promise<void> {
  const field = page.getByLabel('Repository path');
  const link = page.getByRole('link', { name: '+ New project' });
  await field.or(link).first().waitFor();
  if (!(await field.isVisible())) await link.click();
  await expect(field).toBeVisible();
}

let nextGreen = 0;
/** A fresh repository whose baseline tests pass and that carries a repository skill (.aet/skills/go-style). */
export function freshGreenRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_GREEN_REPOS ?? '[]') as string[];
  const r = repos[nextGreen++];
  if (!r) throw new Error('out of green repositories: raise GREEN in global-setup.ts');
  return r;
}

/** A folder with one Markdown page, for an expert pack of type local_dir. */
export const docsDir = (): string => process.env.DRYDOCK_E2E_DOCS ?? '';

/** Open the app with the token, create a project for a fresh repo (or the one given), and return its name. */
export async function createProject(page: Page, name: string, repo?: string): Promise<void> {
  await page.goto(appUrl());
  await showNewProjectForm(page);
  await page.getByLabel('Repository path').fill(repo ?? freshRepo());
  await page.getByLabel('Project name (optional)').fill(name);
  await page.getByRole('button', { name: 'Create project' }).click();
  await expect(page.getByRole('link', { name: /# general/ })).toBeVisible();
}

export async function createAgent(page: Page, name: string, role: string): Promise<void> {
  // The form can be replaced while the page is still settling after a project change (seen once: the field detached and the
  // test waited out its whole timeout), so open the page and fill the name as one step that is retried as a whole.
  await expect(async () => {
    await page.getByTestId('nav-team').click();
    await page.getByLabel('Agent name').fill(name, { timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await page.getByLabel('Role', { exact: true }).fill(role);
  await page.getByLabel('Adapter', { exact: true }).selectOption('claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: name })).toBeVisible();
}

/** Create a task, assign it, and stop before Start (so a test can pick a template). */
export async function createAssignedTask(page: Page, title: string, worker: string, reviewer: string, workerRole = 'backend', reviewerRole = 'reviewer'): Promise<void> {
  await page.getByTestId('nav-tasks').click();
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill(title);
  await page.getByLabel('Description').fill('Add a Multiply function to the gocalc package.');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: new RegExp(title) })).toBeVisible();
  await page.getByLabel('Worker', { exact: true }).selectOption({ label: `${worker} (${workerRole})` });
  await page.getByLabel('Reviewer', { exact: true }).selectOption({ label: `${reviewer} (${reviewerRole})` });
  await page.getByRole('button', { name: 'Assign' }).click();
  await expect(page.getByRole('button', { name: 'Start' })).toBeEnabled();
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

/** The `-licensed` TEST HARNESS (a build with a throwaway license key, starting with no license). */
export const licensedUrl = (): string => {
  const u = process.env.DRYDOCK_E2E_LICENSED_URL;
  if (!u) throw new Error('DRYDOCK_E2E_LICENSED_URL is not set (global setup did not run)');
  return u;
};
export const licenseTokens = (): { active: string; expired: string } => ({
  active: process.env.DRYDOCK_E2E_LICENSE_ACTIVE ?? '',
  expired: process.env.DRYDOCK_E2E_LICENSE_EXPIRED ?? '',
});
let nextLicensedRepo = 0;
export function licensedRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_LICENSED_REPOS ?? '[]') as string[];
  const r = repos[nextLicensedRepo++];
  if (!r) throw new Error('out of licensed-harness repositories');
  return r;
}

/** Reads a zip written by the core (stored/deflate entries only) and returns name -> bytes, so a test can look inside a file the UI created. */
export function readZip(path: string): Record<string, Buffer> {
  const buf = readFileSync(path);
  let eocd = -1;
  for (let i = buf.length - 22; i >= 0; i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error(`${path} is not a zip`);
  const count = buf.readUInt16LE(eocd + 10);
  let p = buf.readUInt32LE(eocd + 16);
  const out: Record<string, Buffer> = {};
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error('bad central directory');
    const method = buf.readUInt16LE(p + 10);
    const csize = buf.readUInt32LE(p + 20);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const local = buf.readUInt32LE(p + 42);
    const name = buf.toString('utf8', p + 46, p + 46 + nameLen);
    const lnLen = buf.readUInt16LE(local + 26);
    const leLen = buf.readUInt16LE(local + 28);
    const data = buf.subarray(local + 30 + lnLen + leLen, local + 30 + lnLen + leLen + csize);
    out[name] = method === 0 ? Buffer.from(data) : inflateRawSync(data);
    p += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}
