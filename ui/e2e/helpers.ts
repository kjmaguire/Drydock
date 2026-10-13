import { expect, type Locator, type Page } from '@playwright/test';
import { closeSync, openSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { inflateRawSync } from 'node:zlib';

export const appUrl = (): string => {
  const u = process.env.DRYDOCK_E2E_URL;
  if (!u) throw new Error('DRYDOCK_E2E_URL is not set (global setup did not run)');
  return u;
};

/**
 * Claims the first entry of a list that no test has claimed yet. The claims are files made with O_EXCL in a folder global
 * setup empties, so they hold across the worker processes Playwright starts again after a failure (a counter in this
 * module would start over and hand an earlier test's repository to the next one).
 */
function claim(kind: string, list: string[], short: string): string {
  const dir = process.env.DRYDOCK_E2E_CLAIMS;
  if (!dir) throw new Error('DRYDOCK_E2E_CLAIMS is not set (global setup did not run)');
  for (let i = 0; i < list.length; i++) {
    try {
      closeSync(openSync(join(dir, `${kind}-${i}`), 'wx'));
      return list[i]!;
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'EEXIST') throw err;
    }
  }
  throw new Error(short);
}

/** A fresh git repository (copy of testdata/fixtures/gocalc) that no other test has touched. */
export function freshRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_REPOS ?? '[]') as string[];
  return claim('repo', repos, 'out of fresh repositories: raise REPOS in global-setup.ts');
}

/** The sidebar (the app's main navigation). */
export const sidebar = (page: Page): Locator => page.getByRole('navigation', { name: 'Main' });

/**
 * A room's link in the sidebar. Rooms are named without a "#" (an icon, then the name, then an unread count such as
 * ", 3 unread"), so the accessible name starts with the room name and nothing word-like follows it.
 */
export function roomLink(page: Page, name: string): Locator {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return sidebar(page).getByRole('link', { name: new RegExp(`^${escaped}(?![\\w-])`) });
}

/**
 * Open a workspace page (Library, Integrations, Connections, Settings...) from the sidebar. On a project's page the sidebar
 * shows the project, so Home comes first; on a workspace page the link is already there.
 */
export async function workspaceNav(page: Page, testId: string): Promise<void> {
  const link = sidebar(page).getByTestId(testId);
  if (!(await link.isVisible())) await sidebar(page).getByTestId('nav-workspace').click();
  await link.click();
}

/**
 * Open a page of a project (Team, Tasks, Project settings...) from the sidebar. On a workspace page the sidebar shows the
 * workspace, so the project is opened first: the one named, else the one the app remembers (its home, "/").
 */
export async function projectNav(page: Page, testId: string, project?: string): Promise<void> {
  const link = sidebar(page).getByTestId(testId);
  if (!(await link.isVisible())) {
    if (project) await sidebar(page).getByTestId('project-link').filter({ hasText: project }).click();
    else await page.goto(new URL('/', page.url()).toString());
  }
  await link.click();
}

/** The project switcher at the top of the sidebar: opens the menu of Home, the projects, "New project…" and "App settings". */
export const projectSwitcher = (page: Page): Locator => page.getByRole('button', { name: 'Switch project' });

/** Switch to a project through the switcher's menu (exact name: "gocalc-approve" must not match a button "Approve"). */
export async function switchProject(page: Page, name: string): Promise<void> {
  await projectSwitcher(page).click();
  await page.getByRole('menuitemradio', { name, exact: true }).click();
  await expect(projectSwitcher(page)).toContainText(name);
}

/** The new-project wizard: the first run shows it directly; later runs open it from the switcher's "New project…". */
export async function showNewProjectForm(page: Page): Promise<void> {
  const field = page.getByLabel('Project name', { exact: true });
  await field.or(projectSwitcher(page)).first().waitFor();
  if (!(await field.isVisible())) {
    await projectSwitcher(page).click();
    await page.getByRole('menuitem', { name: 'New project…', exact: true }).click();
  }
  await expect(field).toBeVisible();
}

/** The wizard's Next button. */
export async function wizardNext(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Next', exact: true }).click();
}

/**
 * Walk the open wizard to its Review step: Basics (the name, when given), Repository (a local folder, or Later), no
 * connectors, and an empty team (the suggested agents are removed: tests add their own agents on the Team page).
 */
export async function wizardToReview(page: Page, opts: { name?: string; repo?: string; later?: boolean }): Promise<void> {
  if (opts.name) await page.getByLabel('Project name', { exact: true }).fill(opts.name);
  await wizardNext(page);
  const source = page.getByRole('radiogroup', { name: 'Repository source' });
  if (opts.later) {
    await source.getByRole('radio', { name: 'Later' }).check();
  } else {
    await source.getByRole('radio', { name: 'Local folder' }).check();
    await page.getByLabel('Repository path').fill(opts.repo ?? freshRepo());
  }
  await wizardNext(page); // Connectors
  await wizardNext(page); // Team
  await expect(page.getByTestId('team-list')).toHaveAttribute('data-ready', 'true');
  const removeAll = page.getByRole('button', { name: 'Remove all', exact: true });
  if (await removeAll.isVisible()) await removeAll.click();
  await expect(page.getByTestId('team-member')).toHaveCount(0);
  await wizardNext(page); // Review
  await expect(page.locator('form[data-step="Review"]')).toBeVisible();
}

/** Create from the Review step and open the new project. */
export async function wizardCreate(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Create project' }).click();
  await page.getByRole('button', { name: 'Open project' }).click();
}

/** A fresh repository whose baseline tests pass and that carries a repository skill (.aet/skills/go-style). */
export function freshGreenRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_GREEN_REPOS ?? '[]') as string[];
  return claim('green', repos, 'out of green repositories: raise GREEN in global-setup.ts');
}

/** A folder with one Markdown page, for an expert pack of type local_dir. */
export const docsDir = (): string => process.env.DRYDOCK_E2E_DOCS ?? '';

/** Open the app with the token, create a project for a fresh repo (or the one given), and return its name. */
export async function createProject(page: Page, name: string, repo?: string): Promise<void> {
  await page.goto(appUrl());
  await showNewProjectForm(page);
  await wizardToReview(page, { name, repo: repo ?? freshRepo() });
  await wizardCreate(page);
  await expect(roomLink(page, 'general')).toBeVisible();
}

export async function createAgent(page: Page, name: string, role: string): Promise<void> {
  // The form can be replaced while the page is still settling after a project change (seen once: the field detached and the
  // test waited out its whole timeout), so open the page and fill the name as one step that is retried as a whole.
  await expect(async () => {
    await projectNav(page, 'nav-team');
    await page.getByLabel('Agent name').fill(name, { timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await page.getByLabel('Role', { exact: true }).fill(role);
  await page.getByLabel('Adapter', { exact: true }).selectOption('claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: name })).toBeVisible();
}

/** Create a task, assign it, and stop before Start (so a test can pick a template). */
export async function createAssignedTask(page: Page, title: string, worker: string, reviewer: string, workerRole = 'backend', reviewerRole = 'reviewer'): Promise<void> {
  await projectNav(page, 'nav-tasks');
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
  await projectNav(page, 'nav-tasks');
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
export function licensedRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_LICENSED_REPOS ?? '[]') as string[];
  return claim('licensed', repos, 'out of licensed-harness repositories');
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
